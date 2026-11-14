package onebot

import (
	"context"
	"log/slog"

	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/idmap"
)

type lookupParams struct {
	UserID    int64 `json:"user_id"`
	GroupID   int64 `json:"group_id"`
	MessageID int64 `json:"message_id"`
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
		return failResponse(1400, "未知的 group_id: "+itoa(p.GroupID)+", 需要先收到该群的消息")
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

	list := make([]map[string]any, 0, len(nums))
	for _, n := range nums {
		list = append(list, map[string]any{"group_id": n, "group_name": ""})
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
