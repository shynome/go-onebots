package qqbot

import (
	"encoding/json"
)

// OpCode ws 指令
// https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/event-emit.html
type OpCode int

const (
	OpDispatch     OpCode = 0  // 服务端推送事件
	OpHeartbeat    OpCode = 1  // 心跳
	OpIdentify     OpCode = 2  // 鉴权
	OpResume       OpCode = 6  // 恢复连接
	OpReconnect    OpCode = 7  // 服务端要求重连
	OpInvalid      OpCode = 9  // identify/resume 参数有误
	OpHello        OpCode = 10 // 连接建立后第一条消息, 含心跳间隔
	OpHeartbeatACK OpCode = 11 // 心跳回执
)

type Intent int

const (
	// IntentGroupAndC2C 群与单聊事件 (含 GROUP_AT_MESSAGE_CREATE / C2C_MESSAGE_CREATE)
	IntentGroupAndC2C Intent = 1 << 25
)

// Event 事件类型
type Event string

const (
	EventReady                Event = "READY"
	EventResumed              Event = "RESUMED"
	EventC2CMessageCreate     Event = "C2C_MESSAGE_CREATE"
	EventGroupATMessageCreate Event = "GROUP_AT_MESSAGE_CREATE"
)

// Payload 网关下行消息
type Payload struct {
	ID    string          `json:"id"`
	Op    OpCode          `json:"op"`
	Seq   uint64          `json:"s"`
	Event Event           `json:"t"`
	Data  json.RawMessage `json:"d"`
}

// ReadyData READY / RESUMED 事件内容
type ReadyData struct {
	Version   int    `json:"version"`
	SessionID string `json:"session_id"`
	User      User   `json:"user"`
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// Author 消息发送者, 群聊与单聊字段名不同
type Author struct {
	ID           string `json:"id"`
	UnionOpenID  string `json:"union_openid"`
	UserOpenID   string `json:"user_openid"`
	MemberOpenID string `json:"member_openid"`
}

func (a Author) OpenID() string {
	if a.MemberOpenID != "" {
		return a.MemberOpenID
	}
	if a.UserOpenID != "" {
		return a.UserOpenID
	}
	return a.ID
}

// C2CMessage 单聊消息
// https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/c2c_message_create.html
type C2CMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Author    Author `json:"author"`
}

// GroupATMessage 群 @ 机器人消息
// https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_at_message_create.html
type GroupATMessage struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	Timestamp   string `json:"timestamp"`
	GroupOpenID string `json:"group_openid"`
	Author      Author `json:"author"`
}
