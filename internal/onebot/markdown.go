package onebot

import (
	"bytes"
	"context"
	"encoding/base64"
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

// resolveImageForMarkdown 返回 markdown 可直接引用的图片 URL 与尺寸
// 公网 URL 只需要额外下载一次读尺寸; 内网 / base64 / 本地文件转存到 S3
func (c *BotContext) resolveImageForMarkdown(ctx context.Context, file string) (string, int, int, error) {
	isRemote := strings.HasPrefix(file, "http://") || strings.HasPrefix(file, "https://")

	// 内网 / 本地 / base64: 都得先拿到数据, 顺便读尺寸
	switch {
	case isRemote && needMirror(file):
		data, err := c.downloadBytes(ctx, file)
		if err != nil {
			return "", 0, 0, fmt.Errorf("内网图片需要服务端本身能访问: %w", err)
		}
		url, err := media.New(c.mgr.app).SaveBytes(c.botID, "image", "media"+extensionOf(data), data)
		if err != nil {
			return "", 0, 0, err
		}
		if err := verifyMediaURL(ctx, url); err != nil {
			return "", 0, 0, err
		}
		w, h := imageSize(data)
		return url, w, h, nil

	case strings.HasPrefix(file, "base64://"):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(file, "base64://"))
		if err != nil {
			return "", 0, 0, fmt.Errorf("base64 解码失败: %w", err)
		}
		url, err := media.New(c.mgr.app).SaveBytes(c.botID, "image", "media"+extensionOf(raw), raw)
		if err != nil {
			return "", 0, 0, err
		}
		w, h := imageSize(raw)
		return url, w, h, verifyMediaURL(ctx, url)

	default:
		// 公网 URL 或本地文件
		w, h := 0, 0
		if isRemote {
			if data, err := c.downloadBytes(ctx, file); err == nil {
				w, h = imageSize(data)
			}
		} else if data, err := os.ReadFile(strings.TrimPrefix(file, "file://")); err == nil {
			w, h = imageSize(data)
		}
		url, err := c.resolveMediaURL(ctx, mediaItem{kind: "image", file: file})
		if err != nil {
			return "", 0, 0, err
		}
		return url, w, h, nil
	}
}

// markdownFromSegments 把文本与图片按原顺序拼成 markdown
func (c *BotContext) markdownFromSegments(ctx context.Context, segs []Segment) (string, error) {
	var sb strings.Builder
	for _, s := range segs {
		switch s.Type {
		case "text":
			sb.WriteString(s.Data["text"])
		case "image":
			file := s.Data["file"]
			if file == "" {
				file = s.Data["url"]
			}
			if file == "" {
				continue
			}
			url, w, h, err := c.resolveImageForMarkdown(ctx, file)
			if err != nil {
				return "", err
			}
			sb.WriteString(markdownImage(w, h, url))
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
