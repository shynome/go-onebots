// ZeroBot 冒烟测试: 群里 @机器人 发 "/hello" 或私聊发 "/hello"
package main

import (
	"os"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
)

func main() {
	zero.OnCommand("hello").Handle(func(ctx *zero.Ctx) {
		ctx.Send("world")
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
