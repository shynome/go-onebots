// Package eventlog 把入站 OneBot 事件写进 PocketBase, 供离线补发与后台排查
package eventlog

import (
	"errors"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/go-onebots/db"
)

// ErrNotFound 表示事件已被清理或从未记录
var ErrNotFound = errors.New("事件不存在")

type Log struct {
	app   core.App
	botID string
}

func New(app core.App, botID string) *Log {
	return &Log{app: app, botID: botID}
}

type Record struct {
	Seq         int64
	MessageType string
	UserID      int64
	GroupID     int64
	MessageID   int64
	Content     string
	Data        []byte
	// Raw QQ 下发的原始消息 JSON, 未经 OneBot 转换, 供排查与引用回复使用
	Raw []byte
	// Delivered 为 true 时直接标记已投递, 不会再补发给客户端
	// 机器人自己发出的消息用这个, 只作为历史记录存在
	Delivered bool
}

type Entry struct {
	ID   string
	Data []byte
}

// LastSeq 返回已记录的最大序号, 用于重启后继续递增
func (l *Log) LastSeq() int64 {
	recs, err := l.app.FindRecordsByFilter(db.TableEvents, "bot = {:bot}", "-seq", 1, 0,
		dbx.Params{"bot": l.botID})
	if err != nil || len(recs) == 0 {
		return 0
	}
	return int64(recs[0].GetFloat("seq"))
}

// Append 追加一条事件记录
func (l *Log) Append(rec Record) (string, error) {
	col, err := l.app.FindCachedCollectionByNameOrId(db.TableEvents)
	if err != nil {
		return "", err
	}
	r := core.NewRecord(col)
	r.Set("bot", l.botID)
	r.Set("seq", rec.Seq)
	r.Set("message_type", rec.MessageType)
	r.Set("user_id", rec.UserID)
	r.Set("group_id", rec.GroupID)
	r.Set("message_id", rec.MessageID)
	r.Set("content", rec.Content)
	r.Set("event", types.JSONRaw(rec.Data))
	if len(rec.Raw) > 0 {
		r.Set("raw", types.JSONRaw(rec.Raw))
	}
	r.Set("delivered", rec.Delivered)
	if rec.Delivered {
		r.Set("delivered_at", types.NowDateTime())
	}
	if err := l.app.Save(r); err != nil {
		return "", err
	}
	return r.Id, nil
}

// Undelivered 取出 since 之后且尚未投递的事件, 按 seq 升序
func (l *Log) Undelivered(since time.Time, limit int) ([]Entry, error) {
	recs, err := l.app.FindRecordsByFilter(db.TableEvents,
		"bot = {:bot} && delivered = false && created >= {:since}",
		"seq", limit, 0,
		dbx.Params{
			"bot":   l.botID,
			"since": since.UTC().Format("2006-01-02 15:04:05.000Z"),
		},
	)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(recs))
	for _, rec := range recs {
		data := eventData(rec)
		if len(data) == 0 {
			continue
		}
		entries = append(entries, Entry{ID: rec.Id, Data: data})
	}
	return entries, nil
}

func eventData(rec *core.Record) []byte {
	switch v := rec.Get("event").(type) {
	case types.JSONRaw:
		return []byte(v)
	case string:
		return []byte(v)
	case []byte:
		return v
	}
	return nil
}

// MessageByID 按 OneBot message_id 取事件 JSON, 用于 get_msg
func (l *Log) MessageByID(messageID int64) ([]byte, error) {
	recs, err := l.app.FindRecordsByFilter(db.TableEvents,
		"bot = {:bot} && message_id = {:mid}",
		"-seq", 1, 0,
		dbx.Params{"bot": l.botID, "mid": messageID},
	)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, ErrNotFound
	}
	return eventData(recs[0]), nil
}

// GroupHistory 取群消息历史, before > 0 时只取 message_id 比它更旧的部分
// 返回按 seq 升序(从旧到新), 符合 get_group_msg_history 的约定
func (l *Log) GroupHistory(groupID int64, before int64, limit int) ([][]byte, error) {
	filter := "bot = {:bot} && message_type = 'group' && group_id = {:gid}"
	params := dbx.Params{"bot": l.botID, "gid": groupID}
	if before > 0 {
		filter += " && message_id < {:before}"
		params["before"] = before
	}

	recs, err := l.app.FindRecordsByFilter(db.TableEvents, filter, "-seq", limit, 0, params)
	if err != nil {
		return nil, err
	}

	msgs := make([][]byte, 0, len(recs))
	for i := len(recs) - 1; i >= 0; i-- {
		if data := eventData(recs[i]); len(data) > 0 {
			msgs = append(msgs, data)
		}
	}
	return msgs, nil
}

// MarkDelivered 批量标记已投递
func (l *Log) MarkDelivered(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return l.app.RunInTransaction(func(tx core.App) error {
		now := types.NowDateTime()
		for _, id := range ids {
			rec, err := tx.FindRecordById(db.TableEvents, id)
			if err != nil {
				continue
			}
			rec.Set("delivered", true)
			rec.Set("delivered_at", now)
			if err := tx.Save(rec); err != nil {
				return err
			}
		}
		return nil
	})
}
