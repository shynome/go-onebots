package qqbot

import (
	"context"
	"net/http"
	"net/url"
)

// BotState 机器人在群内的状态
// 参考 https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_bot_state.get.html
type BotState struct {
	MemberOpenID      string `json:"member_openid"`
	JoinedAt          string `json:"joined_at"`
	AllowProactiveMsg bool   `json:"allow_proactive_msg"`
	RecvMsgSetting    string `json:"recv_msg_setting"`
	MemberRole        string `json:"member_role"`
}

// BotState 查询机器人在指定群的状态
// 该接口只对白名单机器人开放, 无权限时返回 11253
func (c *Client) BotState(ctx context.Context, groupOpenID string) (*BotState, error) {
	var st BotState
	path := "/v2/groups/" + url.PathEscape(groupOpenID) + "/bot_state"
	if err := c.Call(ctx, http.MethodGet, path, nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
