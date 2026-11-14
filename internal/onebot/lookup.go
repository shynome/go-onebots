package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/eventlog"
	"github.com/shynome/go-onebots/internal/idmap"
)

// numberBinds 返回 openid -> 真实号码(群号 / QQ 号) 的对应关系
// 号码直接保存在 onebot_id_map 的记录上
func (c *BotContext) numberBinds(kind idmap.Kind) map[string]int64 {
	m, err := c.ids.Numbers(kind)
	if err != nil {
		return nil
	}
	return m
}

// openIDByNumber 用真实号码查 openid
// QQ 官方 API 只给 openid, 号码只能靠管理员在 onebot_id_map 里填
func (c *BotContext) openIDByNumber(kind idmap.Kind, number int64, label string) (string, error) {
	openid, err := c.ids.OpenIDByNumber(kind, number)
	if err != nil {
		return "", errors.New("未知的 " + label + ": " + itoa(number) +
			", 既不是已有的映射 id, 也没有在 onebot_id_map 里给对应记录填 qq_number")
	}
	return openid, nil
}

type lookupParams struct {
	UserID    int64 `json:"user_id"`
	GroupID   int64 `json:"group_id"`
	MessageID int64 `json:"message_id"`
}

// maxHistoryCount get_group_msg_history 单次返回上限
const maxHistoryCount = 100

// msgParams get_msg / get_group_msg_history 的入参
type msgParams struct {
	MessageID  int64 `json:"message_id"`
	GroupID    int64 `json:"group_id"`
	MessageSeq int64 `json:"message_seq"`
	Count      int   `json:"count"`
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// userProfile 从 idmap.extra 读取最后见到的用户资料
// QQ 官方没有成员信息接口, 只能返回消息里带过的快照
func (c *BotContext) userProfile(kind idmap.Kind, num int64) (nickname, card, role string) {
	_, extra, err := c.ids.NumExtra(kind, num)
	if err != nil {
		return "用户" + itoa(num), "", "member"
	}

	nickname = asString(extra["nickname"])
	if nickname == "" {
		nickname = "用户" + itoa(num)
	}
	card = asString(extra["card"])
	role = asString(extra["role"])
	if role == "" {
		role = "member"
	}
	return nickname, card, role
}

// messageView 把记录的事件转成 OneBot get_msg 风格的对象
func messageView(ev wsEvent) map[string]any {
	view := map[string]any{
		"message_id":   ev.MessageID,
		"real_id":      ev.MessageID,
		"time":         ev.Time,
		"message_type": ev.MessageType,
		"sender":       ev.Sender,
		"message":      ev.Message,
		"raw_message":  ev.RawMessage,
	}
	if ev.GroupID != 0 {
		view["group_id"] = ev.GroupID
	}
	if ev.UserID != 0 {
		view["user_id"] = ev.UserID
	}
	return view
}

func decodeMessage(data []byte) (wsEvent, bool) {
	var ev wsEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, false
	}
	return ev, true
}

// getMsg 从事件日志里按 message_id 找回原消息
// 只能查到保留期内的消息, 超过 logs.maxDays 会被清理
func (c *BotContext) getMsg(p msgParams) actionResponse {
	if p.MessageID == 0 {
		return failResponse(1400, "message_id 不能为空")
	}
	if _, _, err := c.ids.MessageExtra(p.MessageID); err != nil {
		return failResponse(1400, "未知的 message_id: "+itoa(p.MessageID))
	}

	data, err := c.log.MessageByID(p.MessageID)
	if err != nil {
		if errors.Is(err, eventlog.ErrNotFound) {
			return failResponse(1400, "未找到该消息, 可能已超过保留期")
		}
		return failResponse(1200, "查询消息失败: "+err.Error())
	}

	ev, ok := decodeMessage(data)
	if !ok {
		return failResponse(1200, "解析消息失败")
	}
	return okResponse(messageView(ev))
}

// getGroupMsgHistory 返回群消息历史, 按时间升序
func (c *BotContext) getGroupMsgHistory(p msgParams) actionResponse {
	if p.GroupID == 0 {
		return failResponse(1400, "group_id 不能为空")
	}

	count := p.Count
	if count <= 0 {
		count = 20
	}
	if count > maxHistoryCount {
		count = maxHistoryCount
	}

	data, err := c.log.GroupHistory(p.GroupID, p.MessageSeq, count)
	if err != nil {
		return failResponse(1200, "查询群历史失败: "+err.Error())
	}

	msgs := make([]map[string]any, 0, len(data))
	for _, d := range data {
		if ev, ok := decodeMessage(d); ok {
			msgs = append(msgs, messageView(ev))
		}
	}
	return okResponse(map[string]any{"messages": msgs})
}

func (c *BotContext) getStrangerInfo(p lookupParams) actionResponse {
	nickname, _, _ := c.userProfile(idmap.KindUser, p.UserID)
	return okResponse(map[string]any{
		"user_id":  p.UserID,
		"nickname": nickname,
		"sex":      "unknown",
		"age":      0,
	})
}

func (c *BotContext) getGroupMemberInfo(p lookupParams) actionResponse {
	nickname, card, role := c.userProfile(idmap.KindUser, p.UserID)
	return okResponse(map[string]any{
		"group_id":       p.GroupID,
		"user_id":        p.UserID,
		"nickname":       nickname,
		"card":           card,
		"role":           role,
		"sex":            "unknown",
		"age":            0,
		"join_time":      0,
		"last_sent_time": 0,
	})
}

func (c *BotContext) getGroupInfo(p lookupParams) actionResponse {
	if _, _, err := c.ids.NumExtra(idmap.KindGroup, p.GroupID); err != nil {
		// 也允许传填写过的真实群号
		if _, berr := c.openIDByNumber(idmap.KindGroup, p.GroupID, "group_id"); berr != nil {
			return failResponse(1400, "未知的 group_id: "+itoa(p.GroupID)+", 需要先收到该群的消息")
		}
	}
	// 官方没有群信息接口, 群名无法获取
	return okResponse(map[string]any{
		"group_id":         p.GroupID,
		"group_name":       "",
		"member_count":     0,
		"max_member_count": 0,
	})
}

func (c *BotContext) getGroupList() actionResponse {
	nums, err := c.ids.ListNums(idmap.KindGroup)
	if err != nil {
		return failResponse(1200, "读取群列表失败: "+err.Error())
	}

	binds := c.numberBinds(idmap.KindGroup)
	list := make([]map[string]any, 0, len(nums))
	for _, n := range nums {
		gid := n
		if openid, _, err := c.ids.NumExtra(idmap.KindGroup, n); err == nil {
			// 已绑定真实群号的群对外统一用群号
			if qq, ok := binds[openid]; ok {
				gid = qq
			}
		}
		list = append(list, map[string]any{"group_id": gid, "group_name": ""})
	}
	return okResponse(list)
}

func (c *BotContext) deleteMsg(ctx context.Context, b *bot.Bot, p lookupParams) actionResponse {
	messageOpenID, extra, err := c.ids.MessageExtra(p.MessageID)
	if err != nil {
		return failResponse(1400, "未知的 message_id: "+itoa(p.MessageID))
	}

	scene := asString(extra["scene"])
	target := asString(extra["target"])

	var derr error
	switch scene {
	case "group":
		derr = b.Client().DeleteGroupMessage(ctx, target, messageOpenID)
	case "private":
		derr = b.Client().DeleteUserMessage(ctx, target, messageOpenID)
	default:
		return failResponse(1400, "消息缺少场景信息, 无法撤回: "+itoa(p.MessageID))
	}

	if derr != nil {
		slog.Warn("撤回消息失败", "bot", c.botID, "message_id", p.MessageID, "error", derr)
		return failResponse(1200, "撤回失败: "+derr.Error())
	}
	return okResponse(nil)
}
