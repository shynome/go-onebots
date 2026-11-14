package db

const (
	// TableQQBots 机器人配置
	TableQQBots = "qq_bots"
	// TableIDMap openid <-> int64 映射 (self / user / group)
	TableIDMap = "onebot_id_map"
	// TableMessageMap 消息 id 映射 (数量大, 独立存储)
	TableMessageMap = "onebot_message_map"
	// TableEvents OneBot 事件日志, 用于离线补发与后台查看
	TableEvents = "onebot_events"
	// TableMedia 富媒体中转记录
	TableMedia = "onebot_media"
)
