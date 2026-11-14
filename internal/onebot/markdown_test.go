package onebot

import "testing"

func TestEscapeMarkdown(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"普通文本", "普通文本"},
		{"1*2*3", `1\*2\*3`},
		{"# 不是标题", `\# 不是标题`},
		{"a_b_c", `a\_b\_c`},
		{"[不是链接]", `\[不是链接\]`},
		{"~~不是删除线~~", `\~\~不是删除线\~\~`},
		{"a|b", `a\|b`},
		{"> 不是引用", `\> 不是引用`},
		{"第一行\n第二行", "第一行\n第二行"},
		{"- 不是列表", `\- 不是列表`},
		{"A+B", "A+B"},
		{"`code`", "\\`code\\`"},
	}
	for _, c := range cases {
		if got := escapeMarkdown(c.in); got != c.want {
			t.Errorf("escapeMarkdown(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
