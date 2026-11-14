package qqbot

import (
	"context"
	"fmt"
	"net/http"
)

// DeleteGroupMessage 撤回群聊消息
// https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages_message_id.delete.html
func (c *Client) DeleteGroupMessage(ctx context.Context, groupOpenID, messageID string) error {
	if groupOpenID == "" || messageID == "" {
		return fmt.Errorf("群 openid 或消息 id 为空")
	}
	path := fmt.Sprintf("/v2/groups/%s/messages/%s", groupOpenID, messageID)
	return c.Call(ctx, http.MethodDelete, path, nil, nil)
}

// DeleteUserMessage 撤回单聊消息
func (c *Client) DeleteUserMessage(ctx context.Context, userOpenID, messageID string) error {
	if userOpenID == "" || messageID == "" {
		return fmt.Errorf("用户 openid 或消息 id 为空")
	}
	path := fmt.Sprintf("/v2/users/%s/messages/%s", userOpenID, messageID)
	return c.Call(ctx, http.MethodDelete, path, nil, nil)
}
