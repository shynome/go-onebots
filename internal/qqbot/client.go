package qqbot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultEndpoint QQ 开放平台 API 地址
	DefaultEndpoint = "https://api.bot.qq.com"
	// SandboxEndpoint 沙箱环境 API 地址
	SandboxEndpoint = "https://sandbox.api.sgroup.qq.com"
	// tokenEndpoint 获取 AppAccessToken 的地址, 与 API 域名无关
	tokenEndpoint = "https://bots.qq.com/app/getAppAccessToken"

	// tokenRefreshAhead 提前刷新的时间, 避免临界点过期
	tokenRefreshAhead = 60 * time.Second
)

// SessionStore 持久化网关会话与 seq, 进程重启后仍能 resume
// 实现应允许并发调用, 且不得因失败阻断事件处理
type SessionStore interface {
	LoadSession() (sessionID string, seq uint64)
	SaveSession(sessionID string, seq uint64)
}

type Config struct {
	AppID        string
	AppSecret    string
	Endpoint     string
	HTTPClient   *http.Client
	SessionStore SessionStore
}

func (c Config) endpoint() string {
	if c.Endpoint != "" {
		return strings.TrimSuffix(c.Endpoint, "/")
	}
	return DefaultEndpoint
}

func (c Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

type Client struct {
	cfg Config

	tokenMu     sync.Mutex
	token       string
	tokenExpire time.Time

	sessMu    sync.Mutex
	sessionID string
	seq       uint64
	loaded    bool
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) AppID() string { return c.cfg.AppID }

// AccessToken 返回有效的 AppAccessToken, 过期前会自动刷新
func (c *Client) AccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpire) {
		return c.token, nil
	}

	token, ttl, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}
	c.token = token
	c.tokenExpire = time.Now().Add(ttl - tokenRefreshAhead)
	return token, nil
}

func (c *Client) fetchToken(ctx context.Context) (string, time.Duration, error) {
	body, err := json.Marshal(map[string]string{
		"appId":        c.cfg.AppID,
		"clientSecret": c.cfg.AppSecret,
	})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.cfg.httpClient().Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, &APIError{
			StatusCode: resp.StatusCode,
			Method:     req.Method,
			URL:        req.URL.String(),
			Body:       string(raw),
		}
	}

	var data struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, fmt.Errorf("解析 token 响应失败: %w, body: %s", err, raw)
	}
	if data.AccessToken == "" {
		return "", 0, fmt.Errorf("token 响应中没有 access_token, body: %s", raw)
	}

	ttl := 7200 * time.Second
	if n, err := strconv.ParseInt(data.ExpiresIn.String(), 10, 64); err == nil && n > 0 {
		ttl = time.Duration(n) * time.Second
	}
	return data.AccessToken, ttl, nil
}

// APIError QQ 开放平台返回的非 2xx 响应
type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("qq api %s %s 返回 %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// Call 发起一次 REST 调用, 自动携带鉴权头
func (c *Client) Call(ctx context.Context, method, path string, body any, out any) error {
	link := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u, err := url.JoinPath(c.cfg.endpoint(), path)
		if err != nil {
			return err
		}
		link = u
	}

	var reqBody io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, link, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	token, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "QQBot "+token)

	resp, err := c.cfg.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Method:     req.Method,
			URL:        req.URL.String(),
			Body:       string(raw),
		}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("解析响应失败: %w, body: %s", err, raw)
	}
	return nil
}

// ClearSession 丢弃当前 ws 会话, 下次连接走 identify 而不是 resume
func (c *Client) ClearSession() {
	c.sessMu.Lock()
	c.sessionID = ""
	c.seq = 0
	c.loaded = true
	c.sessMu.Unlock()
	c.persist("", 0)
}

func (c *Client) session() (string, uint64) {
	c.sessMu.Lock()
	defer c.sessMu.Unlock()

	if !c.loaded {
		c.loaded = true
		if c.cfg.SessionStore != nil {
			c.sessionID, c.seq = c.cfg.SessionStore.LoadSession()
		}
	}
	return c.sessionID, c.seq
}

func (c *Client) saveSession(sessionID string) {
	c.sessMu.Lock()
	c.sessionID = sessionID
	seq := c.seq
	c.sessMu.Unlock()
	c.persist(sessionID, seq)
}

func (c *Client) saveSeq(seq uint64) {
	if seq == 0 {
		return
	}
	c.sessMu.Lock()
	c.seq = seq
	sessionID := c.sessionID
	c.sessMu.Unlock()
	c.persist(sessionID, seq)
}

func (c *Client) persist(sessionID string, seq uint64) {
	if c.cfg.SessionStore == nil {
		return
	}
	c.cfg.SessionStore.SaveSession(sessionID, seq)
}
