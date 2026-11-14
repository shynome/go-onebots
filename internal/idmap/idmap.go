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

// SetExtra 更新映射的附加信息(昵称/头像/角色等), 内容未变化时不写库
func (m *Map) SetExtra(kind Kind, openid string, extra map[string]any) {
	if openid == "" || extra == nil {
		return
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return
	}
	key := openidKey{kind, openid}
	s := string(raw)

	m.mu.Lock()
	if m.extra[key] == s {
		m.mu.Unlock()
		return
	}
	m.extra[key] = s
	m.mu.Unlock()

	rec, err := m.app.FindFirstRecordByFilter(
		db.TableIDMap,
		"bot = {:bot} && kind = {:kind} && openid = {:openid}",
		dbx.Params{"bot": m.botID, "kind": string(kind), "openid": openid},
	)
	if err != nil {
		m.forgetExtra(key)
		return
	}
	rec.Set("extra", extra)
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

func (m *Map) remember(kind Kind, openid string, num int64) {
	m.byName[openidKey{kind, openid}] = num
	m.byNum[numKey{kind, num}] = openid
}
