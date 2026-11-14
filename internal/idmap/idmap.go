package idmap

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
)

type Kind string

const (
	KindSelf  Kind = "self"
	KindUser  Kind = "user"
	KindGroup Kind = "group"
)

// baseNum 分配号段的起点, 避免和真实 QQ 号冲突
const baseNum int64 = 10_000_000_000

type openidKey struct {
	kind   Kind
	openid string
}

type numKey struct {
	kind Kind
	num  int64
}

// Map openid <-> int64 映射, 内存缓存 + PocketBase 持久化
type Map struct {
	app   core.App
	botID string

	mu     sync.Mutex
	byName map[openidKey]int64
	byNum  map[numKey]string
	extra  map[openidKey]string // 最近一次写入的 extra, 内容未变则跳过写库

	msgByName map[string]int64
	msgByNum  map[int64]string
}

func New(app core.App, botID string) *Map {
	return &Map{
		app:       app,
		botID:     botID,
		byName:    map[openidKey]int64{},
		byNum:     map[numKey]string{},
		extra:     map[openidKey]string{},
		msgByName: map[string]int64{},
		msgByNum:  map[int64]string{},
	}
}

// HasMessage 判断该 QQ 消息 id 是否已被本机器人登记过
// 发送成功时 recordSent 会登记, 所以命中说明是自己发出去的消息又回流了
func (m *Map) HasMessage(openid string) bool {
	if openid == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.msgByName[openid]; ok {
		return true
	}
	rec, err := m.app.FindFirstRecordByFilter(
		db.TableMessageMap,
		"bot = {:bot} && openid = {:openid}",
		dbx.Params{"bot": m.botID, "openid": openid},
	)
	if err != nil || rec == nil {
		return false
	}
	m.rememberMsg(openid, int64(rec.GetFloat("num")))
	return true
}

// MessageNum 获取消息 id 对应的 int64, 不存在则分配
func (m *Map) MessageNum(openid string, extra map[string]any) (int64, error) {
	if openid == "" {
		return 0, fmt.Errorf("消息 id 为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if n, ok := m.msgByName[openid]; ok {
		return n, nil
	}

	rec, err := m.app.FindFirstRecordByFilter(
		db.TableMessageMap,
		"bot = {:bot} && openid = {:openid}",
		dbx.Params{"bot": m.botID, "openid": openid},
	)
	switch {
	case err == nil:
		n := int64(rec.GetFloat("num"))
		m.rememberMsg(openid, n)
		return n, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}

	var num int64
	err = m.app.RunInTransaction(func(tx core.App) error {
		recs, err := tx.FindRecordsByFilter(db.TableMessageMap, "bot = {:bot}", "-num", 1, 0,
			dbx.Params{"bot": m.botID})
		if err != nil {
			return err
		}
		num = baseNum
		if len(recs) > 0 {
			num = int64(recs[0].GetFloat("num")) + 1
		}

		col, err := tx.FindCachedCollectionByNameOrId(db.TableMessageMap)
		if err != nil {
			return err
		}
		rec := core.NewRecord(col)
		rec.Set("bot", m.botID)
		rec.Set("openid", openid)
		rec.Set("num", num)
		if extra != nil {
			rec.Set("extra", extra)
		}
		return tx.Save(rec)
	})
	if err != nil {
		return 0, err
	}

	m.rememberMsg(openid, num)
	return num, nil
}

// MessageOpenID 由 int64 反查消息 id
func (m *Map) MessageOpenID(num int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.msgByNum[num]; ok {
		return s, nil
	}

	rec, err := m.app.FindFirstRecordByFilter(
		db.TableMessageMap,
		"bot = {:bot} && num = {:num}",
		dbx.Params{"bot": m.botID, "num": num},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("找不到消息映射: %d", num)
		}
		return "", err
	}
	openid := rec.GetString("openid")
	m.rememberMsg(openid, num)
	return openid, nil
}

func (m *Map) rememberMsg(openid string, num int64) {
	m.msgByName[openid] = num
	m.msgByNum[num] = openid
}

// SetExtra 合并更新映射的附加信息(昵称/头像/角色等)
// 空字符串不覆盖已有值, 避免私聊消息把群名片冲掉
func (m *Map) SetExtra(kind Kind, openid string, extra map[string]any) {
	if openid == "" || extra == nil {
		return
	}

	rec, err := m.app.FindFirstRecordByFilter(
		db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && openid = {:openid}",
		dbx.Params{"bot": m.botID, "kind": string(kind), "openid": openid},
	)
	if err != nil {
		return
	}

	var current map[string]any
	_ = rec.UnmarshalJSONField("extra", &current)
	if current == nil {
		current = map[string]any{}
	}

	changed := false
	for k, v := range extra {
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		current[k] = v
		changed = true
	}
	if !changed {
		return
	}

	raw, err := json.Marshal(current)
	if err != nil {
		return
	}
	s := string(raw)

	key := openidKey{kind, openid}
	m.mu.Lock()
	if m.extra[key] == s {
		m.mu.Unlock()
		return
	}
	m.extra[key] = s
	m.mu.Unlock()

	rec.Set("extra", current)
	if err := m.app.Save(rec); err != nil {
		m.forgetExtra(key)
	}
}

func (m *Map) forgetExtra(key openidKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.extra, key)
}

// Num 获取 openid 对应的 int64, 不存在则分配并持久化
func (m *Map) Num(kind Kind, openid string) (int64, error) {
	return m.NumWithExtra(kind, openid, nil)
}

// NumWithExtra 同 Num, 首次创建时一并写入 extra
func (m *Map) NumWithExtra(kind Kind, openid string, extra map[string]any) (int64, error) {
	if openid == "" {
		return 0, fmt.Errorf("openid 为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := openidKey{kind, openid}
	if n, ok := m.byName[k]; ok {
		return n, nil
	}

	rec, err := m.app.FindFirstRecordByFilter(
		db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && openid = {:openid}",
		dbx.Params{"bot": m.botID, "kind": string(kind), "openid": openid},
	)
	switch {
	case err == nil:
		n := int64(rec.GetFloat("num"))
		m.remember(kind, openid, n)
		return n, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}

	// 首次出现, 事务内分配
	var num int64
	err = m.app.RunInTransaction(func(tx core.App) error {
		recs, err := tx.FindRecordsByFilter(db.TableIDMap, "bot = {:bot}", "-num", 1, 0, dbx.Params{"bot": m.botID})
		if err != nil {
			return err
		}
		num = baseNum
		if len(recs) > 0 {
			num = int64(recs[0].GetFloat("num")) + 1
		}

		col, err := tx.FindCachedCollectionByNameOrId(db.TableIDMap)
		if err != nil {
			return err
		}
		rec := core.NewRecord(col)
		rec.Set("bot", m.botID)
		rec.Set("kind", string(kind))
		rec.Set("openid", openid)
		rec.Set("num", num)
		if extra != nil {
			rec.Set("extra", extra)
		}
		return tx.Save(rec)
	})
	if err != nil {
		return 0, err
	}

	m.remember(kind, openid, num)
	return num, nil
}

// OpenID 由 int64 反查 openid
func (m *Map) OpenID(kind Kind, num int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := numKey{kind, num}
	if s, ok := m.byNum[k]; ok {
		return s, nil
	}

	rec, err := m.app.FindFirstRecordByFilter(
		db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && num = {:num}",
		dbx.Params{"bot": m.botID, "kind": string(kind), "num": num},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("找不到 %s 映射: %d", kind, num)
		}
		return "", err
	}
	openid := rec.GetString("openid")
	m.remember(kind, openid, num)
	return openid, nil
}

// NumExtra 由 int64 反查 openid 与 extra(昵称/名片/角色等)
func (m *Map) NumExtra(kind Kind, num int64) (string, map[string]any, error) {
	rec, err := m.app.FindFirstRecordByFilter(
		db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && num = {:num}",
		dbx.Params{"bot": m.botID, "kind": string(kind), "num": num},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, fmt.Errorf("找不到 %s 映射: %d", kind, num)
		}
		return "", nil, err
	}

	openid := rec.GetString("openid")
	m.mu.Lock()
	m.remember(kind, openid, num)
	m.mu.Unlock()

	var extra map[string]any
	_ = rec.UnmarshalJSONField("extra", &extra)
	return openid, extra, nil
}

// Numbers 返回 openid -> 管理员填写的真实号码
// QQ 官方只给 openid, 既不提供群号也不提供 QQ 号, 只能人工填写在记录上
func (m *Map) Numbers(kind Kind) (map[string]int64, error) {
	recs, err := m.app.FindRecordsByFilter(db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && qq_number > 0", "", 0, 0,
		dbx.Params{"bot": m.botID, "kind": string(kind)},
	)
	if err != nil {
		return nil, err
	}

	numbers := make(map[string]int64, len(recs))
	for _, rec := range recs {
		numbers[rec.GetString("openid")] = int64(rec.GetFloat("qq_number"))
	}
	return numbers, nil
}

// OpenIDByNumber 按真实号码(群号 / QQ 号)反查 openid
func (m *Map) OpenIDByNumber(kind Kind, number int64) (string, error) {
	recs, err := m.app.FindRecordsByFilter(db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && qq_number = {:num}", "", 1, 0,
		dbx.Params{"bot": m.botID, "kind": string(kind), "num": number},
	)
	if err != nil {
		return "", err
	}
	if len(recs) == 0 {
		return "", errors.New("未找到该号码")
	}
	return recs[0].GetString("openid"), nil
}

// Fallback 返回群记录上配的真实群号与 @全体 备用端记录 id
// 任一缺失时 ok 为 false, 表示不触发备用端
func (m *Map) Fallback(groupOpenID string) (groupNumber int64, fallbackID string, ok bool) {
	rec, err := m.app.FindFirstRecordByFilter(db.TableIDMap,
		"bot = {:bot} && kind = 'group' && openid = {:openid}",
		dbx.Params{"bot": m.botID, "openid": groupOpenID},
	)
	if err != nil || rec == nil {
		return 0, "", false
	}
	groupNumber = int64(rec.GetInt("qq_number"))
	fallbackID = rec.GetString("at_fallback")
	return groupNumber, fallbackID, groupNumber > 0 && fallbackID != ""
}

// ListNums 列出某类型的全部 num(用于 get_group_list)
func (m *Map) ListNums(kind Kind) ([]int64, error) {
	recs, err := m.app.FindRecordsByFilter(
		db.TableIDMap,
		"bot = {:bot} && kind = {:kind}",
		"num", 0, 0,
		dbx.Params{"bot": m.botID, "kind": string(kind)},
	)
	if err != nil {
		return nil, err
	}

	nums := make([]int64, 0, len(recs))
	for _, rec := range recs {
		nums = append(nums, int64(rec.GetFloat("num")))
	}
	return nums, nil
}

// MessageExtra 由消息 num 反查官方消息 id 与 extra(scene/target)
func (m *Map) MessageExtra(num int64) (string, map[string]any, error) {
	rec, err := m.app.FindFirstRecordByFilter(
		db.TableMessageMap,
		"bot = {:bot} && num = {:num}",
		dbx.Params{"bot": m.botID, "num": num},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, fmt.Errorf("找不到消息映射: %d", num)
		}
		return "", nil, err
	}

	var extra map[string]any
	_ = rec.UnmarshalJSONField("extra", &extra)
	return rec.GetString("openid"), extra, nil
}

func (m *Map) remember(kind Kind, openid string, num int64) {
	m.byName[openidKey{kind, openid}] = num
	m.byNum[numKey{kind, num}] = openid
}
