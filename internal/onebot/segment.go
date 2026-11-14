package onebot

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Segment OneBot v11 消息段
type Segment struct {
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}

func textSegment(text string) Segment {
	return Segment{Type: "text", Data: map[string]string{"text": text}}
}

func atSegment(qq int64) Segment {
	return Segment{Type: "at", Data: map[string]string{"qq": itoa(qq)}}
}

// cqRe 匹配 CQ 码, MVP 只做剥离
var cqRe = regexp.MustCompile(`\[CQ:[^\]]*\]`)

var cqEscaper = strings.NewReplacer(
	"&#91;", "[",
	"&#93;", "]",
	"&#44;", ",",
	"&amp;", "&",
)

// ExtractText 从 OneBot 的 message 参数里取出纯文本
// 支持段数组与 CQ 码字符串两种形式; 非文本段会被跳过并记录原因
func ExtractText(raw json.RawMessage) (string, []string) {
	var skipped []string
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", skipped
	}

	switch s[0] {
	case '[':
		var segs []Segment
		if err := json.Unmarshal(raw, &segs); err != nil {
			return "", []string{"message 解析失败: " + err.Error()}
		}
		var sb strings.Builder
		for _, seg := range segs {
			if seg.Type == "text" {
				sb.WriteString(seg.Data["text"])
				continue
			}
			skipped = append(skipped, seg.Type)
		}
		return sb.String(), skipped
	case '{':
		var seg Segment
		if err := json.Unmarshal(raw, &seg); err != nil {
			return "", []string{"message 解析失败: " + err.Error()}
		}
		if seg.Type == "text" {
			return seg.Data["text"], skipped
		}
		return "", []string{seg.Type}
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return "", []string{"message 解析失败: " + err.Error()}
		}
		if cqRe.MatchString(str) {
			skipped = append(skipped, "cq-code")
		}
		return strings.TrimSpace(cqEscaper.Replace(cqRe.ReplaceAllString(str, ""))), skipped
	}
	return s, skipped
}
