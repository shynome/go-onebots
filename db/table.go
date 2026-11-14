package db

const (
	// TableQQBots 机器人配置
	TableQQBots = "qq_bots"
	// TableIDMap openid <-> int64 映射
	TableIDMap = "onebot_id_map"
	// TableEvents OneBot 事件日志, 用于离线补发与后台查看
	TableEvents = "onebot_events"
)
