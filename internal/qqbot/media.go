package qqbot

import (
	"context"
	"fmt"
	"net/http"
)

// FileType 富媒体类型
// https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/send-receive/send.html
type FileType int

const (
	FileTypeImage FileType = 1 // 图片 png/jpg
	FileTypeVideo FileType = 2 // 视频 mp4
	FileTypeAudio FileType = 3 // 语音 silk
	FileTypeRaw   FileType = 4 // 文件
)

// UploadRequest 上传富媒体
type UploadRequest struct {
	FileType   FileType `json:"file_type"`
	URL        string   `json:"url,omitempty"`
	FileData   string   `json:"file_data,omitempty"`
	SrvSendMsg bool     `json:"srv_send_msg"`
}

type UploadResult struct {
	FileUUID string `json:"file_uuid"`
	FileInfo string `json:"file_info"`
	TTL      int    `json:"ttl"`
}

// UploadGroupMedia 上传群聊富媒体, 返回的 FileInfo 用于 msg_type=7 发送
func (c *Client) UploadGroupMedia(ctx context.Context, groupOpenID string, req UploadRequest) (UploadResult, error) {
	if groupOpenID == "" {
		return UploadResult{}, fmt.Errorf("群 openid 为空")
	}
	var res UploadResult
	path := fmt.Sprintf("/v2/groups/%s/files", groupOpenID)
	if err := c.Call(ctx, http.MethodPost, path, req, &res); err != nil {
		return UploadResult{}, err
	}
	if res.FileInfo == "" {
		return UploadResult{}, fmt.Errorf("上传成功但未返回 file_info")
	}
	return res, nil
}

// UploadUserMedia 上传单聊富媒体
func (c *Client) UploadUserMedia(ctx context.Context, userOpenID string, req UploadRequest) (UploadResult, error) {
	if userOpenID == "" {
		return UploadResult{}, fmt.Errorf("用户 openid 为空")
	}
	var res UploadResult
	path := fmt.Sprintf("/v2/users/%s/files", userOpenID)
	if err := c.Call(ctx, http.MethodPost, path, req, &res); err != nil {
		return UploadResult{}, err
	}
	if res.FileInfo == "" {
		return UploadResult{}, fmt.Errorf("上传成功但未返回 file_info")
	}
	return res, nil
}
