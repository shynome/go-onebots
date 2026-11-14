// Package atall 通过备用 OneBot 实现补发 @全体
// 主端(QQ 官方 API)因无管理员权限或当日已达上限发不出 @全体 时用到。
// 只实现「发一条 @全体」这一个能力, 不做转发, 避免长成第二个 onebot 服务端。
package atall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
)

// requestTimeout 备用端请求超时
const requestTimeout = 10 * time.Second

// Client 备用 OneBot 端的 HTTP 客户端
type Client struct {
	baseURL string
	token   string
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
	return &Client{baseURL: base, token: strings.TrimSpace(rec.GetString("access_token"))}, nil
}

type segment struct {
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}

type sendParams struct {
	GroupID int64     `json:"group_id"`
	Message []segment `json:"message"`
}

// SendAtAll 让备用端在该群发一条只含 @全体 的消息
func (c *Client) SendAtAll(ctx context.Context, groupNumber int64) error {
	raw, err := json.Marshal(sendParams{
		GroupID: groupNumber,
		Message: []segment{
			{Type: "at", Data: map[string]string{"qq": "all"}},
			{Type: "text", Data: map[string]string{"text": " "}},
		},
	})
	if err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/send_group_msg", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("备用端返回 %d: %s", resp.StatusCode, body)
	}

	var res struct {
		Status  string `json:"status"`
		Retcode int    `json:"retcode"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("解析备用端响应失败: %w, body: %s", err, body)
	}
	if res.Retcode != 0 {
		return fmt.Errorf("备用端返回失败: retcode=%d message=%s", res.Retcode, res.Message)
	}
	return nil
}
