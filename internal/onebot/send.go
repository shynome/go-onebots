package onebot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/shynome/go-onebots/internal/bot"
	"github.com/shynome/go-onebots/internal/media"
	"github.com/shynome/go-onebots/internal/qqbot"
)

// maxOutbounds QQ 被动回复最多 5 条
const maxOutbounds = 5

type mediaItem struct {
	kind string // image / record / video / file
	file string // OneBot 段里的 file 或 url
}

type outbound struct {
	content string
	media   *mediaItem
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

// buildOutbounds 把消息段编排成若干条 QQ 消息
// 一条 QQ 消息只能带一个媒体, 文本附在第一条媒体上
// at 段会转成 content 里的 <@openid> / <@all> 标记
func (c *BotContext) buildOutbounds(segs []Segment, scene string) (out []outbound, skipped []string) {
	var sb strings.Builder
	var medias []mediaItem

	for _, s := range segs {
		switch s.Type {
		case "text":
			sb.WriteString(s.Data["text"])
		case "at":
			// 实测 QQ 发送侧不识别 <@openid> / <@all>, 会原样显示, 因此跳过
			skipped = append(skipped, "at(官方不支持发送)")
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

	text := strings.TrimSpace(sb.String())
	if len(medias) == 0 {
		if text != "" {
			out = append(out, outbound{content: text})
		}
		return out, skipped
	}

	out = append(out, outbound{content: text, media: &medias[0]})
	for i := 1; i < len(medias); i++ {
		m := medias[i]
		out = append(out, outbound{media: &m})
	}
	if len(out) > maxOutbounds {
		skipped = append(skipped, fmt.Sprintf("超出被动回复上限 %d 条", len(out)-maxOutbounds))
		out = out[:maxOutbounds]
	}
	return out, skipped
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

// sendOutbounds 按编排结果逐条发送, 每条都消耗一次被动回复序号
func (c *BotContext) sendOutbounds(ctx context.Context, b *bot.Bot, scene, openid string, out []outbound) (qqbot.SendResult, error) {
	var last qqbot.SendResult

	for i := range out {
		ob := out[i]
		req := qqbot.SendMessageRequest{
			MsgType: qqbot.MsgTypeText,
			Content: ob.content,
		}

		if ob.media != nil {
			fileURL, err := c.resolveMediaURL(ctx, *ob.media)
			if err != nil {
				return last, err
			}
			up, err := c.uploadMediaToQQ(ctx, b, scene, openid, qqbot.UploadRequest{
				FileType: fileTypeOf(ob.media.kind),
				URL:      fileURL,
			})
			if err != nil {
				return last, fmt.Errorf("上传媒体到 QQ 失败: %w", err)
			}
			req.MsgType = qqbot.MsgTypeMedia
			req.Media = &qqbot.Media{FileInfo: up.FileInfo}
		}

		msgID, seq := c.reply.Next(scene, openid)
		if msgID == "" {
			slog.Warn("被动回复窗口已过期, 尝试发送主动消息",
				"bot", c.botID, "scene", scene, "target", openid, "index", i)
		}
		req.MsgID = msgID
		req.MsgSeq = seq

		res, err := c.sendQQMessage(ctx, b, scene, openid, req)
		if err != nil {
			return last, err
		}
		last = res
	}
	return last, nil
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
// 公网 URL 直接透传, base64 / 本地文件存进 PocketBase 后返回 /api/files 链接
func (c *BotContext) resolveMediaURL(ctx context.Context, m mediaItem) (string, error) {
	file := m.file

	switch {
	case strings.HasPrefix(file, "http://"), strings.HasPrefix(file, "https://"):
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
func logSkipped(botID string, skipped []string) {
	if len(skipped) > 0 {
		slog.Warn("跳过部分消息段", "bot", botID, "segments", skipped)
	}
}
