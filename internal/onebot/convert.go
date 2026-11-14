package onebot

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shynome/go-onebots/internal/idmap"
	"github.com/shynome/go-onebots/internal/qqbot"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// mentionRe 匹配 content 里的 @ 提及标记
var mentionRe = regexp.MustCompile(`<@!?[^>]+>`)

// StripMention 去掉 content 里的 @ 标记
func StripMention(content string) string {
	return strings.TrimSpace(mentionRe.ReplaceAllString(content, ""))
}

type wsSender struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
	Card     string `json:"card,omitempty"`
	Role     string `json:"role,omitempty"`
	Sex      string `json:"sex,omitempty"`
	Age      int    `json:"age,omitempty"`
}

// wsEvent OneBot v11 事件
type wsEvent struct {
	Time        int64     `json:"time"`
	SelfID      int64     `json:"self_id"`
	PostType    string    `json:"post_type"`
	MessageType string    `json:"message_type,omitempty"`
	SubType     string    `json:"sub_type,omitempty"`
	MessageID   int64     `json:"message_id,omitempty"`
	UserID      int64     `json:"user_id,omitempty"`
	GroupID     int64     `json:"group_id,omitempty"`
	RawMessage  string    `json:"raw_message,omitempty"`
	Font        int       `json:"font"`
	Sender      *wsSender `json:"sender,omitempty"`
	Message     []Segment `json:"message,omitempty"`
}

func eventTime(s string) int64 {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return time.Now().Unix()
}

func userNickname(num int64) string {
	return "用户" + itoa(num)
}

func (c *BotContext) onPayload(p qqbot.Payload) {
	switch p.Event {
	case qqbot.EventReady:
		var ready qqbot.ReadyData
		if err := json.Unmarshal(p.Data, &ready); err != nil {
			slog.Warn("解析 READY 失败", "bot", c.botID, "error", err)
			return
		}
		num, err := c.ids.Num(idmap.KindSelf, ready.User.ID)
		if err != nil {
			slog.Warn("记录机器人自身 id 失败", "bot", c.botID, "error", err)
			return
		}
		c.setSelf(num, ready.User.Username)
	case qqbot.EventC2CMessageCreate:
		c.handleC2C(p.Data)
	case qqbot.EventGroupATMessageCreate:
		c.handleGroup(p.Data)
	}
}

func (c *BotContext) handleC2C(data json.RawMessage) {
	var msg qqbot.C2CMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Warn("解析单聊消息失败", "bot", c.botID, "error", err)
		return
	}

	selfNum, _ := c.self()
	if selfNum == 0 {
		slog.Warn("机器人未就绪, 丢弃单聊消息", "bot", c.botID)
		return
	}

	openid := msg.Author.OpenID()
	userNum, err := c.ids.Num(idmap.KindUser, openid)
	if err != nil {
		slog.Warn("分配用户 id 失败", "bot", c.botID, "error", err)
		return
	}
	msgNum, err := c.ids.NumWithExtra(idmap.KindMessage, msg.ID, map[string]any{
		"scene": "private", "target": openid,
	})
	if err != nil {
		slog.Warn("分配消息 id 失败", "bot", c.botID, "error", err)
		return
	}

	c.reply.Set("private", openid, msg.ID)
	text := msg.Content

	c.broadcast(wsEvent{
		Time:        eventTime(msg.Timestamp),
		SelfID:      selfNum,
		PostType:    "message",
		MessageType: "private",
		SubType:     "friend",
		MessageID:   msgNum,
		UserID:      userNum,
		RawMessage:  text,
		Sender:      &wsSender{UserID: userNum, Nickname: userNickname(userNum), Sex: "unknown"},
		Message:     []Segment{textSegment(text)},
	})
}

func (c *BotContext) handleGroup(data json.RawMessage) {
	var msg qqbot.GroupATMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Warn("解析群消息失败", "bot", c.botID, "error", err)
		return
	}

	selfNum, _ := c.self()
	if selfNum == 0 {
		slog.Warn("机器人未就绪, 丢弃群消息", "bot", c.botID)
		return
	}

	openid := msg.Author.OpenID()
	userNum, err := c.ids.Num(idmap.KindUser, openid)
	if err != nil {
		slog.Warn("分配用户 id 失败", "bot", c.botID, "error", err)
		return
	}
	groupNum, err := c.ids.Num(idmap.KindGroup, msg.GroupOpenID)
	if err != nil {
		slog.Warn("分配群 id 失败", "bot", c.botID, "error", err)
		return
	}
	msgNum, err := c.ids.NumWithExtra(idmap.KindMessage, msg.ID, map[string]any{
		"scene": "group", "target": msg.GroupOpenID,
	})
	if err != nil {
		slog.Warn("分配消息 id 失败", "bot", c.botID, "error", err)
		return
	}

	c.reply.Set("group", msg.GroupOpenID, msg.ID)
	text := StripMention(msg.Content)

	// 群消息必须带 at 段, ZeroBot 靠它判断 IsToMe
	segments := []Segment{atSegment(selfNum)}
	if text != "" {
		segments = append(segments, textSegment(text))
	}

	c.broadcast(wsEvent{
		Time:        eventTime(msg.Timestamp),
		SelfID:      selfNum,
		PostType:    "message",
		MessageType: "group",
		SubType:     "normal",
		MessageID:   msgNum,
		UserID:      userNum,
		GroupID:     groupNum,
		RawMessage:  "[CQ:at,qq=" + itoa(selfNum) + "] " + text,
		Font:        0,
		Sender:      &wsSender{UserID: userNum, Nickname: userNickname(userNum), Role: "member", Sex: "unknown"},
		Message:     segments,
	})
}
