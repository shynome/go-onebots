package onebot

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/idmap"
	"github.com/shynome/go-onebots/internal/qqbot"
)

// Version 由 main 注入
var Version = "dev"

const (
	pingInterval   = 30 * time.Second
	writeTimeout   = 10 * time.Second
	handshakeExtra = "go-onebots"
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
		mgr:     m,
		botID:   botID,
		ids:     idmap.New(m.app, botID),
		reply:   newReplyCtx(),
		clients: map[*wsClient]struct{}{},
	}
	m.ctxs[botID] = c
	bot.Subscribe(botID, c.onPayload)
	return c
}

type BotContext struct {
	mgr   *Manager
	botID string
	ids   *idmap.Map
	reply *replyCtx

	selfMu   sync.RWMutex
	selfNum  int64
	selfName string

	clientsMu sync.RWMutex
	clients   map[*wsClient]struct{}
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

func (c *BotContext) setSelf(num int64, name string) {
	c.selfMu.Lock()
	defer c.selfMu.Unlock()
	c.selfNum = num
	c.selfName = name
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
		slog.Warn("分配机器人自身 id 失败", "bot", c.botID, "error", err)
		return 0, ""
	}
	c.setSelf(n, u.Username)
	return n, u.Username
}

func (c *BotContext) addClient(cl *wsClient) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	c.clients[cl] = struct{}{}
}

func (c *BotContext) delClient(cl *wsClient) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	delete(c.clients, cl)
}

func (c *BotContext) broadcast(ev wsEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("序列化事件失败", "bot", c.botID, "error", err)
		return
	}

	c.clientsMu.RLock()
	clients := make([]*wsClient, 0, len(c.clients))
	for cl := range c.clients {
		clients = append(clients, cl)
	}
	c.clientsMu.RUnlock()

	for _, cl := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := cl.writeRaw(ctx, data)
		cancel()
		if err != nil {
			slog.Debug("推送事件失败", "bot", c.botID, "error", err)
			c.delClient(cl)
			cl.conn.CloseNow()
		}
	}
}

// ServeWS OneBot v11 正向 WebSocket 入口
func (m *Manager) ServeWS(e *core.RequestEvent) error {
	botID := e.Request.PathValue("botId")
	if botID == "" {
		writeJSONError(e, http.StatusBadRequest, "缺少机器人 id")
		return nil
	}

	rec, err := m.app.FindRecordById(db.TableQQBots, botID)
	if err != nil {
		writeJSONError(e, http.StatusNotFound, "机器人不存在")
		return nil
	}

	if token := rec.GetString("onebot_token"); token != "" {
		auth := strings.TrimPrefix(e.Request.Header.Get("Authorization"), "Bearer ")
		if auth != token {
			e.Response.Header().Set("WWW-Authenticate", `Bearer realm="onebot"`)
			writeJSONError(e, http.StatusUnauthorized, "token 无效")
			return nil
		}
	}

	c := m.Get(botID)
	selfNum, selfName := c.self()
	if selfNum == 0 {
		writeJSONError(e, http.StatusServiceUnavailable, "机器人尚未就绪, 等待 QQ 网关连接成功后重试")
		return nil
	}

	conn, err := websocket.Accept(e.Response, e.Request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Warn("onebot ws 握手失败", "bot", botID, "error", err)
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
		slog.Warn("发送握手包失败", "bot", botID, "error", err)
		return nil
	}

	c.addClient(cl)
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

	slog.Info("onebot 客户端已连接", "bot", botID, "self_id", selfNum)

	for {
		var req actionRequest
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			slog.Info("onebot 客户端断开", "bot", botID, "error", err)
			return nil
		}
		resp := c.handleAction(ctx, req.Action, req.Params)
		resp.Echo = req.Echo
		if err := cl.writeJSON(ctx, resp); err != nil {
			return nil
		}
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
	UserID      int64           `json:"user_id"`
	GroupID     int64           `json:"group_id"`
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
	default:
		return failResponse(1404, "该 action 在 QQ 官方 API 下不支持: "+action)
	}
}

func (c *BotContext) sendMessage(ctx context.Context, b *bot.Bot, action string, p sendParams) actionResponse {
	text, skipped := ExtractText(p.Message)
	if len(skipped) > 0 {
		slog.Warn("跳过不支持的消息段", "bot", c.botID, "segments", skipped)
	}
	if strings.TrimSpace(text) == "" {
		return failResponse(1400, "消息内容为空或只包含不支持的段")
	}

	switch action {
	case "send_group_msg":
		return c.sendGroup(ctx, b, p.GroupID, text)
	case "send_private_msg":
		return c.sendPrivate(ctx, b, p.UserID, text)
	case "send_msg":
		if p.MessageType == "" || (p.MessageType != "group" && p.MessageType != "private") {
			if p.GroupID != 0 {
				p.MessageType = "group"
			} else {
				p.MessageType = "private"
			}
		}
		if p.MessageType == "group" {
			return c.sendGroup(ctx, b, p.GroupID, text)
		}
		return c.sendPrivate(ctx, b, p.UserID, text)
	}
	return failResponse(1404, "不支持的发送方式: "+action)
}

func (c *BotContext) sendGroup(ctx context.Context, b *bot.Bot, groupID int64, text string) actionResponse {
	openid, err := c.ids.OpenID(idmap.KindGroup, groupID)
	if err != nil {
		return failResponse(1400, "未知的 group_id: "+itoa(groupID)+", 需要先收到该群的消息才能发送")
	}
	msgID, seq := c.reply.Next("group", openid)
	if msgID == "" {
		slog.Warn("被动回复窗口已过期, 尝试发送主动消息", "bot", c.botID, "group", openid)
	}
	res, err := b.Client().SendGroupMessage(ctx, openid, qqbot.SendMessageRequest{
		Content: text,
		MsgType: qqbot.MsgTypeText,
		MsgID:   msgID,
		MsgSeq:  seq,
	})
	if err != nil {
		slog.Warn("发送群消息失败", "bot", c.botID, "group", openid, "error", err)
		return failResponse(1200, "发送失败: "+err.Error())
	}
	return okResponse(c.messageData("group", openid, res.ID))
}

func (c *BotContext) sendPrivate(ctx context.Context, b *bot.Bot, userID int64, text string) actionResponse {
	openid, err := c.ids.OpenID(idmap.KindUser, userID)
	if err != nil {
		return failResponse(1400, "未知的 user_id: "+itoa(userID)+", 需要先收到该用户的消息才能发送")
	}
	msgID, seq := c.reply.Next("private", openid)
	if msgID == "" {
		slog.Warn("被动回复窗口已过期, 尝试发送主动消息", "bot", c.botID, "user", openid)
	}
	res, err := b.Client().SendUserMessage(ctx, openid, qqbot.SendMessageRequest{
		Content: text,
		MsgType: qqbot.MsgTypeText,
		MsgID:   msgID,
		MsgSeq:  seq,
	})
	if err != nil {
		slog.Warn("发送单聊消息失败", "bot", c.botID, "user", openid, "error", err)
		return failResponse(1200, "发送失败: "+err.Error())
	}
	return okResponse(c.messageData("private", openid, res.ID))
}

func (c *BotContext) messageData(scene, target, messageID string) map[string]any {
	num, err := c.ids.NumWithExtra(idmap.KindMessage, messageID, map[string]any{
		"scene": scene, "target": target,
	})
	if err != nil {
		slog.Warn("分配消息 id 失败", "bot", c.botID, "error", err)
		return map[string]any{"message_id": 0}
	}
	return map[string]any{"message_id": num}
}
