package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// MsgType 消息类型
type MsgType int

const (
	MsgTypeText     MsgType = 0 // 纯文本
	MsgTypeMarkdown MsgType = 2 // markdown(图文混排)
	MsgTypeMedia    MsgType = 7 // 富媒体
)

// Media 富媒体内容, 由上传接口返回的 file_info 构造
type Media struct {
	FileInfo string `json:"file_info"`
}

// Markdown markdown 消息内容
// 图片用 ![text #宽px #高px](url) 引用公网资源, 开放平台会自行下载转存
type Markdown struct {
	Content string `json:"content,omitempty"`
}

// MessageReference 引用回复的目标消息
// 注意: message_id 填的是入站事件 message_scene.ext 里的 msg_idx(REFIDX_...),
// 不是 message_id, 也不是被动回复用的 msg_id
type MessageReference struct {
	MessageID string `json:"message_id"`
}

// SendMessageRequest 发送消息请求体
// https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/send-receive/send.html
type SendMessageRequest struct {
	Content string  `json:"content,omitempty"`
	MsgType MsgType `json:"msg_type"`
	// Markdown msg_type=2 时使用, 可图文混排并控制顺序
	Markdown *Markdown `json:"markdown,omitempty"`
	// Keyboard 内嵌键盘(互动按钮), 原样透传, 通常与 markdown 一起发送
	// https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html#schema-keyboard
	Keyboard json.RawMessage `json:"keyboard,omitempty"`
	// Media msg_type=7 时使用的富媒体内容
	Media *Media `json:"media,omitempty"`
	// MessageReference 引用回复
	MessageReference *MessageReference `json:"message_reference,omitempty"`
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
