package onebot

import "testing"

func TestNeedMirror(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		// 内网 / 本机地址: QQ 拉不到, 必须转存
		{"http://192.168.1.10/a.png", true},
		{"http://10.0.0.5/a.png", true},
		{"http://172.16.3.4/a.png", true},
		{"http://127.0.0.1:8080/a.png", true},
		{"http://169.254.1.1/a.png", true},
		{"http://0.0.0.0/a.png", true},
		{"http://[::1]/a.png", true},
		{"http://[fd00::1]/a.png", true},
		{"http://localhost:9000/a.png", true},
		{"http://nas/a.png", true},
		{"http://printer.local/a.png", true},

		// 公网地址: 直接透传
		{"https://example.com/a.png", false},
		{"https://q1.qlogo.cn/g?b=qq&nk=10001&s=640", false},
		{"http://8.8.8.8/a.png", false},
		{"http://172.32.0.1/a.png", false},
		{"http://[2400:3200::1]/a.png", false},

		{"", false},
		{"not a url", false},
	}

	for _, c := range cases {
		if got := needMirror(c.url); got != c.want {
			t.Errorf("needMirror(%q) = %v, 期望 %v", c.url, got, c.want)
		}
	}
}
