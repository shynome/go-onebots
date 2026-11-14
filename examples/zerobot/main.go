// ZeroBot 冒烟测试: 群里 @机器人 发 "/hello" 或私聊发 "/hello"
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

	url := os.Getenv("ONEBOT_WS")
	if url == "" {
		url = "ws://127.0.0.1:8090/onebot/v11/<botId>/ws"
	}

	zero.RunAndBlock(&zero.Config{
		NickName:      []string{"bot"},
		CommandPrefix: "/",
		Driver: []zero.Driver{
			driver.NewWebSocketClient(url, os.Getenv("ONEBOT_TOKEN")),
		},
	}, nil)
}
