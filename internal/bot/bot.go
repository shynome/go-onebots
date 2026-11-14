package bot

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/qqbot"
)

type Handler func(qqbot.Payload)

type Bot struct {
	App    core.App
	Record *core.Record

	client *qqbot.Client
	cancel context.CancelFunc
	done   chan struct{}

	// self 从 READY 里拿到的机器人自身信息
	selfMu sync.RWMutex
	self   qqbot.User
	ready  bool

	// loadMu 防止并发重复拉取自身信息
	loadMu sync.Mutex
}

var registry = struct {
	sync.RWMutex
	m map[string]*Bot
}{m: map[string]*Bot{}}

// 事件处理器按机器人 id 保存, 这样 restart 后订阅不会丢
var handlers = struct {
	sync.RWMutex
	m map[string][]Handler
}{m: map[string][]Handler{}}

// Subscribe 注册事件处理器, 机器人重启不影响
func Subscribe(botID string, h Handler) {
	handlers.Lock()
	defer handlers.Unlock()
	handlers.m[botID] = append(handlers.m[botID], h)
}

// Get 按机器人记录 id 获取运行实例
func Get(id string) (*Bot, bool) {
	registry.RLock()
	defer registry.RUnlock()
	b, ok := registry.m[id]
	return b, ok
}

// Start 启动机器人: 连接 QQ 网关并开始接收事件
func Start(app core.App, record *core.Record) *Bot {
	stop(record.Id, false)

	b := &Bot{
		App:    app,
		Record: record,
		client: qqbot.New(qqbot.Config{
			AppID:        record.GetString("app_id"),
			AppSecret:    record.GetString("app_secret"),
			SessionStore: newWSStateStore(app, record.Id),
		}),
		done: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	registry.Lock()
	registry.m[record.Id] = b
	registry.Unlock()

	go b.run(ctx)
	return b
}

// Stop 停止机器人
func Stop(id string) {
	stop(id, true)
}

func stop(id string, manual bool) {
	registry.Lock()
	b, ok := registry.m[id]
	delete(registry.m, id)
	registry.Unlock()
	if !ok {
		return
	}
	b.cancel()
	<-b.done
	if manual {
		b.setStatus("手动停止")
	}
}

// StopAll 停止所有机器人
func StopAll() {
	registry.Lock()
	ids := make([]string, 0, len(registry.m))
	for id := range registry.m {
		ids = append(ids, id)
	}
	registry.Unlock()
	for _, id := range ids {
		Stop(id)
	}
}

// Client 返回 QQ 官方 API 客户端
func (b *Bot) Client() *qqbot.Client { return b.client }

// Self 返回机器人自身信息
// resume 场景不会重发 READY, 此时用 REST /users/@me 补齐一次
func (b *Bot) Self() (qqbot.User, bool) {
	b.selfMu.RLock()
	u, ready := b.self, b.ready
	b.selfMu.RUnlock()
	if ready {
		return u, true
	}

	b.tryLoadSelf()

	b.selfMu.RLock()
	defer b.selfMu.RUnlock()
	return b.self, b.ready
}

// tryLoadSelf 通过 REST 获取机器人自身信息
func (b *Bot) tryLoadSelf() {
	b.loadMu.Lock()
	defer b.loadMu.Unlock()

	b.selfMu.RLock()
	ready := b.ready
	b.selfMu.RUnlock()
	if ready {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	u, err := b.client.Me(ctx)
	if err != nil {
		slog.Warn("获取机器人自身信息失败", "bot", b.Record.Id, "error", err)
		return
	}

	b.selfMu.Lock()
	b.self = u
	b.ready = true
	b.selfMu.Unlock()
	slog.Info("qq 机器人信息已补齐", "bot", b.Record.Id, "name", u.Username, "openid", u.ID)
}

// Subscribe 注册事件处理器
func (b *Bot) Subscribe(h Handler) {
	Subscribe(b.Record.Id, h)
}

func (b *Bot) dispatch(p qqbot.Payload) {
	handlers.RLock()
	subs := make([]Handler, len(handlers.m[b.Record.Id]))
	copy(subs, handlers.m[b.Record.Id])
	handlers.RUnlock()
	for _, h := range subs {
		h(p)
	}
}

func (b *Bot) run(ctx context.Context) {
	defer close(b.done)
	b.setStatus("启动中")
	defer func() {
		if ctx.Err() == nil {
			b.setStatus("出错了")
		}
	}()

	events := b.client.Events(ctx, qqbot.IntentGroupAndC2C)
	b.setStatus("运行中")
	for p := range events {
		b.handle(p)
	}
}

func (b *Bot) handle(p qqbot.Payload) {
	switch p.Event {
	case qqbot.EventReady:
		var ready qqbot.ReadyData
		if err := json.Unmarshal(p.Data, &ready); err == nil {
			b.selfMu.Lock()
			b.self = ready.User
			b.ready = true
			b.selfMu.Unlock()
		}
	case qqbot.EventResumed:
		// resume 成功不会重发 READY, 这里补一次自身信息
		slog.Info("qq 网关已恢复", "bot", b.Record.Id)
		b.tryLoadSelf()
	case qqbot.EventC2CMessageCreate, qqbot.EventGroupATMessageCreate, qqbot.EventGroupMessageCreate:
		slog.Info("收到 QQ 消息", "bot", b.Record.Id, "event", p.Event, "data", string(p.Data))
	default:
		slog.Debug("忽略 QQ 事件", "bot", b.Record.Id, "event", p.Event)
	}
	b.dispatch(p)
}

func (b *Bot) setStatus(status string) {
	err := b.App.RunInTransaction(func(tx core.App) error {
		r, err := tx.FindRecordById(db.TableQQBots, b.Record.Id)
		if err != nil {
			return err
		}
		r.Set("ws_status", status)
		return tx.Save(r)
	})
	if err != nil {
		slog.Warn("更新机器人状态失败", "bot", b.Record.Id, "status", status, "error", err)
	}
}
