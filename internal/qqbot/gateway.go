package qqbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"sync"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type GatewayInfo struct {
	URL string `json:"url"`
}

// Gateway 获取 ws 网关地址
func (c *Client) Gateway(ctx context.Context) (GatewayInfo, error) {
	var info GatewayInfo
	if err := c.Call(ctx, http.MethodGet, "/gateway", nil, &info); err != nil {
		return GatewayInfo{}, err
	}
	if info.URL == "" {
		return GatewayInfo{}, errors.New("网关地址为空")
	}
	return info, nil
}

const (
	wsWriteTimeout   = 10 * time.Second
	defaultHeartbeat = 30 * time.Second
)

// Events 建立网关连接并持续推送事件, 内部负责心跳与断线重连(resume)
// 返回的 channel 在 ctx 结束后关闭
func (c *Client) Events(ctx context.Context, intents Intent) <-chan Payload {
	out := make(chan Payload, 64)
	go func() {
		defer close(out)
		// 无限重试, 指数退避 + 抖动, 单次等待最多 1s
		retry.New(
			retry.Context(ctx),
			retry.Attempts(0),
			retry.MaxDelay(time.Second),
			retry.OnRetry(func(n uint, err error) {
				if ctx.Err() != nil {
					// 主动关闭时不算异常退出, 不打重连日志
					return
				}
				c.logger().Warn("qq 网关连接结束, 准备重连", "error", err, "attempt", n+1)
			}),
		).Do(func() error {
			return c.runOnce(ctx, intents, out)
		})
	}()
	return out
}

func (c *Client) runOnce(ctx context.Context, intents Intent, out chan<- Payload) (err error) {
	info, err := c.Gateway(ctx)
	if err != nil {
		return fmt.Errorf("获取网关地址失败: %w", err)
	}

	conn, _, err := websocket.Dial(ctx, info.URL, nil)
	if err != nil {
		return fmt.Errorf("连接网关失败: %w", err)
	}
	defer conn.CloseNow()
	defer func() {
		var ce websocket.CloseError
		if errors.As(err, &ce) && (ce.Code == 4006 || ce.Code == 4009) {
			// 会话失效(如机器人被踢下线), 下次必须重新 identify
			c.ClearSession()
		}
	}()

	// op 10 hello
	helloCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	var hello Payload
	err = wsjson.Read(helloCtx, conn, &hello)
	cancel()
	if err != nil {
		return fmt.Errorf("读取 hello 失败: %w", err)
	}
	if hello.Op != OpHello {
		return fmt.Errorf("期望 op=%d hello, 实际 op=%d", OpHello, hello.Op)
	}
	var helloData struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.Data, &helloData); err != nil {
		return fmt.Errorf("解析 hello 失败: %w", err)
	}
	interval := time.Duration(helloData.HeartbeatInterval) * time.Millisecond
	if interval <= 0 {
		interval = defaultHeartbeat
	}

	// ws 写操作不是并发安全的, 统一串行化
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
		defer cancel()
		return wsjson.Write(wctx, conn, v)
	}

	token, err := c.AccessToken(ctx)
	if err != nil {
		return fmt.Errorf("获取 token 失败: %w", err)
	}
	authToken := "QQBot " + token

	sessionID, seq := c.session()
	if sessionID == "" {
		c.logger().Info("qq 网关 identify", "intents", intents)
		if err := write(map[string]any{
			"op": OpIdentify,
			"d": map[string]any{
				"token":   authToken,
				"intents": intents,
				"shard":   []int{0, 1},
				"properties": map[string]string{
					"$os":      "linux",
					"$browser": "go-onebots",
					"$device":  "go-onebots",
				},
			},
		}); err != nil {
			return fmt.Errorf("发送 identify 失败: %w", err)
		}
	} else {
		if seq > 5 {
			seq -= 5 // 多重放几条, 避免边界丢消息
		} else {
			seq = 0
		}
		c.logger().Info("qq 网关 resume", "session", sessionID, "seq", seq)
		if err := write(map[string]any{
			"op": OpResume,
			"d": map[string]any{
				"token":      authToken,
				"session_id": sessionID,
				"seq":        seq,
			},
		}); err != nil {
			return fmt.Errorf("发送 resume 失败: %w", err)
		}
	}

	// 心跳
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				_, seq := c.session()
				var d any
				if seq > 0 {
					d = seq
				}
				if err := write(map[string]any{"op": OpHeartbeat, "d": d}); err != nil {
					return
				}
			}
		}
	}()

	// 读循环
	for {
		var p Payload
		if err := wsjson.Read(ctx, conn, &p); err != nil {
			return fmt.Errorf("读取网关消息失败: %w", err)
		}
		switch p.Op {
		case OpDispatch:
			c.saveSeq(p.Seq)
			if p.Event == EventReady {
				var ready ReadyData
				if err := json.Unmarshal(p.Data, &ready); err == nil && ready.SessionID != "" {
					c.saveSession(ready.SessionID)
					c.logger().Info("qq 网关就绪", "bot", ready.User.Username, "openid", ready.User.ID)
				}
			}
			select {
			case out <- p:
			case <-ctx.Done():
				return ctx.Err()
			}
		case OpReconnect:
			return errors.New("服务端要求重新连接")
		case OpInvalid:
			c.ClearSession()
			return errors.New("identify/resume 参数有误, 已清除会话")
		case OpHeartbeatACK:
			// ignore
		}
	}
}
