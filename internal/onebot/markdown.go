package onebot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"

	// 注册常见图片格式用于解析尺寸
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shynome/go-onebots/internal/media"
)

// markdownTimeout markdown 图片解析(下载/转存)超时
const markdownTimeout = 3 * time.Minute

// markdownTextSegments 挑出 markdown 能表达的段(文本与图片)
func markdownTextSegments(segs []Segment) []Segment {
	out := make([]Segment, 0, len(segs))
	for _, s := range segs {
		switch s.Type {
		case "text", "image":
			out = append(out, s)
		}
	}
	return out
}

// onlyImages 判断媒体是否全是图片(markdown 只支持图片)
func onlyImages(medias []mediaItem) bool {
	if len(medias) == 0 {
		return false
	}
	for _, m := range medias {
		if m.kind != "image" {
			return false
		}
	}
	return true
}

// imageSize 解析图片宽高, 失败返回 0,0
func imageSize(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// markdownImage QQ markdown 的图片必须带尺寸, 否则会退化成 [alt] 文字
func markdownImage(w, h int, url string) string {
	if w <= 0 || h <= 0 {
		// 尺寸解析失败时给个保守的占位, 至少能显示成图
		w, h = 300, 300
	}
	return fmt.Sprintf("![image #%dpx #%dpx](%s)", w, h, url)
}

// downloadBytes 下载远端资源(上限 maxMirrorSize)
func (c *BotContext) downloadBytes(ctx context.Context, rawURL string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, markdownTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载图片失败: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMirrorSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMirrorSize {
		return nil, fmt.Errorf("图片超过 %d MB 上限", maxMirrorSize>>20)
	}
	return data, nil
}

// imageSource 计算图片来源标识, 用于在 onebot_media 里跨消息去重
// 公网 URL 直接用链接本身; 本地/base64 用内容 hash(内容可能变化)
func imageSource(file string, data []byte) string {
	if strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://") {
		return "url:" + file
	}
	if len(data) > 0 {
		sum := sha256.Sum256(data)
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	return "file:" + file
}

// resolveImageForMarkdown 返回 markdown 可直接引用的图片 URL 与尺寸
// 同一个来源只会下载/转存一次, 命中 onebot_media 缓存后直接复用
func (c *BotContext) resolveImageForMarkdown(ctx context.Context, file string) (string, int, int, error) {
	store := media.New(c.mgr.app)
	isRemote := strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://")

	// 公网链接可以只凭 URL 判断缓存, 连下载都不需要
	if isRemote && !needMirror(file) {
		if img, ok, err := store.FindImage(c.botID, "url:"+file); err == nil && ok {
			return img.URL, img.Width, img.Height, nil
		}
	}

	// 其余情况先拿到原始数据(顺便用于内容 hash 与尺寸解析)
	var data []byte
	var err error
	switch {
	case isRemote:
		data, err = c.downloadBytes(ctx, file)
		if err != nil {
			return "", 0, 0, fmt.Errorf("图片需要服务端本身能访问: %w", err)
		}
	case strings.HasPrefix(file, "base64://"):
		data, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(file, "base64://"))
		if err != nil {
			return "", 0, 0, fmt.Errorf("base64 解码失败: %w", err)
		}
	default:
		data, err = os.ReadFile(strings.TrimPrefix(file, "file://"))
		if err != nil {
			return "", 0, 0, fmt.Errorf("读取本地图片失败(仅支持服务本机路径): %w", err)
		}
	}

	source := imageSource(file, data)
	if img, ok, ferr := store.FindImage(c.botID, source); ferr == nil && ok {
		return img.URL, img.Width, img.Height, nil
	}

	w, h := imageSize(data)
	url, err := store.SaveImage(c.botID, source, "media"+extensionOf(data), data, media.Image{Width: w, Height: h})
	if err != nil {
		return "", 0, 0, err
	}
	if err := verifyMediaURL(ctx, url); err != nil {
		return "", 0, 0, err
	}
	return url, w, h, nil
}

// markdownEscaper 转义 markdown 特殊字符
// 普通文本里的 * _ # 等不应被当成格式, 否则插件发的内容会变形
var markdownEscaper = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", `\*`,
	"_", `\_`,
	"[", `\[`,
	"]", `\]`,
	"#", `\#`,
	"~", `\~`,
	"|", `\|`,
	">", `\>`,
)

// escapeMarkdown 转义普通文本, 保留换行
func escapeMarkdown(s string) string {
	// 按行处理: 行首的列表/标题符号也一并转义
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		trimmed = markdownEscaper.Replace(trimmed)
		// 行首 - + 数字编号会被识别成列表
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "+") {
			trimmed = `\` + trimmed
		}
		lines[i] = indent + trimmed
	}
	return strings.Join(lines, "\n")
}

// resolvedImage 已解析的 markdown 图片
type resolvedImage struct {
	url  string
	w, h int
}

// markdownFromSegments 把文本与图片按原顺序拼成 markdown
// 同一条消息里重复的图片链接只会解析/下载一次
func (c *BotContext) markdownFromSegments(ctx context.Context, segs []Segment) (string, error) {
	cache := make(map[string]resolvedImage, len(segs))
	var sb strings.Builder
	for _, s := range segs {
		switch s.Type {
		case "text":
			sb.WriteString(escapeMarkdown(s.Data["text"]))
		case "image":
			file := s.Data["file"]
			if file == "" {
				file = s.Data["url"]
			}
			if file == "" {
				continue
			}
			img, ok := cache[file]
			if !ok {
				url, w, h, err := c.resolveImageForMarkdown(ctx, file)
				if err != nil {
					return "", err
				}
				img = resolvedImage{url: url, w: w, h: h}
				cache[file] = img
			}
			sb.WriteString(markdownImage(img.w, img.h, img.url))
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
