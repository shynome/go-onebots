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

	t.Run("纯文本也走 markdown", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{textSegment("hello")}, "group")
		if len(out) != 1 || out[0].media != nil {
			t.Fatalf("解析错误: %+v", out)
		}
		if len(out[0].segments) != 1 || out[0].segments[0].Data["text"] != "hello" {
			t.Fatalf("纯文本应转成 markdown 段: %+v", out[0])
		}
	})

	t.Run("文本加一张图转 markdown", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("看图"),
			{Type: "image", Data: map[string]string{"file": "u"}},
		}, "group")
		if len(out) != 1 {
			t.Fatalf("应合并成 1 条, 实际 %d", len(out))
		}
		// 文本 + 图片走 markdown, 才能控制图片在文字之后
		if len(out[0].segments) != 2 || out[0].segments[0].Type != "text" {
			t.Fatalf("应转成 markdown 段: %+v", out[0])
		}
	})

	t.Run("文本加两张图合成一条 markdown", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "image", Data: map[string]string{"file": "a"}},
			{Type: "image", Data: map[string]string{"file": "b"}},
		}, "group")
		if len(out) != 1 || len(out[0].segments) != 3 {
			t.Fatalf("应合成 1 条 3 段 markdown, 实际 %+v", out)
		}
	})

	t.Run("纯图片多张合成一条 markdown", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			{Type: "image", Data: map[string]string{"file": "a"}},
			{Type: "image", Data: map[string]string{"file": "b"}},
		}, "group")
		if len(out) != 1 {
			t.Fatalf("应合成 1 条, 实际 %d", len(out))
		}
		if len(out[0].segments) != 2 || out[0].media != nil {
			t.Fatalf("应转成 2 段 markdown: %+v", out[0])
		}
		if out[0].segments[0].Data["file"] != "a" || out[0].segments[1].Data["file"] != "b" {
			t.Fatalf("顺序错误: %+v", out)
		}
	})

	t.Run("纯图片多张不受被动回复条数限制", func(t *testing.T) {
		segs := []Segment{}
		for i := 0; i < 7; i++ {
			segs = append(segs, Segment{Type: "image", Data: map[string]string{"file": "u"}})
		}
		out, skipped := c.buildOutbounds(segs, "group")
		if len(out) != 1 || len(out[0].segments) != 7 {
			t.Fatalf("应合成 1 条 7 段 markdown, 实际 %+v", out)
		}
		if len(skipped) != 0 {
			t.Fatalf("不应丢弃任何段: %+v", skipped)
		}
	})

	t.Run("文本加视频超出上限截断", func(t *testing.T) {
		segs := []Segment{textSegment("t")}
		for i := 0; i < 7; i++ {
			segs = append(segs, Segment{Type: "video", Data: map[string]string{"file": "v"}})
		}
		out, skipped := c.buildOutbounds(segs, "group")
		if len(out) != maxOutbounds {
			t.Fatalf("应截断到 %d 条, 实际 %d", maxOutbounds, len(out))
		}
		if len(skipped) == 0 {
			t.Fatal("应记录被截断的段")
		}
	})

	t.Run("混合非图片媒体不转 markdown", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "video", Data: map[string]string{"file": "v"}},
		}, "group")
		if len(out) != 1 || out[0].media == nil {
			t.Fatalf("含视频应走富媒体: %+v", out)
		}
		if len(out[0].segments) != 0 {
			t.Fatal("不应转成 markdown")
		}
	})

	t.Run("忽略不支持的段", func(t *testing.T) {
		c := &BotContext{}
		out, skipped := c.buildOutbounds([]Segment{
			{Type: "face", Data: map[string]string{"id": "1"}},
			textSegment("hi"),
		}, "group")
		if len(out) != 1 || len(out[0].segments) != 1 || out[0].segments[0].Type != "text" {
			t.Fatalf("解析错误: %+v", out)
		}
		if len(skipped) != 1 || skipped[0] != "face" {
			t.Fatalf("应记录 face 被跳过: %+v", skipped)
		}
	})

	t.Run("带键盘的文本走 markdown", func(t *testing.T) {
		kb := `{"content":{"rows":[{"buttons":[{"id":"signin","action":{"type":2,"permission":{"type":1},"data":"/签到"}}]}]}}`
		out, skipped := c.buildOutbounds([]Segment{
			textSegment("点按钮签到"),
			{Type: "keyboard", Data: map[string]string{"content": kb}},
		}, "group")
		if len(skipped) != 0 {
			t.Fatalf("不应跳过任何段: %+v", skipped)
		}
		if len(out) != 1 {
			t.Fatalf("应合成 1 条, 实际 %d", len(out))
		}
		if string(out[0].keyboard) != kb {
			t.Fatalf("键盘应原样透传: %s", out[0].keyboard)
		}
		// 按钮必须挂在 markdown 消息上
		if out[0].markdown != "" || len(out[0].segments) != 1 || out[0].segments[0].Type != "text" {
			t.Fatalf("应走 markdown 段: %+v", out[0])
		}
	})

	t.Run("键盘只给按钮行数组时自动包外壳", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "keyboard", Data: map[string]string{"content": `[{"buttons":[]}]`}},
		}, "group")
		if len(out) != 1 {
			t.Fatalf("应合成 1 条, 实际 %d", len(out))
		}
		var kb struct {
			Content struct {
				Rows []map[string]any `json:"rows"`
			} `json:"content"`
		}
		if err := json.Unmarshal(out[0].keyboard, &kb); err != nil {
			t.Fatal(err)
		}
		if len(kb.Content.Rows) != 1 {
			t.Fatalf("应包装成 content.rows: %s", out[0].keyboard)
		}
	})

	t.Run("键盘模板 id 直接成对", func(t *testing.T) {
		out, _ := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "keyboard", Data: map[string]string{"id": "kb_1"}},
		}, "group")
		if len(out) != 1 || string(out[0].keyboard) != `{"id":"kb_1"}` {
			t.Fatalf("解析错误: %+v", out)
		}
	})

	t.Run("没有文本时键盘被跳过", func(t *testing.T) {
		out, skipped := c.buildOutbounds([]Segment{
			{Type: "keyboard", Data: map[string]string{"content": `{"content":{"rows":[]}}`}},
		}, "group")
		if len(out) != 0 {
			t.Fatalf("没有文本不应发送: %+v", out)
		}
		if len(skipped) != 1 || skipped[0] != "keyboard(需要同时有文本内容)" {
			t.Fatalf("应记录跳过原因: %+v", skipped)
		}
	})

	t.Run("键盘与视频同发时跳过键盘", func(t *testing.T) {
		out, skipped := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "video", Data: map[string]string{"file": "v"}},
			{Type: "keyboard", Data: map[string]string{"content": `{"content":{"rows":[]}}`}},
		}, "group")
		if len(out) != 1 || out[0].media == nil {
			t.Fatalf("应照常发视频: %+v", out)
		}
		if len(skipped) != 1 || skipped[0] != "keyboard(不能与语音/视频/文件同发)" {
			t.Fatalf("应记录跳过原因: %+v", skipped)
		}
	})

	t.Run("键盘 JSON 非法被跳过", func(t *testing.T) {
		out, skipped := c.buildOutbounds([]Segment{
			textSegment("t"),
			{Type: "keyboard", Data: map[string]string{"content": "{oops"}},
		}, "group")
		if len(out) != 1 || len(out[0].segments) != 1 || out[0].segments[0].Type != "text" {
			t.Fatalf("文本应照常发送: %+v", out)
		}
		if len(skipped) != 1 || skipped[0] != "keyboard(content 不是合法 JSON)" {
			t.Fatalf("应记录跳过原因: %+v", skipped)
		}
	})
}
