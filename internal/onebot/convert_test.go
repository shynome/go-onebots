package onebot

import (
	"testing"

	"github.com/shynome/go-onebots/internal/qqbot"
)

func TestAttachmentSegment(t *testing.T) {
	cases := []struct {
		name string
		att  qqbot.MessageAttachment
		want string
	}{
		{"标准图片类型", qqbot.MessageAttachment{ContentType: "image/jpeg", URL: "u"}, "image"},
		{"QQ 以 file 下发图片", qqbot.MessageAttachment{ContentType: "file", Filename: "a.JPG", URL: "u"}, "image"},
		{"语音", qqbot.MessageAttachment{ContentType: "voice", URL: "u"}, "record"},
		{"视频", qqbot.MessageAttachment{ContentType: "video/mp4", URL: "u"}, "video"},
		{"QQ 以 file 下发视频", qqbot.MessageAttachment{ContentType: "file", Filename: "a.mp4", URL: "u"}, "video"},
		{"普通文件", qqbot.MessageAttachment{ContentType: "file", Filename: "a.pdf", URL: "u"}, "file"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seg, _ := attachmentSegment(tc.att)
			if seg.Type != tc.want {
				t.Fatalf("期望 %s, 实际 %s", tc.want, seg.Type)
			}
			if seg.Data["url"] != "u" {
				t.Fatalf("url 未透传: %v", seg.Data)
			}
		})
	}
}

func TestBuildMessageSegments(t *testing.T) {
	segs := buildMessageSegments("hello", []qqbot.MessageAttachment{
		{ContentType: "file", Filename: "a.jpg", URL: "u"},
	}, 100)

	if len(segs) != 3 {
		t.Fatalf("期望 3 段, 实际 %d", len(segs))
	}
	if segs[0].Type != "at" || segs[0].Data["qq"] != "100" {
		t.Fatalf("首段应为 at, 实际 %v", segs[0])
	}
	if segs[1].Type != "text" || segs[1].Data["text"] != "hello" {
		t.Fatalf("第二段应为 text, 实际 %v", segs[1])
	}
	if segs[2].Type != "image" {
		t.Fatalf("第三段应为 image, 实际 %v", segs[2])
	}
}
