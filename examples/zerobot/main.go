// ZeroBot 冒烟测试: 群里 @机器人 发 "/hello" 或私聊发 "/hello"
//
// 默认走 WebSocket; ONEBOT_MODE=http 时改走 HTTP driver:
// action 走 HTTP 打到服务端, 事件需要服务端 POST 到 ONEBOT_HTTP_LISTEN
package main

import (
	"os"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
	"github.com/wdvxdr1123/ZeroBot/message"
)

func main() {
	zero.OnCommand("hello").Handle(func(ctx *zero.Ctx) {
		ctx.Send("world")
	})

	// 本地文件 -> S3 中转 -> 预签名 URL
	zero.OnCommand("pic").Handle(func(ctx *zero.Ctx) {
		ctx.Send(message.Message{
			message.Image("file:///tmp/onebots-test.png"),
		})
	})

	// 公网 URL -> 直接透传给 QQ
	zero.OnCommand("picurl").Handle(func(ctx *zero.Ctx) {
		ctx.Send(message.Message{
			message.Image("https://q1.qlogo.cn/g?b=qq&nk=10001&s=640"),
		})
	})

	// 发送后立即撤回, 验证 delete_msg
	zero.OnCommand("recall").Handle(func(ctx *zero.Ctx) {
		mid := ctx.Send("这条消息会被立刻撤回")
		ctx.DeleteMessage(mid)
	})

	// @全体成员, QQ 限制每群每天 1 次
	zero.OnCommand("atall").Handle(func(ctx *zero.Ctx) {
		ctx.Send("[CQ:at,qq=all] 全体成员测试")
	})

	zero.RunAndBlock(&zero.Config{
		NickName:      []string{"bot"},
		CommandPrefix: "/",
		Driver: []zero.Driver{
			newDriver(),
		},
	}, nil)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newDriver ONEBOT_MODE=http 时用 HTTP driver
// 注意: 该 driver 不连 WS, 事件需要服务端主动 POST 到 ONEBOT_HTTP_LISTEN
func newDriver() zero.Driver {
	if os.Getenv("ONEBOT_MODE") == "http" {
		return driver.NewHTTPClient(
			env("ONEBOT_HTTP_LISTEN", "127.0.0.1:8092"),
			os.Getenv("ONEBOT_HTTP_SECRET"),
			env("ONEBOT_HTTP_API", "http://127.0.0.1:8090/onebot/v11/<botId>"),
			os.Getenv("ONEBOT_TOKEN"),
		)
	}
	return driver.NewWebSocketClient(
		env("ONEBOT_WS", "ws://127.0.0.1:8090/onebot/v11/<botId>/ws"),
		os.Getenv("ONEBOT_TOKEN"),
	)
}
