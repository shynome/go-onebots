package onebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/eventlog"
	"github.com/shynome/go-onebots/internal/idmap"
	"github.com/shynome/go-onebots/internal/media"
	"github.com/shynome/go-onebots/internal/outbox"
	"github.com/shynome/go-onebots/internal/qqbot"
)

// maxOutbounds QQ 被动回复最多 5 条
const maxOutbounds = 5

type mediaItem struct {
	kind string // image / record / video / file
	file string // OneBot 段里的 file 或 url
	url  string // 已解析出的可访问 URL, 重试时复用
}

type outbound struct {
	content  string
	markdown string    // 非空表示直接作为 markdown 发送(msg_type=2)
	segments []Segment // 非空表示发送时拼成 markdown(文本+图片保序)
	keyboard json.RawMessage
	media    *mediaItem
	// replyTo 引用回复的目标 OneBot message_id, 空表示不引用
	replyTo string
}

// ParseSegments 解析 OneBot 的 message 参数, 支持段数组 / 单段 / CQ 码字符串
func ParseSegments(raw json.RawMessage) ([]Segment, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}

	switch s[0] {
	case '[':
		var segs []Segment
		if err := json.Unmarshal(raw, &segs); err != nil {
			return nil, err
		}
		return segs, nil
	case '{':
		var seg Segment
		if err := json.Unmarshal(raw, &seg); err != nil {
			return nil, err
		}
		return []Segment{seg}, nil
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return nil, err
		}
		return parseCQString(str), nil
	}
	return []Segment{textSegment(s)}, nil
}

// cqItemRe 匹配 CQ 码, 第二个捕获组是参数部分(以逗号开头)
var cqItemRe = regexp.MustCompile(`\[CQ:([A-Za-z0-9_.-]+)((?:,[^\]]*)?)\]`)

func parseCQString(s string) []Segment {
	var segs []Segment

	pushText := func(t string) {
		if t = strings.TrimSpace(cqEscaper.Replace(t)); t != "" {
			segs = append(segs, textSegment(t))
		}
	}

	last := 0
	for _, m := range cqItemRe.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > last {
			pushText(s[last:m[0]])
		}
		typ := s[m[2]:m[3]]
		data := map[string]string{}
		for _, kv := range strings.Split(strings.TrimPrefix(s[m[4]:m[5]], ","), ",") {
			if kv == "" {
				continue
			}
			if k, v, ok := strings.Cut(kv, "="); ok {
				data[k] = cqEscaper.Replace(v)
			}
		}
		segs = append(segs, Segment{Type: typ, Data: data})
		last = m[1]
	}
	if last < len(s) {
		pushText(s[last:])
	}
	return segs
}

// keyboardFromSegment 解析 keyboard 段, 返回可直接发给 QQ 的键盘 JSON
// 支持三种写法(段里的 data 只能放字符串, 所以嵌套结构用 JSON 字符串表达):
//   - data.content: 完整键盘对象, 如 {"content":{"rows":[...]}} 或 {"id":"..."}
//   - data.content: 仅按钮行数组 [{"buttons":[...]}], 自动包成 content.rows
//   - data.id: 平台预设键盘模板 ID, 等价于 {"id":"..."}
func keyboardFromSegment(s Segment) (json.RawMessage, error) {
	raw := strings.TrimSpace(s.Data["content"])
	if raw == "" {
		if id := strings.TrimSpace(s.Data["id"]); id != "" {
			kb, err := json.Marshal(map[string]string{"id": id})
			if err != nil {
				return nil, err
			}
			return kb, nil
		}
		return nil, fmt.Errorf("缺少 content 或 id")
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("content 不是合法 JSON")
	}
	if raw[0] == '[' {
		// 只给了按钮行, 补上 keyboard.content.rows 外壳
		kb, err := json.Marshal(map[string]any{
			"content": map[string]json.RawMessage{"rows": json.RawMessage(raw)},
		})
		if err != nil {
			return nil, err
		}
		return kb, nil
	}
	if raw[0] != '{' {
		return nil, fmt.Errorf("content 需要是 JSON 对象或数组")
	}
	return json.RawMessage(raw), nil
}

func hasNonImageMedia(medias []mediaItem) bool {
	for _, m := range medias {
		if m.kind != "image" {
			return true
		}
	}
	return false
}

// buildOutbounds 把消息段编排成若干条 QQ 消息
// 一条 QQ 消息只能带一个媒体, 文本附在第一条媒体上
// at 段会转成 content 里的 <@openid> / <@all> 标记
func (c *BotContext) buildOutbounds(segs []Segment, scene string) (out []outbound, skipped []string) {
	var sb strings.Builder
	var medias []mediaItem
	var markdown string
	var replyTo string
	var keyboard json.RawMessage
	// 群聊的 @用户 只支持 markdown 消息, 有单人 at 时优先走 markdown
	var atUser bool

	for _, s := range segs {
		switch s.Type {
		case "text":
			sb.WriteString(s.Data["text"])
		case "reply":
			// 引用回复: 后面的发送会把它映射成 QQ 的 msg_id + msg_seq
			if id := s.Data["id"]; id != "" {
				replyTo = id
			}
		case "markdown":
			// 显式 markdown 段: 原样交给 QQ, 用于图文混排
			markdown = s.Data["content"]
		case "keyboard":
			// 内嵌键盘(互动按钮), QQ 侧只认挂载在 markdown 消息上的形式
			kb, err := keyboardFromSegment(s)
			if err != nil {
				skipped = append(skipped, "keyboard("+err.Error()+")")
				continue
			}
			keyboard = kb
		case "at":
			qq := s.Data["qq"]
			switch {
			case scene != "group":
				skipped = append(skipped, "at(私聊)")
			case qq == "all":
				// @全体 文本消息与 markdown 都支持, 这里写给纯文本路径
				sb.WriteString("<qqbot-at-everyone />")
			default:
				// 单人的 @ 标记格式未找到官方说明, 先跳过
				skipped = append(skipped, "at(仅支持 @全体)")
			}
		case "image", "record", "video", "file":
			file := s.Data["file"]
			if file == "" {
				file = s.Data["url"]
			}
			if file == "" {
				skipped = append(skipped, s.Type+"(缺少 file)")
				continue
			}
			medias = append(medias, mediaItem{kind: s.Type, file: file})
		default:
			skipped = append(skipped, s.Type)
		}
	}

	// 引用回复只挂在第一条消息上
	attachReply := func(outs []outbound) []outbound {
		if replyTo != "" && len(outs) > 0 {
			outs[0].replyTo = replyTo
		}
		return outs
	}

	text := strings.TrimSpace(sb.String())

	// 带按钮的消息必须以 markdown(msg_type=2) 发送, 所以这里优先走 markdown 分支
	if len(keyboard) > 0 {
		switch {
		case markdown != "":
			return attachReply([]outbound{{markdown: markdown, keyboard: keyboard}}), skipped
		case hasNonImageMedia(medias):
			skipped = append(skipped, "keyboard(不能与语音/视频/文件同发)")
		default:
			if mdSegs := markdownTextSegments(segs); len(mdSegs) > 0 {
				return attachReply([]outbound{{segments: mdSegs, keyboard: keyboard}}), skipped
			}
			skipped = append(skipped, "keyboard(需要同时有文本内容)")
		}
	}

	if markdown != "" {
		out = append(out, outbound{markdown: markdown})
		return attachReply(out), skipped
	}
	if len(medias) == 0 || onlyImages(medias) {
		if atUser {
			if mdSegs := markdownTextSegments(segs); len(mdSegs) > 0 {
				return attachReply([]outbound{{segments: mdSegs}}), skipped
			}
		}
	}
	if atUser {
		skipped = append(skipped, "at(单人, 与语音/视频/文件共存时不支持)")
	}

	if len(medias) == 0 {
		if text != "" {
			out = append(out, outbound{content: text})
		}
		return attachReply(out), skipped
	}

	// 文本 + 纯图片: 改用 markdown, 否则 QQ 富媒体消息固定把图片排在文字前面
	if text != "" && onlyImages(medias) {
		if mdSegs := markdownTextSegments(segs); len(mdSegs) > 1 {
			return attachReply([]outbound{{segments: mdSegs}}), skipped
		}
	}

	out = append(out, outbound{content: text, media: &medias[0]})
	for i := 1; i < len(medias); i++ {
		m := medias[i]
		out = append(out, outbound{media: &m})
	}
	if len(out) > maxOutbounds {
		skipped = append(skipped, fmt.Sprintf("超出单次发送上限 %d 条", len(out)-maxOutbounds))
		out = out[:maxOutbounds]
	}
	return attachReply(out), skipped
}
func fileTypeOf(kind string) qqbot.FileType {
	switch kind {
	case "image":
		return qqbot.FileTypeImage
	case "video":
		return qqbot.FileTypeVideo
	case "record":
		return qqbot.FileTypeAudio
	default:
		return qqbot.FileTypeRaw
	}
}

// 发送重试策略: 同步重试 2 次(共 3 次尝试), 仍失败则标记 failed
const (
	maxSendRetry  = 2
	sendRetryBase = time.Second
)

// retryable 判断错误是否值得重试
// QQ 返回的 4xx 基本是内容审核/权限/频次问题, 重试也无效
func retryable(err error) bool {
	var apiErr *qqbot.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	return true
}

func retryBackoff(attempt int) time.Duration {
	return sendRetryBase * time.Duration(1<<attempt)
}

// sendOutbounds 按编排结果逐条发送, 每条都消耗一次被动回复序号
// 每条消息先写 outbox 再发送, 进程崩溃后可以由定时任务补发
func (c *BotContext) sendOutbounds(ctx context.Context, b *bot.Bot, scene, openid string, out []outbound) (qqbot.SendResult, error) {
	var last qqbot.SendResult

	for i := range out {
		rec, err := c.enqueueOutbound(scene, openid, out[i])
		if err != nil {
			return last, fmt.Errorf("写入发送队列失败: %w", err)
		}

		res, err := c.deliver(ctx, b, rec)
		if err != nil {
			return last, err
		}
		last = res
	}
	return last, nil
}

func (c *BotContext) enqueueOutbound(scene, openid string, ob outbound) (*core.Record, error) {
	it := outbox.Item{
		BotID:    c.botID,
		Scene:    scene,
		Target:   openid,
		Content:  ob.content,
		Markdown: ob.markdown,
		Keyboard: ob.keyboard,
	}
	if ob.replyTo != "" {
		// OneBot message_id -> 入站事件里的 msg_idx, QQ 的 message_reference 用它
		num, err := strconv.ParseInt(ob.replyTo, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("引用回复的 message_id 不是数字: %s", ob.replyTo)
		}
		_, extra, err := c.ids.MessageExtra(num)
		if err != nil {
			return nil, fmt.Errorf("引用回复的消息不存在: %w", err)
		}
		refIdx, _ := extra["msg_idx"].(string)
		if refIdx == "" {
			return nil, fmt.Errorf("该消息没有引用信息(仅支持引用收到的消息)")
		}
		it.ReplyTo = refIdx
	}
	if len(ob.segments) > 0 {
		if data, err := json.Marshal(ob.segments); err == nil {
			it.Segments = data
		}
	}
	if ob.media != nil {
		it.MediaKind = ob.media.kind
		it.MediaFile = ob.media.file
		it.MediaURL = ob.media.url
	}
	return c.outbox.Enqueue(it)
}

// deliver 发送一条 outbox 记录, 内部做有限次重试
func (c *BotContext) deliver(ctx context.Context, b *bot.Bot, rec *core.Record) (qqbot.SendResult, error) {
	var last qqbot.SendResult

	for attempt := 0; ; attempt++ {
		res, err := c.deliverOnce(ctx, b, rec)
		if err == nil {
			if serr := c.outbox.MarkSent(rec, res.ID); serr != nil {
				c.logger.Warn("更新发送状态失败", "bot", c.botID, "outbox", rec.Id, "error", serr)
			}
			c.recordSent(rec, res.ID)
			return res, nil
		}
		last = res

		if !retryable(err) || attempt >= maxSendRetry {
			if ferr := c.outbox.MarkFailed(rec, err.Error()); ferr != nil {
				c.logger.Warn("更新发送状态失败", "bot", c.botID, "outbox", rec.Id, "error", ferr)
			}
			return last, err
		}

		delay := retryBackoff(attempt)
		c.logger.Warn("发送失败, 准备重试", "bot", c.botID, "attempt", attempt+1, "delay", delay, "error", err)
		if rerr := c.outbox.RetryLater(rec, time.Now().Add(delay), err.Error()); rerr != nil {
			c.logger.Warn("更新重试时间失败", "bot", c.botID, "outbox", rec.Id, "error", rerr)
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			c.outbox.MarkFailed(rec, "已取消: "+ctx.Err().Error())
			return last, ctx.Err()
		}
	}
}

// deliverOnce 尝试发送一次
func (c *BotContext) deliverOnce(ctx context.Context, b *bot.Bot, rec *core.Record) (qqbot.SendResult, error) {
	scene := rec.GetString("scene")
	openid := rec.GetString("target")

	var m *mediaItem
	if kind := rec.GetString("media_kind"); kind != "" {
		m = &mediaItem{kind: kind, file: rec.GetString("media_file"), url: rec.GetString("media_url")}
	}

	req := qqbot.SendMessageRequest{
		MsgType: qqbot.MsgTypeText,
		Content: rec.GetString("content"),
	}

	// markdown 消息: 图文混排, 图片顺序由 content 决定
	if md := rec.GetString("markdown"); md != "" {
		req.MsgType = qqbot.MsgTypeMarkdown
		req.Content = ""
		req.Markdown = &qqbot.Markdown{Content: md}
	} else if segs := outboxSegments(rec); len(segs) > 0 {
		// 文本 + 图片: 发送时拼成 markdown(图片带尺寸)
		// 图片本身按来源缓存在 onebot_media, 重试不会重复下载
		md, err := c.markdownFromSegments(ctx, segs)
		if err != nil {
			return qqbot.SendResult{}, err
		}
		req.MsgType = qqbot.MsgTypeMarkdown
		req.Content = ""
		req.Markdown = &qqbot.Markdown{Content: md}
	} else if m != nil {
		fileURL, err := c.resolveMediaURL(ctx, *m)
		if err != nil {
			return qqbot.SendResult{}, err
		}
		if m.url == "" {
			if serr := c.outbox.SetMediaURL(rec, fileURL); serr != nil {
				c.logger.Debug("缓存媒体 URL 失败", "bot", c.botID, "outbox", rec.Id, "error", serr)
			}
		}
		up, err := c.uploadMediaToQQ(ctx, b, scene, openid, qqbot.UploadRequest{
			FileType: fileTypeOf(m.kind),
			URL:      fileURL,
		})
		if err != nil {
			return qqbot.SendResult{}, fmt.Errorf("上传媒体到 QQ 失败: %w", err)
		}
		req.MsgType = qqbot.MsgTypeMedia
		req.Media = &qqbot.Media{FileInfo: up.FileInfo}
	}

	// 引用回复: QQ 用 message_reference 表示引用, 与被动回复的 msg_id 是两回事
	if ref := rec.GetString("reply_to"); ref != "" {
		req.MessageReference = &qqbot.MessageReference{MessageID: ref}
	}
	// 内嵌键盘: 与 markdown 一起发送, 用户端会在消息底部挂按钮
	if kb := outboxKeyboard(rec); len(kb) > 0 {
		req.Keyboard = kb
	}

	return c.sendQQMessage(ctx, b, scene, openid, req)
}

// outboxKeyboard 取出待发送的内嵌键盘 JSON
func outboxKeyboard(rec *core.Record) json.RawMessage {
	raw, _ := rec.Get("keyboard").(types.JSONRaw)
	if len(raw) == 0 {
		if v, ok := rec.Get("keyboard").(string); ok {
			raw = types.JSONRaw(v)
		}
	}
	return json.RawMessage(raw)
}

// outboxSegments 取出待拼 markdown 的原始段
func outboxSegments(rec *core.Record) []Segment {
	raw, _ := rec.Get("segments").(types.JSONRaw)
	if len(raw) == 0 {
		if v, ok := rec.Get("segments").(string); ok {
			raw = types.JSONRaw(v)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	var segs []Segment
	if err := json.Unmarshal(raw, &segs); err != nil {
		return nil
	}
	return segs
}

// recordSent 把机器人自己发出的消息写进事件日志
// 直接标记为已投递, 不会补发给客户端, 仅供 get_msg 与群历史查询使用
func (c *BotContext) recordSent(rec *core.Record, messageID string) {
	if messageID == "" {
		return
	}

	scene := rec.GetString("scene")
	openid := rec.GetString("target")
	ob := outbound{content: rec.GetString("content")}
	if kind := rec.GetString("media_kind"); kind != "" {
		ob.media = &mediaItem{kind: kind, file: rec.GetString("media_file")}
	}

	num, err := c.ids.MessageNum(messageID, map[string]any{
		"scene": scene, "target": openid,
	})
	if err != nil {
		c.logger.Warn("记录发出的消息失败", "bot", c.botID, "error", err)
		return
	}

	segs := make([]Segment, 0, 2)
	if md := rec.GetString("markdown"); md != "" {
		segs = append(segs, Segment{Type: "markdown", Data: map[string]string{"content": md}})
	} else {
		if ob.content != "" {
			segs = append(segs, textSegment(ob.content))
		}
		if ob.media != nil {
			segs = append(segs, Segment{Type: ob.media.kind, Data: map[string]string{"file": ob.media.file}})
		}
	}
	// 按钮也记进消息日志, get_msg / 群历史里能看出来发过键盘
	if kb := outboxKeyboard(rec); len(kb) > 0 {
		segs = append(segs, Segment{Type: "keyboard", Data: map[string]string{"content": string(kb)}})
	}

	selfNum, selfName := c.self()
	ev := wsEvent{
		Time:        time.Now().Unix(),
		SelfID:      selfNum,
		PostType:    "message_sent",
		MessageType: scene,
		SubType:     "normal",
		MessageID:   num,
		RawMessage:  rawFromSegments(segs),
		Sender:      &wsSender{UserID: selfNum, Nickname: selfName, Role: "member", Sex: "unknown"},
		Message:     segs,
	}

	var userNum int64
	if scene == "group" {
		// 与入站事件保持一致: 已填写真实号码的用真实号码
		if n, ok := c.numberBinds(idmap.KindGroup)[openid]; ok {
			ev.GroupID = n
		} else if n, err := c.ids.Num(idmap.KindGroup, openid); err == nil {
			ev.GroupID = n
		}
	} else {
		if n, ok := c.numberBinds(idmap.KindUser)[openid]; ok {
			userNum, ev.UserID = n, n
		} else if n, err := c.ids.Num(idmap.KindUser, openid); err == nil {
			userNum, ev.UserID = n, n
		}
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	seq := atomic.AddInt64(&c.seq, 1)
	if _, err := c.log.Append(eventlog.Record{
		Seq:         seq,
		MessageType: scene,
		UserID:      userNum,
		GroupID:     ev.GroupID,
		MessageID:   num,
		Content:     ev.RawMessage,
		Data:        data,
		Delivered:   true,
	}); err != nil {
		c.logger.Warn("记录发出的消息失败", "bot", c.botID, "error", err)
	}
}

func (c *BotContext) uploadMediaToQQ(ctx context.Context, b *bot.Bot, scene, openid string, req qqbot.UploadRequest) (qqbot.UploadResult, error) {
	if scene == "group" {
		return b.Client().UploadGroupMedia(ctx, openid, req)
	}
	return b.Client().UploadUserMedia(ctx, openid, req)
}

func (c *BotContext) sendQQMessage(ctx context.Context, b *bot.Bot, scene, openid string, req qqbot.SendMessageRequest) (qqbot.SendResult, error) {
	if scene == "group" {
		return b.Client().SendGroupMessage(ctx, openid, req)
	}
	return b.Client().SendUserMessage(ctx, openid, req)
}

// resolveMediaURL 把消息段里的媒体来源转成 QQ 可访问的 URL
// 公网 URL 直接透传, base64 / 本地文件 / 内网 URL 存进 PocketBase 后返回预签名链接
func (c *BotContext) resolveMediaURL(ctx context.Context, m mediaItem) (string, error) {
	if m.url != "" {
		return m.url, nil
	}
	file := m.file

	switch {
	case strings.HasPrefix(file, "http://"), strings.HasPrefix(file, "https://"):
		if needMirror(file) {
			return c.mirrorRemoteMedia(ctx, file, m.kind)
		}
		return file, nil

	case strings.HasPrefix(file, "base64://"):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(file, "base64://"))
		if err != nil {
			return "", fmt.Errorf("base64 解码失败: %w", err)
		}
		name := "media" + extensionOf(raw)
		url, err := media.New(c.mgr.app).SaveBytes(c.botID, m.kind, name, raw)
		if err != nil {
			return "", err
		}
		return url, verifyMediaURL(ctx, url)

	default:
		localPath := strings.TrimPrefix(file, "file://")
		url, err := media.New(c.mgr.app).SavePath(c.botID, m.kind, localPath)
		if err != nil {
			return "", fmt.Errorf("保存本地媒体失败(仅支持服务本机路径): %w", err)
		}
		return url, verifyMediaURL(ctx, url)
	}
}

// maxMirrorSize 内网媒体转存时允许的最大体积
const maxMirrorSize = 20 << 20

// mirrorTimeout 内网媒体下载超时, 慢内网的大文件可能要很久
const mirrorTimeout = 3 * time.Minute

// needMirror 判断 URL 是否指向内网或本机地址
// QQ 服务器在公网, 拉不到这类地址, 必须先由服务端自己下载转存
func needMirror(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}

	// IP 字面量按网段判断, 避免 IPv6 公网地址被主机名规则误判
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsPrivate() || ip.IsLoopback() ||
			ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}

	switch {
	case strings.EqualFold(host, "localhost"),
		strings.HasSuffix(strings.ToLower(host), ".local"),
		// 不含点的单段主机名(如 http://nas/xxx)只可能在内网解析
		!strings.Contains(host, "."):
		return true
	}
	return false
}

// mirrorRemoteMedia 把内网媒体下载下来存进 PocketBase, 返回 QQ 可访问的预签名 URL
func (c *BotContext) mirrorRemoteMedia(ctx context.Context, rawURL, kind string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, mirrorTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("内网媒体需要服务端本身能访问该地址, 下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载内网媒体失败: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMirrorSize+1))
	if err != nil {
		return "", fmt.Errorf("读取内网媒体失败: %w", err)
	}
	if len(data) > maxMirrorSize {
		return "", fmt.Errorf("内网媒体超过 %d MB 上限", maxMirrorSize>>20)
	}

	name := "media" + extensionOf(data)
	link, err := media.New(c.mgr.app).SaveBytes(c.botID, kind, name, data)
	if err != nil {
		return "", err
	}
	return link, verifyMediaURL(ctx, link)
}

// extensionOf 根据文件头推断扩展名, PocketBase 会用文件名后缀判断 MIME
func extensionOf(data []byte) string {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	ct := http.DetectContentType(head)
	if exts, err := mime.ExtensionsByType(ct); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

// verifyMediaURL 确认中转后的链接真的可下载, 否则 QQ 只会返回 40093007
func verifyMediaURL(ctx context.Context, url string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("媒体链接不可访问: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("媒体链接返回 %d: %s", resp.StatusCode, body)
	}
	return nil
}

// logSkipped 记录被忽略的消息段, 避免静默丢内容
func (c *BotContext) logSkipped(skipped []string) {
	if len(skipped) > 0 {
		c.logger.Warn("跳过部分消息段", "segments", skipped)
	}
}
