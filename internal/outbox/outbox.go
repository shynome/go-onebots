// Package outbox 负责出站消息的落库与状态流转
// 一条记录对应一条 QQ 消息(一次 OneBot 发送请求可能拆成多条)
// 发送前写 pending, 成功后转 sent, 永久失败转 failed
package outbox

import (
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/go-onebots/db"
)

const (
	StatePending = "pending"
	StateSent    = "sent"
	StateFailed  = "failed"
)

// timeLayout PocketBase 日期字段的比较格式
const timeLayout = "2006-01-02 15:04:05.000Z"

type Store struct {
	app core.App
}

func New(app core.App) *Store {
	return &Store{app: app}
}

type Item struct {
	BotID     string
	Scene     string
	Target    string
	Content   string
	Markdown  string
	Segments  []byte // 非空时按 markdown 发送: 文本与图片按原顺序拼接
	Keyboard  []byte // 非空时附带内嵌键盘(互动按钮)
	MediaKind string
	MediaFile string
	MediaURL  string
	// ReplyTo 引用回复目标的 msg_idx(REFIDX_...), 空表示不引用
	ReplyTo string
}

// Enqueue 写入一条待发送记录, 返回记录以便后续更新状态
// next_retry_at 设为当前时间, 崩溃重启后可以被立即捡起
func (s *Store) Enqueue(it Item) (*core.Record, error) {
	col, err := s.app.FindCachedCollectionByNameOrId(db.TableOutbox)
	if err != nil {
		return nil, err
	}

	r := core.NewRecord(col)
	r.Set("bot", it.BotID)
	r.Set("scene", it.Scene)
	r.Set("target", it.Target)
	r.Set("content", it.Content)
	r.Set("markdown", it.Markdown)
	if len(it.Segments) > 0 {
		r.Set("segments", types.JSONRaw(it.Segments))
	}
	if len(it.Keyboard) > 0 {
		r.Set("keyboard", types.JSONRaw(it.Keyboard))
	}
	r.Set("media_kind", it.MediaKind)
	r.Set("media_file", it.MediaFile)
	r.Set("media_url", it.MediaURL)
	r.Set("reply_to", it.ReplyTo)
	r.Set("state", StatePending)
	r.Set("attempts", 0)
	r.Set("next_retry_at", types.NowDateTime())
	if err := s.app.Save(r); err != nil {
		return nil, err
	}
	return r, nil
}

// Due 取出已到重试时间的待发送记录
func (s *Store) Due(now time.Time, limit int) ([]*core.Record, error) {
	return s.app.FindRecordsByFilter(db.TableOutbox,
		"state = {:state} && next_retry_at <= {:now}",
		"next_retry_at", limit, 0,
		dbx.Params{
			"state": StatePending,
			"now":   now.UTC().Format(timeLayout),
		},
	)
}

// MarkSent 标记发送成功并回填 QQ 消息 id
func (s *Store) MarkSent(r *core.Record, messageID string) error {
	r.Set("state", StateSent)
	r.Set("message_id", messageID)
	r.Set("last_error", "")
	return s.app.Save(r)
}

// MarkFailed 标记永久失败, 不再重试
func (s *Store) MarkFailed(r *core.Record, reason string) error {
	r.Set("state", StateFailed)
	r.Set("last_error", truncate(reason, 1000))
	return s.app.Save(r)
}

// RetryLater 记录一次失败并把下次重试时间往后推
func (s *Store) RetryLater(r *core.Record, next time.Time, reason string) error {
	r.Set("attempts", r.GetInt("attempts")+1)
	r.Set("next_retry_at", next.UTC().Format(timeLayout))
	r.Set("last_error", truncate(reason, 1000))
	return s.app.Save(r)
}

// SetMediaURL 缓存首次解析出的媒体 URL, 重试时直接复用
func (s *Store) SetMediaURL(r *core.Record, url string) error {
	r.Set("media_url", url)
	return s.app.Save(r)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
