# Changelog

## [0.2.4] - 2026-11-14

### 修复

- 完整路径 `/onebot/v11/{botId}/{action}` 支持 GET（之前只注册了 POST，
  GET 会落到根路径处理器并报 401 `缺少 access_token`）
- 根路径 `/{action}` 只接受单段路径：PocketBase 的根路由会把任意深度路径的首段当成
  路径参数，导致 `/foo/bar/baz` 这类未知路径被当成 action 返回 401/1404，现在返回 404

## [0.2.3] - 2026-11-14

### 新增

- 实现 OneBot v11 标准 HTTP 接口：`GET|POST /{action}`（含尾部斜杠），
  支持 query、urlencoded 表单、JSON 三种传参，字符串参数自动还原为数字/布尔
- 机器人按 `access_token` 反查，标准接口必须提供 token（在 `qq_bots.onebot_token` 配置）
- 状态码遵循规范：401 未提供 token、403 token 不符、404 action 不存在、其余 200

### 修复

- 配了 `onebot_token` 的机器人不再被"唯一启用"兜底命中，必须显式提供 token

## [0.2.2] - 2026-11-14

### 修复

- WebSocket action 改为并发处理：单个慢 action（如内网大文件转存）不再阻塞同一连接上的
  其它请求，单连接并发上限 16，满了背压等待而不丢请求
- 内网媒体下载超时从 30 秒调整为 3 分钟

## [0.2.1] - 2026-11-14

### 新增

- 内网媒体自动转存：URL 指向 `10/8`、`172.16/12`、`192.168/16`、`127/8`、`localhost`、
  `*.local` 或无点主机名时，先由服务端下载并存入 PocketBase，再用预签名 URL 发给 QQ
  （单文件 20MB 上限，30 秒下载超时）

### 变更

- `onebot_media` 删除从未使用的 `size` / `content_type` 字段（文件信息由 PocketBase 存储层管理）

## [0.2.0] - 2026-11-14

### 新增

- OneBot v11 HTTP API：`POST /onebot/v11/{botId}/{action}`，与 WebSocket 共用同一套 action 实现
- 认证支持 `Authorization: Bearer` 与 `?access_token=` 两种写法，WS 与 HTTP 统一校验
- `examples/zerobot` 支持 `ONEBOT_MODE=http` 切换到 HTTP driver

### 说明

- HTTP 通道只提供 action 接口，事件推送仍走 WebSocket。ZeroBot 的 HTTP driver 需要服务端
  主动 POST 事件到其监听地址，本项目暂未实现该上报，故 HTTP driver 只能握手与发消息

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
