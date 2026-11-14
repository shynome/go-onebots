package qqbot

import (
	"context"
	"fmt"
	"net/http"
)

// MsgType 消息类型
type MsgType int

const (
	MsgTypeText MsgType = 0 // 纯文本
)

// SendMessageRequest 发送消息请求体
// https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/send-receive/send.html
type SendMessageRequest struct {
	Content string  `json:"content"`
	MsgType MsgType `json:"msg_type"`
	// MsgID 被动回复所对应的事件消息 id, 5 分钟内有效
	MsgID string `json:"msg_id,omitempty"`
	// MsgSeq 被动回复序号, 同一条被动消息最多 5 次
	MsgSeq int `json:"msg_seq,omitempty"`
	// EventID 互动事件 id, 与 MsgID 二选一
	EventID string `json:"event_id,omitempty"`
}

type SendResult struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

// SendGroupMessage 发送群聊消息
func (c *Client) SendGroupMessage(ctx context.Context, groupOpenID string, req SendMessageRequest) (SendResult, error) {
	if groupOpenID == "" {
		return SendResult{}, fmt.Errorf("群 openid 为空")
	}
	var res SendResult
	path := fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
	if err := c.Call(ctx, http.MethodPost, path, req, &res); err != nil {
		return SendResult{}, err
	}
	return res, nil
}

// SendUserMessage 发送单聊消息
func (c *Client) SendUserMessage(ctx context.Context, userOpenID string, req SendMessageRequest) (SendResult, error) {
	if userOpenID == "" {
		return SendResult{}, fmt.Errorf("用户 openid 为空")
	}
	var res SendResult
	path := fmt.Sprintf("/v2/users/%s/messages", userOpenID)
	if err := c.Call(ctx, http.MethodPost, path, req, &res); err != nil {
		return SendResult{}, err
	}
	return res, nil
}

// Me 获取当前机器人信息
func (c *Client) Me(ctx context.Context) (User, error) {
	var user User
	if err := c.Call(ctx, http.MethodGet, "/users/@me", nil, &user); err != nil {
		return User{}, err
	}
	return user, nil
}
