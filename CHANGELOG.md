# Changelog

## [0.3.9] - 2026-11-14

### 变更

- `@全体` 备用端改用 ZeroBot 的 `driver.HTTPCaller` 调用，不再手写 HTTP 与响应解析
  - 成功判定以 `status`（`ok` / `async`）为准，不再拿 `retcode` 当 HTTP 状态码；
    部分实现成功时也返回 `retcode=200`，旧逻辑会误报失败
  - 主模块新增 `github.com/wdvxdr1123/ZeroBot v1.8.2` 依赖

## [0.3.8] - 2026-11-14

### 新增

- `onebot_id_map` 与 `onebot_at_fallback` 增加 `name` 备注字段，便于在后台识别
  是哪个群 / 哪个人 / 哪个备用实例
  - 同时把字段顺序理顺：`name` 紧跟 `kind`，`at_fallback` 移到 `qq_number` 之后，
    备用端表的 `name` 放在最前

## [0.3.7] - 2026-11-14

### 新增

- **`@全体` 备用端**：主端（QQ 官方 API）因机器人无管理员权限或该群当日已达上限
  被拒时，去掉 `at` 段正常发出图文后，再由备用 OneBot 实现补发一条只有 `@全体` 的消息
  - 新表 `onebot_at_fallback`（`base_url` / `access_token`），在 `onebot_id_map`
    的群记录上用 `at_fallback` 字段选用，需该群已填 `qq_number` 才会触发
  - 新增 `internal/atall` 包，只做 `POST /send_group_msg` 这一件事

### 变更

- 日志改用 PocketBase 的 `app.Logger()`，写入 `_logs` 表供后台查看
  - `qqbot.Client` 新增可选 `Config.Logger`，未注入时退回 `slog.Default()`
  - 注意：`Debug` 级别是否落库取决于后台日志级别设置

## [0.3.6] - 2026-11-14

### 新增

- `@全体成员` 被 QQ 拒绝（机器人无管理员权限 / 该群当日已达上限）时，
  自动去掉 `at` 段重新构建消息并重发，不再整条失败

### 变更

- 移除本地「每群每天一次 @全体」冷却，改由 QQ 侧裁决；
  判定是否含 @全体 改为遍历消息段，不再只扫 `content`
  （markdown 消息的 `at` 段在 `segments` 里，原来查不出来）

## [0.3.5] - 2026-11-14

### 修复

- markdown 消息不再丢失 `@全体`：`markdownTextSegments` 保留 `at` 段，
  `markdownFromSegments` 将其渲染为 `<qqbot-at-everyone />`
  - 之前「文本 + 图片 + @全体」走 markdown 分支时，`at` 段被过滤，
    消息发出去群里没有 @ 到任何人

## [0.3.4] - 2026-11-14

### 修复

- PocketBase 后台字段展示顺序整理，按「身份 → 内容 → 媒体 → 状态」排列：
  - `onebot_outbox`：`content` / `markdown` / `segments` 提到 `media_*` 之前
  - `onebot_events`：`event`（大 JSON）移到末尾
  - `onebot_media`：`source` / `width` / `height` 紧跟 `kind`
  - `onebot_id_map`：`qq_number` 紧跟 `num`

### 回退

- 撤回 0.3.3 中「markdown 消息下 `onebot_events.content` 为空」的改动：
  该列恢复存 `raw_message`；纯媒体消息不再清空 `req.Content`，
  文本 + 视频消息的文本部分不再丢失

## [0.3.2] - 2026-11-14

### 修复

- markdown 图片按来源缓存在 `onebot_media`（新增 `source` / `width` / `height`）：
  同一来源只下载/转存一次，**跨消息复用**
  - 公网 URL 用链接本身做 key，本地文件 / base64 用内容 sha256
  - 命中缓存直接复用 URL 与尺寸，发送重试也不会重复下载

## [0.3.1] - 2026-11-14

### 修复

- markdown 模式下普通文本做转义：`*`、`_`、`#`、`[`、`]`、`~`、`|`、`` ` ``、`>`
  以及行首的 `-` / `+` 不再被当成格式渲染（实测 QQ 支持反斜杠转义）；
  显式 markdown 段仍原样发送

## [0.3.0] - 2026-11-14

### 新增

- 文本 + 图片的消息改用 QQ **Markdown 消息**（`msg_type=2`）发送：图片不再被
  强制排在文字前面，顺序完全按消息段排列；一条消息可含多张图，宽高从图片本身读取
  （QQ 要求 markdown 图片必须带尺寸，否则会退化成 `[alt]` 文本）
- 支持显式 markdown 段：`{"type":"markdown","data":{"content":"..."}}`

### 说明

- markdown 文本会按语法渲染，`*`、`_`、`#` 等会被当作格式
- 纯图片、以及含视频/语音/文件的消息仍走富媒体（markdown 不支持这些类型）

## [0.2.11] - 2026-11-14

### 变更

- 中转媒体（本地文件 / `base64://` / 内网 URL）**要求配置 S3**：未配置时直接返回
  `未配置 S3, 不支持发送图片等中转媒体`，不再先报 `not an auth collection record`
  这种难懂的错误（PocketBase 只允许 auth collection 签发文件访问 token）

## [0.2.10] - 2026-11-14

### 修复

- 参数兼容 OneBot 规范的宽松数字：`{"group_id": "123456789"}` 这类字符串形式
  不再报 `cannot unmarshal string into Go struct field ... of type int64`，
  覆盖 send / lookup / history / delete 的全部数字参数

## [0.2.9] - 2026-11-14

### 新增

- `qq_number` 从"仅群号"扩展为"真实号码"，私聊用户也可填写真实 QQ 号：
  - `send_private_msg` 支持传真实 QQ 号
  - 入站单聊事件与出站记录的 `user_id` 使用真实 QQ 号

## [0.2.8] - 2026-11-14

### 变更

- 真实群号改为直接填在 `onebot_id_map` 的群记录上（`qq_number` 字段），
  不再需要复制 openid 到独立的 `onebot_group_bind` 表；旧表已删除，已有数据自动迁移

## [0.2.7] - 2026-11-14

### 新增

- 群号绑定后**收发统一**用真实群号：入站事件、出站记录、`get_group_list`、`get_group_info`
  都返回真实群号；未绑定时维持原映射 id

## [0.2.6] - 2026-11-14

### 新增

- 支持用真实群号发消息：新增 `onebot_group_bind`(bot / qq_number / group_openid)，
  `send_group_msg` 传真实群号时先查绑定表拿 openid；映射 id 仍然可用
  （QQ 官方只给 openid，群号无法自动获取，需管理员手工绑定）

## [0.2.5] - 2026-11-14

### 变更

- 移除根路径形式 `/{action}`：PocketBase 的根路由会把任意深度路径的首段当成路径参数，
  导致所有未匹配请求被吞掉、正常的 404 变成 401/1404。HTTP 接口统一为
  `GET|POST /onebot/v11/{botId}/{action}`

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
