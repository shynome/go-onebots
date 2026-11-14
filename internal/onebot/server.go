package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/eventlog"
	"github.com/shynome/go-onebots/internal/idmap"
	"github.com/shynome/go-onebots/internal/outbox"
	"github.com/shynome/go-onebots/internal/qqbot"
)

// Version 由 main 注入
var Version = "dev"

const (
	pingInterval   = 30 * time.Second
	writeTimeout   = 10 * time.Second
	handshakeExtra = "go-onebots"
	// pendingRetention 离线补发窗口, 超过该时长的消息被动回复已失效, 补发无意义
	pendingRetention = 30 * time.Minute
	// maxPending 离线队列上限, 超出丢弃最旧的
	maxPending = 2000
	// dedupWindow 消息去重窗口, 覆盖 resume 重放与群 @/全量事件重叠
	dedupWindow = 10 * time.Minute
	// dedupMax 去重表最多保存的消息数
	dedupMax = 2000
	// maxConcurrentActions 单个 OneBot 连接上同时处理的 action 上限
	maxConcurrentActions = 16
)

type Manager struct {
	app  core.App
	mu   sync.Mutex
	ctxs map[string]*BotContext
}

func NewManager(app core.App) *Manager {
	return &Manager{app: app, ctxs: map[string]*BotContext{}}
}

// Get 获取(或创建)机器人的 OneBot 上下文, 并注册事件订阅
func (m *Manager) Get(botID string) *BotContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.ctxs[botID]; ok {
		return c
	}
	c := &BotContext{
		mgr:       m,
		botID:     botID,
		ids:       idmap.New(m.app, botID),
		reply:     newReplyCtx(),
		log:       eventlog.New(m.app, botID),
		clients:   map[*wsClient]struct{}{},
		outbox:    outbox.New(m.app),
		deliverCh: make(chan string, 1024),
		logger:    m.app.Logger(),
	}
	c.restore()
	go c.deliverWorker()
	m.ctxs[botID] = c
	bot.Subscribe(botID, c.onPayload)
	return c
}

type pendingEvent struct {
	id   string
	data []byte
	at   time.Time
}

type BotContext struct {
	mgr   *Manager
	botID string
	ids   *idmap.Map
	reply *replyCtx
	log   *eventlog.Log

	seq int64 // 事件序号, 原子递增

	selfMu     sync.RWMutex
	selfNum    int64
	selfName   string
	selfOpenID string

	seenMu sync.Mutex
	seen   map[string]time.Time

	clientsMu sync.Mutex
	clients   map[*wsClient]struct{}
	pending   []pendingEvent

	// outbox 出站消息队列, 先落库再发送
	outbox *outbox.Store

	deliverCh chan string

	// logger 用 PocketBase 的日志, 会写入 _logs 表方便后台排查
	logger *slog.Logger
}

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *wsClient) writeJSON(ctx context.Context, v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, c.conn, v)
}

func (c *wsClient) writeRaw(ctx context.Context, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.conn.Write(wctx, websocket.MessageText, data)
}

func (c *BotContext) setSelf(num int64, name, openid string) {
	c.selfMu.Lock()
	defer c.selfMu.Unlock()
	c.selfNum = num
	c.selfName = name
	c.selfOpenID = openid
}

// selfOpenIDStr 返回机器人自身的 openid, 用于判断 mentions 里是否 @ 了机器人
func (c *BotContext) selfOpenIDStr() string {
	c.selfMu.RLock()
	id := c.selfOpenID
	c.selfMu.RUnlock()
	return id
}

// firstSeen 按消息 id 去重: resume 重放、@ 事件与全量事件并存时避免重复处理
func (c *BotContext) firstSeen(id string) bool {
	if id == "" {
		return true
	}
	now := time.Now()
	c.seenMu.Lock()
	defer c.seenMu.Unlock()

	if c.seen == nil {
		c.seen = map[string]time.Time{}
	}
	if t, ok := c.seen[id]; ok && now.Sub(t) < dedupWindow {
		return false
	}
	if len(c.seen) >= dedupMax {
		for k, t := range c.seen {
			if now.Sub(t) >= dedupWindow {
				delete(c.seen, k)
			}
		}
	}
	c.seen[id] = now
	return true
}

// self 返回机器人自身的 OneBot id 与昵称, 优先用 READY 缓存
func (c *BotContext) self() (int64, string) {
	c.selfMu.RLock()
	num, name := c.selfNum, c.selfName
	c.selfMu.RUnlock()
	if num != 0 {
		return num, name
	}

	b, ok := bot.Get(c.botID)
	if !ok {
		return 0, ""
	}
	u, ready := b.Self()
	if !ready {
		return 0, ""
	}
	n, err := c.ids.Num(idmap.KindSelf, u.ID)
	if err != nil {
		c.logger.Warn("分配机器人自身 id 失败", "bot", c.botID, "error", err)
		return 0, ""
	}
	c.setSelf(n, u.Username, u.ID)
	return n, u.Username
}

func (c *BotContext) addClientAndFlush(cl *wsClient) {
	c.clientsMu.Lock()
	c.clients[cl] = struct{}{}
	pending := c.pending
	c.pending = nil
	defer c.clientsMu.Unlock()

	if len(pending) == 0 {
		return
	}

	ids := make([]string, 0, len(pending))
	for i, p := range pending {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := cl.writeRaw(ctx, p.data)
		cancel()
		if err != nil {
			c.logger.Warn("补发事件失败, 剩余事件保留待下次补发", "bot", c.botID, "error", err)
			c.pending = append(pending[i:], c.pending...)
			return
		}
		ids = append(ids, p.id)
	}

	if err := c.log.MarkDelivered(ids); err != nil {
		c.logger.Warn("标记事件已投递失败", "bot", c.botID, "error", err)
	}
	c.logger.Info("已补发离线期间的事件", "bot", c.botID, "count", len(ids))
}

func (c *BotContext) delClient(cl *wsClient) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	delete(c.clients, cl)
}

// pushPendingLocked 调用方必须持有 clientsMu
func (c *BotContext) pushPendingLocked(id string, data []byte) {
	now := time.Now()
	cut := now.Add(-pendingRetention)
	drop := 0
	for drop < len(c.pending) && c.pending[drop].at.Before(cut) {
		drop++
	}
	if drop > 0 {
		c.pending = c.pending[drop:]
	}

	c.pending = append(c.pending, pendingEvent{id: id, data: data, at: now})
	if len(c.pending) > maxPending {
		over := len(c.pending) - maxPending
		c.pending = c.pending[over:]
		c.logger.Warn("离线事件超出上限, 丢弃最旧的", "bot", c.botID, "dropped", over, "limit", maxPending)
	}
}

func (c *BotContext) broadcast(ev wsEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		c.logger.Warn("序列化事件失败", "bot", c.botID, "error", err)
		return
	}

	seq := atomic.AddInt64(&c.seq, 1)
	id, err := c.log.Append(eventlog.Record{
		Seq:         seq,
		MessageType: ev.MessageType,
		UserID:      ev.UserID,
		GroupID:     ev.GroupID,
		MessageID:   ev.MessageID,
		Content:     ev.RawMessage,
		Data:        data,
	})
	if err != nil {
		c.logger.Warn("写入事件日志失败", "bot", c.botID, "error", err)
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	delivered := 0
	for cl := range c.clients {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := cl.writeRaw(ctx, data)
		cancel()
		if err != nil {
			c.logger.Debug("推送事件失败", "bot", c.botID, "error", err)
			delete(c.clients, cl)
			cl.conn.CloseNow()
			continue
		}
		delivered++
	}

	if delivered == 0 {
		c.pushPendingLocked(id, data)
		c.logger.Warn("没有 OneBot 客户端连接, 事件已缓存待补发",
			"bot", c.botID, "message_type", ev.MessageType,
			"user_id", ev.UserID, "group_id", ev.GroupID, "pending", len(c.pending))
		return
	}
	c.markDelivered(id)
}

func (c *BotContext) markDelivered(id string) {
	if id == "" {
		return
	}
	select {
	case c.deliverCh <- id:
	default:
		go func() { _ = c.log.MarkDelivered([]string{id}) }()
	}
}

func (c *BotContext) deliverWorker() {
	batch := make([]string, 0, 64)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := c.log.MarkDelivered(batch); err != nil {
			c.logger.Warn("标记事件已投递失败", "bot", c.botID, "error", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case id := <-c.deliverCh:
			batch = append(batch, id)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// restore 从 PocketBase 恢复序号与未投递事件
func (c *BotContext) restore() {
	c.seq = c.log.LastSeq()
	since := time.Now().Add(-pendingRetention)
	entries, err := c.log.Undelivered(since, maxPending)
	if err != nil {
		c.logger.Warn("恢复未投递事件失败", "bot", c.botID, "error", err)
		return
	}
	now := time.Now()
	for _, e := range entries {
		c.pending = append(c.pending, pendingEvent{id: e.ID, data: e.Data, at: now})
	}
	if len(entries) > 0 {
		c.logger.Info("恢复未投递事件", "bot", c.botID, "count", len(entries), "last_seq", c.seq)
	}
}

// ServeWS OneBot v11 正向 WebSocket 入口
func (m *Manager) ServeWS(e *core.RequestEvent) error {
	botID := e.Request.PathValue("botId")
	if _, ok := m.checkAuth(e, botID); !ok {
		return nil
	}

	c := m.Get(botID)
	selfNum, selfName := c.self()
	if selfNum == 0 {
		m.app.Logger().Warn("onebot 连接被拒绝", "bot", botID, "reason", "机器人尚未就绪", "remote", e.Request.RemoteAddr)
		writeJSONError(e, http.StatusServiceUnavailable, "机器人尚未就绪, 等待 QQ 网关连接成功后重试")
		return nil
	}

	conn, err := websocket.Accept(e.Response, e.Request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		m.app.Logger().Warn("onebot ws 握手失败", "bot", botID, "error", err)
		return nil
	}
	conn.SetReadLimit(4 << 20)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cl := &wsClient{conn: conn}

	// ZeroBot 连接成功后读取的第一帧必须包含 self_id
	if err := cl.writeJSON(ctx, map[string]any{
		"self_id": selfNum,
		"status":  "ok",
		"retcode": 0,
		"data": map[string]any{
			"user_id":  selfNum,
			"nickname": selfName,
		},
		"app": handshakeExtra,
	}); err != nil {
		m.app.Logger().Warn("发送握手包失败", "bot", botID, "error", err)
		return nil
	}

	c.addClientAndFlush(cl)
	defer c.delClient(cl)

	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					conn.CloseNow()
					return
				}
			}
		}
	}()

	m.app.Logger().Info("onebot 客户端已连接", "bot", botID, "self_id", selfNum)

	// action 并发处理: 客户端(ZeroBot)本身就是并发发请求的, 靠 echo 匹配响应。
	// 串行处理会让慢 action(如内网媒体转存)阻塞同一连接上的其它请求。
	sem := make(chan struct{}, maxConcurrentActions)
	for {
		var req actionRequest
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			m.app.Logger().Info("onebot 客户端断开", "bot", botID, "error", err)
			return nil
		}

		select {
		case sem <- struct{}{}:
		default:
			// 并发已满, 直接排队等一个槽位, 不丢请求
			sem <- struct{}{}
		}
		go func(req actionRequest) {
			defer func() { <-sem }()
			resp := c.handleAction(ctx, req.Action, req.Params)
			resp.Echo = req.Echo
			if err := cl.writeJSON(ctx, resp); err != nil {
				m.app.Logger().Debug("写回 action 结果失败", "bot", botID, "action", req.Action, "error", err)
			}
		}(req)
	}
}

func writeJSONError(e *core.RequestEvent, status int, msg string) {
	e.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	e.Response.WriteHeader(status)
	_ = json.NewEncoder(e.Response).Encode(map[string]any{
		"status":  "failed",
		"retcode": status,
		"message": msg,
	})
}

type actionRequest struct {
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
	Echo   json.RawMessage `json:"echo"`
}

// checkAuth 校验机器人存在且 token 匹配
// token 可放 Authorization: Bearer 或 access_token 查询参数
// 返回 false 表示已写好错误响应, 调用方直接返回
func (m *Manager) checkAuth(e *core.RequestEvent, botID string) (*core.Record, bool) {
	if botID == "" {
		writeJSONError(e, http.StatusBadRequest, "缺少机器人 id")
		return nil, false
	}

	rec, err := m.app.FindRecordById(db.TableQQBots, botID)
	if err != nil {
		m.app.Logger().Warn("onebot 请求被拒绝", "bot", botID, "reason", "机器人不存在", "remote", e.Request.RemoteAddr)
		writeJSONError(e, http.StatusNotFound, "机器人不存在")
		return nil, false
	}

	token := rec.GetString("onebot_token")
	if token == "" {
		return rec, true
	}

	if requestToken(e.Request) != token {
		m.app.Logger().Warn("onebot 请求被拒绝", "bot", botID, "reason", "token 无效", "remote", e.Request.RemoteAddr)
		e.Response.Header().Set("WWW-Authenticate", `Bearer realm="onebot"`)
		writeJSONError(e, http.StatusUnauthorized, "token 无效")
		return nil, false
	}
	return rec, true
}

type actionResponse struct {
	Status  string          `json:"status"`
	RetCode int             `json:"retcode"`
	Data    any             `json:"data"`
	Message string          `json:"message,omitempty"`
	Wording string          `json:"wording,omitempty"`
	Echo    json.RawMessage `json:"echo,omitempty"`
}

func okResponse(data any) actionResponse {
	return actionResponse{Status: "ok", RetCode: 0, Data: data}
}

func failResponse(retcode int, msg string) actionResponse {
	return actionResponse{Status: "failed", RetCode: retcode, Message: msg, Wording: msg}
}

type sendParams struct {
	UserID      flexInt         `json:"user_id"`
	GroupID     flexInt         `json:"group_id"`
	Message     json.RawMessage `json:"message"`
	MessageType string          `json:"message_type"`
}

func (c *BotContext) handleAction(ctx context.Context, action string, raw json.RawMessage) actionResponse {
	b, running := bot.Get(c.botID)
	if !running {
		return failResponse(1400, "机器人未运行")
	}

	switch action {
	case "get_login_info":
		num, name := c.self()
		if num == 0 {
			return failResponse(1400, "机器人尚未就绪")
		}
		return okResponse(map[string]any{"user_id": num, "nickname": name})
	case "get_status":
		return okResponse(map[string]any{"online": true, "good": true})
	case "get_version_info":
		return okResponse(map[string]any{
			"app_name":         "go-onebots",
			"app_version":      Version,
			"protocol_version": "v11",
		})
	case "send_group_msg", "send_private_msg", "send_msg":
		var p sendParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return failResponse(1400, "参数解析失败: "+err.Error())
			}
		}
		return c.sendMessage(ctx, b, action, p)
	case "get_stranger_info", "get_group_member_info", "get_group_info", "delete_msg":
		var p lookupParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return failResponse(1400, "参数解析失败: "+err.Error())
			}
		}
		switch action {
		case "get_stranger_info":
			return c.getStrangerInfo(p)
		case "get_group_member_info":
			return c.getGroupMemberInfo(p)
		case "get_group_info":
			return c.getGroupInfo(p)
		default:
			return c.deleteMsg(ctx, b, p)
		}
	case "get_group_list":
		return c.getGroupList()
	case "get_msg", "get_group_msg_history":
		var p msgParams
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &p); err != nil {
				return failResponse(1400, "参数解析失败: "+err.Error())
			}
		}
		if action == "get_msg" {
			return c.getMsg(p)
		}
		return c.getGroupMsgHistory(p)
	case "get_friend_list", "get_group_member_list":
		// 官方没有对应接口, 返回空数组避免插件报错
		return okResponse([]any{})
	default:
		return failResponse(retcodeUnsupported, "该 action 在 QQ 官方 API 下不支持: "+action)
	}
}

func (c *BotContext) sendMessage(ctx context.Context, b *bot.Bot, action string, p sendParams) actionResponse {
	segs, err := ParseSegments(p.Message)
	if err != nil {
		return failResponse(1400, "message 解析失败: "+err.Error())
	}

	scene := p.MessageType
	switch action {
	case "send_group_msg":
		scene = "group"
	case "send_private_msg":
		scene = "private"
	case "send_msg":
		if scene != "group" && scene != "private" {
			if p.GroupID != 0 {
				scene = "group"
			} else {
				scene = "private"
			}
		}
	}

	out, skipped := c.buildOutbounds(segs, scene)
	c.logSkipped(skipped)
	if len(out) == 0 {
		return failResponse(1400, "消息内容为空或只包含不支持的段")
	}

	if scene == "group" {
		return c.sendGroup(ctx, b, p.GroupID.Int64(), segs, out)
	}
	return c.sendPrivate(ctx, b, p.UserID.Int64(), out)
}

func (c *BotContext) sendGroup(ctx context.Context, b *bot.Bot, groupID int64, segs []Segment, out []outbound) actionResponse {
	openid, err := c.ids.OpenID(idmap.KindGroup, groupID)
	if err != nil {
		// 不是映射 id 时, 尝试当作真实群号去 idmap 里查
		openid, err = c.openIDByNumber(idmap.KindGroup, groupID, "group_id")
		if err != nil {
			return failResponse(1400, err.Error())
		}
	}

	hasAtAll := hasAtAllSegs(segs)

	res, err := c.sendOutbounds(ctx, b, "group", openid, out)
	if err == nil {
		return okResponse(c.messageData("group", openid, res.ID))
	}

	// QQ 侧拒绝 @全体(无管理员权限 / 当日已达上限) 时, 去掉 at 重发一遍
	if hasAtAll && atAllRejected(err) {
		retryOut, retrySkipped := c.buildOutbounds(withoutAtAll(segs), "group")
		c.logSkipped(retrySkipped)
		if len(retryOut) > 0 {
			if res, rerr := c.sendOutbounds(ctx, b, "group", openid, retryOut); rerr == nil {
				c.logger.Warn("去掉 @全体 后发送成功", "bot", c.botID, "group", openid, "error", err)
				return okResponse(c.messageData("group", openid, res.ID))
			}
		}
	}

	c.logger.Warn("发送群消息失败", "bot", c.botID, "group", openid, "error", err)
	return failResponse(1200, "发送失败: "+err.Error())
}

// hasAtAllSegs 判断段里是否含 @全体
func hasAtAllSegs(segs []Segment) bool {
	for _, s := range segs {
		if s.Type == "at" && s.Data["qq"] == "all" {
			return true
		}
	}
	return false
}

// withoutAtAll 去掉 @全体 段, 保留其余段
func withoutAtAll(segs []Segment) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		if s.Type == "at" && s.Data["qq"] == "all" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// atAllRejected 判断失败是否因为 @全体 不被允许(无管理员权限 / 当日已达上限)
func atAllRejected(err error) bool {
	var apiErr *qqbot.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	body := strings.ToLower(apiErr.Body)
	return strings.Contains(body, "40034142") ||
		strings.Contains(body, "everyone") ||
		strings.Contains(body, "permission") ||
		strings.Contains(body, "权限")
}

func (c *BotContext) sendPrivate(ctx context.Context, b *bot.Bot, userID int64, out []outbound) actionResponse {
	openid, err := c.ids.OpenID(idmap.KindUser, userID)
	if err != nil {
		// 不是映射 id 时, 尝试当作真实 QQ 号去 idmap 里查
		openid, err = c.openIDByNumber(idmap.KindUser, userID, "user_id")
		if err != nil {
			return failResponse(1400, err.Error())
		}
	}
	res, err := c.sendOutbounds(ctx, b, "private", openid, out)
	if err != nil {
		c.logger.Warn("发送单聊消息失败", "bot", c.botID, "user", openid, "error", err)
		return failResponse(1200, "发送失败: "+err.Error())
	}
	return okResponse(c.messageData("private", openid, res.ID))
}

func (c *BotContext) messageData(scene, target, messageID string) map[string]any {
	num, err := c.ids.MessageNum(messageID, map[string]any{
		"scene": scene, "target": target,
	})
	if err != nil {
		c.logger.Warn("分配消息 id 失败", "bot", c.botID, "error", err)
		return map[string]any{"message_id": 0}
	}
	return map[string]any{"message_id": num}
}
