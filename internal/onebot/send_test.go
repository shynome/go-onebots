package onebot

import (
	"encoding/json"
	"testing"
)

func TestParseCQString(t *testing.T) {
	segs := parseCQString("hi[CQ:image,file=https://a/b.png] there")

	if len(segs) != 3 {
		t.Fatalf("期望 3 段, 实际 %d: %+v", len(segs), segs)
	}
	if segs[0].Type != "text" || segs[0].Data["text"] != "hi" {
		t.Fatalf("首段应为文本 hi: %+v", segs[0])
	}
	if segs[1].Type != "image" || segs[1].Data["file"] != "https://a/b.png" {
		t.Fatalf("第二段应为图片: %+v", segs[1])
	}
	if segs[2].Type != "text" || segs[2].Data["text"] != "there" {
		t.Fatalf("末段应为文本 there: %+v", segs[2])
	}
}

func TestParseCQEscaped(t *testing.T) {
	segs := parseCQString("a&#91;b&#93;[CQ:at,qq=123]")
	if len(segs) != 2 {
		t.Fatalf("期望 2 段, 实际 %+v", segs)
	}
	if segs[0].Data["text"] != "a[b]" {
		t.Fatalf("转义未还原: %+v", segs[0])
	}
	if segs[1].Type != "at" || segs[1].Data["qq"] != "123" {
		t.Fatalf("at 段解析错误: %+v", segs[1])
	}
}

func TestParseSegmentsArray(t *testing.T) {
	raw := json.RawMessage(`[{"type":"image","data":{"file":"base64://abc"}},{"type":"text","data":{"text":"x"}}]`)
	segs, err := ParseSegments(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || segs[0].Type != "image" {
		t.Fatalf("解析错误: %+v", segs)
	}
}

func TestBuildOutbounds(t *testing.T) {
	c := &BotContext{}

	t.Run("纯文本", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{textSegment("hello")}, "group")
		if len(out) != 1 || out[0].media != nil || out[0].content != "hello" {
			t.Fatalf("解析错误: %+v", out)
		}
	})

	t.Run("文本加一张图", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("看图"),
			{Type: "image", Data: map[string]string{"file": "u"}},
		}, "group")
		if len(out) != 1 {
			t.Fatalf("应合并成 1 条, 实际 %d", len(out))
		}
		if out[0].content != "看图" || out[0].media == nil || out[0].media.kind != "image" {
			t.Fatalf("解析错误: %+v", out[0])
		}
	})

	t.Run("两张图拆两条", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "image", Data: map[string]string{"file": "a"}},
			{Type: "image", Data: map[string]string{"file": "b"}},
		}, "group")
		if len(out) != 2 {
			t.Fatalf("应拆成 2 条, 实际 %d", len(out))
		}
		if out[0].content != "t" || out[0].media.file != "a" {
			t.Fatalf("第一条错误: %+v", out[0])
		}
		if out[1].content != "" || out[1].media.file != "b" {
			t.Fatalf("第二条错误: %+v", out[1])
		}
	})

	t.Run("超出被动回复上限截断", func(t *testing.T) {
		segs := []Segment{textSegment("t")}
		for i := 0; i < 7; i++ {
			segs = append(segs, Segment{Type: "image", Data: map[string]string{"file": "u"}})
		}
		out, skipped := c.buildOutbounds(segs, "group")
		if len(out) != maxOutbounds {
			t.Fatalf("应截断到 %d 条, 实际 %d", maxOutbounds, len(out))
		}
		if len(skipped) == 0 {
			t.Fatal("应记录被截断的段")
		}
	})

	t.Run("忽略不支持的段", func(t *testing.T) {
		c := &BotContext{}
		out, skipped := c.buildOutbounds([]Segment{
			{Type: "face", Data: map[string]string{"id": "1"}},
			textSegment("hi"),
		}, "group")
		if len(out) != 1 || out[0].content != "hi" {
			t.Fatalf("解析错误: %+v", out)
		}
		if len(skipped) != 1 || skipped[0] != "face" {
			t.Fatalf("应记录 face 被跳过: %+v", skipped)
		}
	})
}
