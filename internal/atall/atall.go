// Package atall 通过备用 OneBot 实现补发 @全体
// 主端(QQ 官方 API)因无管理员权限或当日已达上限发不出 @全体 时用到。
// 只实现「发一条 @全体」这一个能力, 不做转发, 避免长成第二个 onebot 服务端。
package atall

import (
	"context"
	"fmt"
	"strings"
	"time"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/driver"
	"github.com/wdvxdr1123/ZeroBot/message"

	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
)

// requestTimeout 备用端请求超时
const requestTimeout = 10 * time.Second

// Client 备用 OneBot 端的 HTTP 调用器
type Client struct {
	caller *driver.HTTPCaller
}

// Load 按记录 id 取备用端配置, 未配置或地址为空时返回 nil
func Load(app core.App, id string) (*Client, error) {
	if id == "" {
		return nil, nil
	}
	rec, err := app.FindRecordById(db.TableAtFallback, id)
	if err != nil {
		return nil, fmt.Errorf("查询 @全体 备用端配置失败: %w", err)
	}
	base := strings.TrimRight(strings.TrimSpace(rec.GetString("base_url")), "/")
	if base == "" {
		return nil, nil
	}
	return &Client{caller: &driver.HTTPCaller{
		URL:         base,
		AccessToken: strings.TrimSpace(rec.GetString("access_token")),
	}}, nil
}

// SendAtAll 让备用端在该群发一条只含 @全体 的消息
func (c *Client) SendAtAll(ctx context.Context, groupNumber int64) error {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	rsp, err := c.caller.CallAPI(reqCtx, zero.APIRequest{
		Action: "send_group_msg",
		Params: zero.Params{
			"group_id": groupNumber,
			"message":  message.Message{message.AtAll(), message.Text(" ")},
		},
	})
	if err != nil {
		return fmt.Errorf("调用备用端失败: %w", err)
	}
	// OneBot 用 status 表示成败(ok / async / failed), retcode 只是错误码
	if rsp.Status != "ok" && rsp.Status != "async" {
		return fmt.Errorf("备用端返回失败: status=%s retcode=%d message=%s wording=%s",
			rsp.Status, rsp.RetCode, rsp.Message, rsp.Wording)
	}
	return nil
}
