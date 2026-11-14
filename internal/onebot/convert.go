package onebot

import (
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shynome/go-onebots/internal/idmap"
	"github.com/shynome/go-onebots/internal/qqbot"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// mentionRe 匹配 content 里的 @ 提及标记, 捕获组为被 @ 的 openid
var mentionRe = regexp.MustCompile(`<@!?([^>]+)>`)

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
			c.logger.Warn("解析 READY 失败", "bot", c.botID, "error", err)
			return
		}
		num, err := c.ids.Num(idmap.KindSelf, ready.User.ID)
		if err != nil {
			c.logger.Warn("记录机器人自身 id 失败", "bot", c.botID, "error", err)
			return
		}
		c.setSelf(num, ready.User.Username, ready.User.ID)
	case qqbot.EventC2CMessageCreate:
		c.handleC2C(p.Data)
	case qqbot.EventGroupATMessageCreate:
		// 群 @ 机器人, 必然发给机器人
		c.handleGroup(p.Data, true)
	case qqbot.EventGroupMessageCreate:
		// 群内全量消息, 是否 @ 机器人由 mentions 决定
		c.handleGroup(p.Data, false)
	}
}

// isSelfMessage 判断这条 QQ 消息是不是机器人自己发出去后回流的
// Author 里没有 bot 标记, 自己的 openid 在群里也与 READY 拿到的不同,
// 所以用「发送成功时登记过的 message_id」来判定
func (c *BotContext) isSelfMessage(messageID string) bool {
	return c.ids.HasMessage(messageID)
}

func (c *BotContext) handleC2C(data json.RawMessage) {
	var msg qqbot.C2CMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		c.logger.Warn("解析单聊消息失败", "bot", c.botID, "error", err)
		return
	}
	// 机器人自己发的消息会作为入站事件回流, 不能当成用户消息
	if c.isSelfMessage(msg.ID) {
		c.logger.Debug("跳过机器人自己发的单聊消息", "bot", c.botID, "message_id", msg.ID)
		return
	}
	if !c.firstSeen(msg.ID) {
		c.logger.Debug("跳过重复单聊消息", "bot", c.botID, "message_id", msg.ID)
		return
	}

	selfNum, _ := c.self()
	if selfNum == 0 {
		c.logger.Warn("机器人未就绪, 丢弃单聊消息", "bot", c.botID)
		return
	}

	openid := msg.Author.OpenID()
	userNum, err := c.ids.Num(idmap.KindUser, openid)
	if err != nil {
		c.logger.Warn("分配用户 id 失败", "bot", c.botID, "error", err)
		return
	}
	// 已填写真实 QQ 号时对外统一用真实号
	if qq, ok := c.numberBinds(idmap.KindUser)[openid]; ok {
		userNum = qq
	}
	msgNum, err := c.ids.MessageNum(msg.ID, map[string]any{
		"scene": "private", "target": openid,
	})
	if err != nil {
		c.logger.Warn("分配消息 id 失败", "bot", c.botID, "error", err)
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

func (c *BotContext) handleGroup(data json.RawMessage, atEvent bool) {
	var msg qqbot.GroupATMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		c.logger.Warn("解析群消息失败", "bot", c.botID, "error", err)
		return
	}
	// 机器人自己发的消息会作为 GROUP_MESSAGE_CREATE 回流:
	// 既不能当入站事件(会重复记录并推给客户端), 也不能拿它的 msg_id 做被动回复
	if c.isSelfMessage(msg.ID) {
		c.logger.Debug("跳过机器人自己发的群消息", "bot", c.botID, "message_id", msg.ID)
		return
	}
	if !c.firstSeen(msg.ID) {
		c.logger.Debug("跳过重复群消息", "bot", c.botID, "message_id", msg.ID)
		return
	}

	selfNum, _ := c.self()
	if selfNum == 0 {
		c.logger.Warn("机器人未就绪, 丢弃群消息", "bot", c.botID)
		return
	}

	openid := msg.Author.OpenID()
	userNum, err := c.ids.Num(idmap.KindUser, openid)
	if err != nil {
		c.logger.Warn("分配用户 id 失败", "bot", c.botID, "error", err)
		return
	}
	groupNum, err := c.ids.Num(idmap.KindGroup, msg.GroupOpenID)
	if err != nil {
		c.logger.Warn("分配群 id 失败", "bot", c.botID, "error", err)
		return
	}
	// 已填写的真实号码优先
	binds := c.numberBinds(idmap.KindGroup)
	if qq, ok := binds[msg.GroupOpenID]; ok {
		groupNum = qq
	}
	msgNum, err := c.ids.MessageNum(msg.ID, map[string]any{
		"scene": "group", "target": msg.GroupOpenID,
	})
	if err != nil {
		c.logger.Warn("分配消息 id 失败", "bot", c.botID, "error", err)
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
	segments := c.buildGroupSegments(msg, selfNum, atEvent)

	c.broadcast(wsEvent{
		Time:        eventTime(msg.Timestamp),
		SelfID:      selfNum,
		PostType:    "message",
		MessageType: "group",
		SubType:     "normal",
		MessageID:   msgNum,
		UserID:      userNum,
		GroupID:     groupNum,
		RawMessage:  rawFromSegments(segments),
		Font:        0,
		Sender: &wsSender{
			UserID:   userNum,
			Nickname: displayName(msg.Author, userNum),
			Card:     msg.Author.MemberNickname,
			Role:     role,
			Sex:      "unknown",
		},
		Message: segments,
	})
}

// buildGroupSegments 把群消息 content 拆成 OneBot 消息段
// atEvent 为 true(GROUP_AT_MESSAGE_CREATE) 时保证带 at 机器人段;
// 全量消息(GROUP_MESSAGE_CREATE) 只在 mentions 里 @ 了机器人才带, 否则 IsToMe=false
func (c *BotContext) buildGroupSegments(msg qqbot.GroupATMessage, selfNum int64, atEvent bool) []Segment {
	segs := c.splitContent(msg.Content)

	toMe := atEvent || mentionsSelf(msg.Mentions, c.selfOpenIDStr())
	if toMe {
		hasAtSelf := false
		for _, s := range segs {
			if s.Type == "at" && s.Data["qq"] == itoa(selfNum) {
				hasAtSelf = true
				break
			}
		}
		if !hasAtSelf {
			segs = append([]Segment{atSegment(selfNum)}, segs...)
		}
	}

	for _, a := range msg.Attachments {
		seg, _ := attachmentSegment(a)
		segs = append(segs, seg)
	}
	return segs
}

// splitContent 把 <@openid> / <@all> 占位符转成 at 段
func (c *BotContext) splitContent(content string) []Segment {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	matches := mentionRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return []Segment{textSegment(content)}
	}

	segs := make([]Segment, 0, len(matches)*2+1)
	pushText := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			segs = append(segs, textSegment(s))
		}
	}

	last := 0
	for _, m := range matches {
		if m[0] > last {
			pushText(content[last:m[0]])
		}
		openid := content[m[2]:m[3]]
		switch {
		case openid == "all":
			segs = append(segs, Segment{Type: "at", Data: map[string]string{"qq": "all"}})
		default:
			if num, err := c.ids.Num(idmap.KindUser, openid); err == nil {
				segs = append(segs, atSegment(num))
			} else {
				pushText("<@" + openid + ">")
			}
		}
		last = m[1]
	}
	if last < len(content) {
		pushText(content[last:])
	}
	return segs
}

func mentionsSelf(mentions []qqbot.User, selfOpenID string) bool {
	if selfOpenID == "" {
		return false
	}
	for _, u := range mentions {
		if u.MemberOpenID == selfOpenID || u.UserOpenID == selfOpenID || u.ID == selfOpenID {
			return true
		}
	}
	return false
}

// rawFromSegments 生成 CQ 码形式的 raw_message, 仅用于日志与兼容
func rawFromSegments(segs []Segment) string {
	var sb strings.Builder
	for _, s := range segs {
		switch s.Type {
		case "text":
			sb.WriteString(s.Data["text"])
		case "at":
			sb.WriteString("[CQ:at,qq=" + s.Data["qq"] + "] ")
		case "image":
			sb.WriteString("[CQ:image]")
		case "record":
			sb.WriteString("[CQ:record]")
		case "video":
			sb.WriteString("[CQ:video]")
		case "file":
			sb.WriteString("[CQ:file,name=" + s.Data["name"] + "]")
		}
	}
	return sb.String()
}
