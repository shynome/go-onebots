// 模拟 OneBot v11 客户端, 用于本地冒烟测试
// go run ./cmd/obtest -url ws://127.0.0.1:8090/onebot/v11/<botId>/ws -token xxx
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func main() {
	url := flag.String("url", "", "ws 地址")
	token := flag.String("token", "", "onebot_token")
	action := flag.String("action", "get_login_info", "要调用的 action")
	params := flag.String("params", "{}", "action 参数, JSON")
	flag.Parse()

	if *url == "" {
		fmt.Println("缺少 -url")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{}
	if *token != "" {
		opts.HTTPHeader = map[string][]string{"Authorization": {"Bearer " + *token}}
	}
	conn, _, err := websocket.Dial(ctx, *url, opts)
	if err != nil {
		fmt.Println("连接失败:", err)
		os.Exit(1)
	}
	defer conn.CloseNow()

	var handshake map[string]any
	if err := wsjson.Read(ctx, conn, &handshake); err != nil {
		fmt.Println("读取握手包失败:", err)
		os.Exit(1)
	}
	fmt.Println("握手包:", mustJSON(handshake))

	var rawParams json.RawMessage = json.RawMessage(*params)
	req := map[string]any{
		"action": *action,
		"params": rawParams,
		"echo":   1,
	}
	if err := wsjson.Write(ctx, conn, req); err != nil {
		fmt.Println("发送请求失败:", err)
		os.Exit(1)
	}

	for {
		var resp map[string]any
		if err := wsjson.Read(ctx, conn, &resp); err != nil {
			fmt.Println("读取响应失败:", err)
			os.Exit(1)
		}
		if echo, ok := resp["echo"]; !ok || fmt.Sprint(echo) != "1" {
			fmt.Println("收到事件:", mustJSON(resp))
			continue
		}
		fmt.Println("响应:", mustJSON(resp))
		return
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
