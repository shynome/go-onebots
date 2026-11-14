package onebot

import (
	"encoding/json"
	"log/slog"
	"path"
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

func displayName(a qqbot.Author, num int64) string {
	if a.Username != "" {
		return a.Username
	}
	if a.MemberNickname != "" {
		return a.MemberNickname
	}
	return "用户" + itoa(num)
}

// buildMessageSegments 把文本与附件拼成 OneBot 消息段
// selfNum 非 0 时在首位插入 at 机器人段(群聊需要, ZeroBot 靠它判断 IsToMe)
func buildMessageSegments(content string, atts []qqbot.MessageAttachment, selfNum int64) []Segment {
	segs := make([]Segment, 0, len(atts)+2)
	if selfNum != 0 {
		segs = append(segs, atSegment(selfNum))
	}
	if content != "" {
		segs = append(segs, textSegment(content))
	}
	for _, a := range atts {
		seg, _ := attachmentSegment(a)
		segs = append(segs, seg)
	}
	return segs
}

func buildRawMessage(content string, atts []qqbot.MessageAttachment, selfNum int64) string {
	var sb strings.Builder
	if selfNum != 0 {
		sb.WriteString("[CQ:at,qq=" + itoa(selfNum) + "] ")
	}
	sb.WriteString(content)
	for _, a := range atts {
		_, cq := attachmentSegment(a)
		sb.WriteString(cq)
	}
	return sb.String()
}

// QQ 在单聊里会以 content_type=file 下发图片/视频, 只能靠扩展名兜底判断
func isImageFilename(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".heif":
		return true
	}
	return false
}

func isVideoFilename(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mov", ".m4v", ".avi", ".mkv":
		return true
	}
	return false
}

// attachmentSegment 附件转 OneBot 消息段
func attachmentSegment(a qqbot.MessageAttachment) (Segment, string) {
	ct := strings.ToLower(a.ContentType)
	name := a.Filename
	if name == "" {
		name = "attachment"
	}

	switch {
	case strings.HasPrefix(ct, "image/") || (ct == "file" && isImageFilename(a.Filename)):
		return Segment{Type: "image", Data: map[string]string{"file": a.URL, "url": a.URL}},
			"[CQ:image,file=" + name + "]"
	case ct == "voice" || strings.HasPrefix(ct, "audio/"):
		url := a.VoiceWavURL
		if url == "" {
			url = a.URL
		}
		return Segment{Type: "record", Data: map[string]string{"file": url, "url": url}},
			"[CQ:record,file=" + name + "]"
	case strings.HasPrefix(ct, "video/") || (ct == "file" && isVideoFilename(a.Filename)):
		return Segment{Type: "video", Data: map[string]string{"file": a.URL, "url": a.URL}},
			"[CQ:video,file=" + name + "]"
	default:
		return Segment{Type: "file", Data: map[string]string{"file": a.URL, "url": a.URL, "name": a.Filename}},
			"[CQ:file,name=" + name + "]"
	}
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

	c.ids.SetExtra(idmap.KindUser, openid, map[string]any{
		"nickname": msg.Author.Username,
		"avatar":   msg.Author.Avatar,
		"scene":    "private",
	})

	c.reply.Set("private", openid, msg.ID)

	c.broadcast(wsEvent{
		Time:        eventTime(msg.Timestamp),
		SelfID:      selfNum,
		PostType:    "message",
		MessageType: "private",
		SubType:     "friend",
		MessageID:   msgNum,
		UserID:      userNum,
		RawMessage:  buildRawMessage(msg.Content, msg.Attachments, 0),
		Sender: &wsSender{
			UserID:   userNum,
			Nickname: displayName(msg.Author, userNum),
			Sex:      "unknown",
		},
		Message: buildMessageSegments(msg.Content, msg.Attachments, 0),
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

	role := msg.Author.MemberRole
	if role == "" {
		role = "member"
	}
	c.ids.SetExtra(idmap.KindUser, openid, map[string]any{
		"nickname": msg.Author.Username,
		"card":     msg.Author.MemberNickname,
		"role":     role,
		"avatar":   msg.Author.Avatar,
		"group":    msg.GroupOpenID,
	})

	c.reply.Set("group", msg.GroupOpenID, msg.ID)
	text := StripMention(msg.Content)

	c.broadcast(wsEvent{
		Time:        eventTime(msg.Timestamp),
		SelfID:      selfNum,
		PostType:    "message",
		MessageType: "group",
		SubType:     "normal",
		MessageID:   msgNum,
		UserID:      userNum,
		GroupID:     groupNum,
		RawMessage:  buildRawMessage(text, msg.Attachments, selfNum),
		Font:        0,
		Sender: &wsSender{
			UserID:   userNum,
			Nickname: displayName(msg.Author, userNum),
			Card:     msg.Author.MemberNickname,
			Role:     role,
			Sex:      "unknown",
		},
		Message: buildMessageSegments(text, msg.Attachments, selfNum),
	})
}
