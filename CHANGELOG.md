# Changelog

## [Unreleased]

### 新增

- OneBot v11 HTTP API：`POST /onebot/v11/{botId}/{action}`，与 WebSocket 共用同一套 action 实现
- 认证支持 `Authorization: Bearer` 与 `?access_token=` 两种写法，WS 与 HTTP 统一校验
- `examples/zerobot` 支持 `ONEBOT_MODE=http` 切换到 HTTP driver（仅 action，事件上报暂未实现）

## [0.1.0] - 2026-11-14

首个可用版本：把 QQ 官方机器人包装成 OneBot v11 WebSocket 服务端，可直接接入 ZeroBot。

### 功能

- QQ 官方后端：AppID/AppSecret 鉴权、access token 自动刷新、网关 heartbeat / identify / resume
- OneBot v11 正向 WebSocket：握手帧 `{"self_id": <int64>}`、`echo` 原样返回、每个 bot 独立端点
- 消息
  - 私聊（C2C）与群聊（@ 消息 + 全量消息）
  - 真实昵称、群名片、群角色，入站附件（图片 / 语音 / 视频 / 文件）转对应消息段
  - 发送文本与富媒体，自动作为被动回复（msg_id + msg_seq，5 分钟 / 最多 5 条）
  - `@全体成员`：content 写 `<qqbot-at-everyone />`，需机器人为群管理员，每群每天 1 次
- 可靠性
  - 入站事件落库 `onebot_events`，客户端掉线后重连自动补发（30 分钟 / 2000 条）
  - 出站消息先写 `onebot_outbox` 再发送，网络抖动退避重试，进程崩溃后由定时任务补发
  - 网关 session / seq 持久化，重启走 resume 不丢消息
  - 事件表与中转媒体按 PocketBase `Settings → Logs → Max days` 定期清理
- API
  - `get_login_info` / `get_status` / `get_version_info`
  - `send_private_msg` / `send_group_msg` / `send_msg`
  - `get_msg` / `get_group_msg_history` / `delete_msg`
  - `get_stranger_info` / `get_group_member_info` / `get_group_info` / `get_group_list`
  - `get_friend_list` / `get_group_member_list` 返回空数组（官方无对应接口）
- 运维
  - PocketBase 后台管理机器人配置与全部数据表
  - `make build` / `make docker` / `make push`
  - `cmd/obtest` 冒烟测试客户端，`examples/zerobot` ZeroBot 接入示例

### 已知限制

- 单人 `@` 无官方标记格式，出站会跳过并告警
- 群名、群成员列表、成员入群时间、群管操作（禁言 / 踢人 / 改名片）、合并转发均无官方接口
- 图片、视频等媒体需公网可访问，QQ 侧会自行拉取
- 发送属 at-least-once：QQ 已收到但 HTTP 响应超时的情况下，补发可能重复
