package onebot

import (
	"context"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/outbox"
)

const (
	// outboxCron 出站补发扫描间隔
	// PocketBase 的 cron 只认 5 段标准表达式与预定义宏, 不支持 @every, 所以用每分钟
	outboxCron = "* * * * *"
	// outboxBatch 单次最多处理条数
	outboxBatch = 50
	// outboxMaxAge 超过被动回复窗口就不再补发, 避免把陈旧消息发到群里
	outboxMaxAge = replyWindow
	// outboxMaxAttempts 累计尝试次数上限, 防止反复崩溃时无限重试
	outboxMaxAttempts = 3
)

// StartOutboxWorker 定时补发 pending 的出站消息
// 正常路径的失败已在 deliver 里处理完毕, 这里主要兜底进程崩溃留下的记录
func (m *Manager) StartOutboxWorker() {
	fn := func() {
		if err := m.retryOutbox(); err != nil {
			m.app.Logger().Error("补发出站消息失败", "error", err)
		}
	}
	if err := m.app.Cron().Add("onebot_outbox_retry", outboxCron, fn); err != nil {
		m.app.Logger().Error("注册出站补发任务失败", "error", err)
	}
}

func (m *Manager) retryOutbox() error {
	store := outbox.New(m.app)
	recs, err := store.Due(time.Now(), outboxBatch)
	if err != nil {
		return err
	}

	for _, rec := range recs {
		m.retryOutboxOne(store, rec)
	}
	return nil
}

func (m *Manager) retryOutboxOne(store *outbox.Store, rec *core.Record) {
	botID := rec.GetString("bot")

	if rec.GetInt("attempts") >= outboxMaxAttempts {
		store.MarkFailed(rec, "已超过最大重试次数")
		return
	}
	if rec.GetDateTime("created").Time().Add(outboxMaxAge).Before(time.Now()) {
		store.MarkFailed(rec, "已超过补发时限")
		return
	}

	b, running := bot.Get(botID)
	if !running {
		store.MarkFailed(rec, "机器人未运行")
		return
	}

	c := m.Get(botID)
	res, err := c.deliver(context.Background(), b, rec)
	if err != nil {
		m.app.Logger().Warn("补发出站消息失败", "bot", botID, "record", rec.Id, "error", err)
		return
	}
	m.app.Logger().Info("已补发积压的出站消息", "bot", botID, "record", rec.Id, "message_id", res.ID)
}
