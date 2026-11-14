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
	// EventGroupMessageCreate 群内全量消息, 需在开放平台开启 "接收所有消息"
	EventGroupMessageCreate Event = "GROUP_MESSAGE_CREATE"

	// 群管理事件, 四个事件的字段一致
	EventGroupAddRobot   Event = "GROUP_ADD_ROBOT"   // 机器人被加入群聊
	EventGroupDelRobot   Event = "GROUP_DEL_ROBOT"   // 机器人被移出群聊
	EventGroupMsgReceive Event = "GROUP_MSG_RECEIVE" // 群管理员开启通知(主动消息)
	EventGroupMsgReject  Event = "GROUP_MSG_REJECT"  // 群管理员关闭通知(主动消息)

	// 单聊管理事件
	EventC2CMsgReceive Event = "C2C_MSG_RECEIVE" // 用户开启主动消息推送开关
	EventC2CMsgReject  Event = "C2C_MSG_REJECT"  // 用户关闭主动消息推送开关
	EventFriendAdd     Event = "FRIEND_ADD"      // 用户添加机器人好友
	EventFriendDel     Event = "FRIEND_DEL"      // 用户删除机器人好友
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
	ID           string `json:"id"`
	Username     string `json:"username"`
	Bot          bool   `json:"bot"`
	MemberOpenID string `json:"member_openid"`
	UserOpenID   string `json:"user_openid"`
}

// Author 消息发送者, 群聊与单聊字段名不同
type Author struct {
	ID             string `json:"id"`
	UnionOpenID    string `json:"union_openid"`
	UserOpenID     string `json:"user_openid"`
	MemberOpenID   string `json:"member_openid"`
	Username       string `json:"username"`        // 账号昵称
	MemberNickname string `json:"member_nickname"` // 群昵称
	MemberRole     string `json:"member_role"`     // owner / admin / member
	Avatar         string `json:"avatar"`
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

// MessageAttachment 消息附件(图片/语音/视频/文件)
type MessageAttachment struct {
	URL          string `json:"url"`
	Filename     string `json:"filename"`
	Width        int64  `json:"width"`
	Height       int64  `json:"height"`
	Size         int64  `json:"size"`
	ContentType  string `json:"content_type"` // image/jpeg / voice / video/mp4 / file
	VoiceWavURL  string `json:"voice_wav_url"`
	AsrReferText string `json:"asr_refer_text"`
}

// GroupManageEvent 群管理事件事件体
// 事件: GROUP_ADD_ROBOT / GROUP_DEL_ROBOT / GROUP_MSG_RECEIVE / GROUP_MSG_REJECT
type GroupManageEvent struct {
	Timestamp      int64  `json:"timestamp"`
	GroupOpenID    string `json:"group_openid"`
	OpMemberOpenID string `json:"op_member_openid"`
}

// C2CManageEvent 单聊管理事件事件体
type C2CManageEvent struct {
	Timestamp int64  `json:"timestamp"`
	OpenID    string `json:"openid"`
}

// FriendEvent 好友事件事件体
type FriendEvent struct {
	Timestamp  int64  `json:"timestamp"`
	OpenID     string `json:"openid"`
	Scene      int    `json:"scene"`
	SceneParam string `json:"scene_param"`
}

// MessageScene 消息场景, ext 里带引用所需的 msg_idx
type MessageScene struct {
	Source string   `json:"source"`
	Ext    []string `json:"ext"`
}

// C2CMessage 单聊消息
// https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/c2c_message_create.html
type C2CMessage struct {
	ID           string              `json:"id"`
	Content      string              `json:"content"`
	Timestamp    string              `json:"timestamp"`
	Author       Author              `json:"author"`
	Attachments  []MessageAttachment `json:"attachments"`
	MessageScene MessageScene        `json:"message_scene"`
}

// GroupATMessage 群 @ 机器人消息
// https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_at_message_create.html
type GroupATMessage struct {
	ID           string              `json:"id"`
	Content      string              `json:"content"`
	Timestamp    string              `json:"timestamp"`
	GroupOpenID  string              `json:"group_openid"`
	Author       Author              `json:"author"`
	Attachments  []MessageAttachment `json:"attachments"`
	Mentions     []User              `json:"mentions"`
	MessageScene MessageScene        `json:"message_scene"`
}
