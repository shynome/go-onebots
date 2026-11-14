# go-onebots 设计文档

把 QQ 官方机器人（QQ 开放平台 / `api.bot.qq.com`）包装成 **OneBot v11** 协议服务端，
供 [ZeroBot](https://github.com/wdvxdr1123/ZeroBot) 等 OneBot v11 客户端直接连接。

- 只支持 **QQ 官方后端**
- 只支持 **OneBot v11**
- 只支持 **WebSocket**（正向 WS，客户端连服务端）
- 不使用 onebots(TS) 的代码

## 一、为什么这么设计：ZeroBot 的硬性对接要求

来自 ZeroBot `driver/wsclient.go` 与 `bot.go` 的实际实现（不是猜测）：

| 要求 | 说明 |
| --- | --- |
| 握手首帧 | 连接成功后读到的**第一帧必须是 `{"self_id": <int64>}`**，否则断开重连 |
| `echo` 必须是数字 | driver 用 `rsp.Get("echo").Uint()` 匹配请求，字符串 echo 会丢响应导致超时 |
| 响应格式 | `{"status":"ok","retcode":0,"data":{...},"echo":N}` |
| ID 必须是 int64 | `user_id` / `group_id` / `message_id` 全部按 int64 解析，见第二节 ID 映射 |
| `message` 用段数组 | `message.ParseMessage` 遇 `[` 开头按数组解析；本服务统一推数组格式 |
| `at` 段决定 IsToMe | 群消息必须有 `{"type":"at","data":{"qq":"<self_id>"}}`，否则 `IsToMe=false`，插件命令不会触发 |
| 心跳事件 | `meta_event_type == "heartbeat"` 会被忽略，本服务不发送 |

## 二、核心难点：openid ↔ int64 映射

QQ 官方所有 id（用户、群、消息）都是字符串 openid，而 OneBot v11 要求 int64，
并且**反向必须可还原**（发消息、撤回都要用 openid）。

方案：PocketBase collection `onebot_id_map`（用户/群）与 `onebot_message_map`（消息）
持久化自增分配。

- 号段从 `10000000000` 开始，`MAX(num)+1` 在 `app.RunInTransaction` 内分配（sqlite 写串行，安全）
- 唯一索引 `(bot, kind, openid)` 与 `(bot, kind, num)`（消息表为 `(bot, openid)` / `(bot, num)`）
- 进程内加一层 `map` 缓存，PB 只在新 id 首次出现时写一次
- 每机器人独立号段隔离，避免多机器人 id 互相变化
- 消息映射数量级远大于用户/群，单独存表，避免撑大 id 映射表并拖慢 `MAX(num)`

`onebot_id_map.kind` 取值：

| kind | 含义 | 典型来源 |
| --- | --- | --- |
| `self` | 机器人自己 | WS READY `d.user.id` |
| `user` | 用户 openid | 事件 `author.member_openid` / `author.user_openid` |
| `group` | 群 openid | 事件 `group_openid` |

消息 id（事件 `id`）映射在 `onebot_message_map`，`extra` 记录归属目标用于后续撤回。

## 三、事件映射

### 3.1 支持的事件

| QQ 官方事件 | OneBot v11 |
| --- | --- |
| `C2C_MESSAGE_CREATE` | `post_type=message`，`message_type=private`，`sub_type=friend` |
| `GROUP_AT_MESSAGE_CREATE` | `post_type=message`，`message_type=group`，`sub_type=normal`，一定带 `at` 机器人段 |
| `GROUP_MESSAGE_CREATE` | 同上，但只有 `mentions` 里 @ 了机器人才带 `at` 段 |
| 其它（`FRIEND_ADD` / `GROUP_MSG_RECEIVE` / `GROUP_ADD_ROBOT` …） | MVP 忽略，仅打日志 |

`GROUP_MESSAGE_CREATE` 是「群消息（全量模式）」，与 `GROUP_AT_MESSAGE_CREATE` 共用
`GROUP_AND_C2C_EVENT (1<<25)`，需要在开放平台开启「接收所有消息」才会下发，
字段与 `GROUP_AT_MESSAGE_CREATE` 一致，额外带 `mentions`。

### 3.2 群消息的 @ 处理

QQ 官方群消息 `content` 里 `@某成员` 会写成 `<@member_openid>`，`@全体` 写成 `<@all>`。
转换时会拆成 `at` 段（映射成 int64，`<@all>` 用 `qq=all`）。

- `GROUP_AT_MESSAGE_CREATE`：一定是 @ 机器人，**保证**首位有 `at` 机器人段
- `GROUP_MESSAGE_CREATE`（全量）：只有 `mentions` 包含机器人才补 `at` 段，否则 `IsToMe=false`

ZeroBot 靠 `at` 段判断 `IsToMe`，群里命令才会触发；全量消息不 @ 机器人时不会误触发命令。

### 3.2.1 消息去重

按消息 id 在内存中保留 10 分钟（最多 2000 条）去重，避免：

- WS `resume` 重放最近 5 条导致重复处理
- 开启全量模式后 `GROUP_AT_MESSAGE_CREATE` 与 `GROUP_MESSAGE_CREATE` 同时到达

### 3.3 sender 字段

QQ 官方事件里自带 `username` / `member_nickname` / `member_role` / `avatar`：

| 字段 | 取值 |
| --- | --- |
| `nickname` | `username` → 回退 `member_nickname` → 回退 `"用户"+num` |
| `card` | 群聊取 `member_nickname`，私聊为空 |
| `role` | 群聊取 `member_role`（`owner` / `admin` / `member`），空则填 `member` |
| `user_id` | 映射后的 int64 |

同时把昵称/头像/角色写入 `onebot_id_map.extra`，供后续查询类 API 使用。

### 3.4 入站附件

事件里的 `attachments` 会转成 OneBot 消息段：

| 来源 | 消息段 |
| --- | --- |
| `content_type` 以 `image/` 开头 | `image` |
| `content_type = file` 且扩展名是图片 | `image`（QQ 单聊实际就是这样下发的） |
| `content_type = voice` / `audio/*` | `record`（优先 `voice_wav_url`） |
| `content_type` 以 `video/` 开头，或 `file` + 视频扩展名 | `video` |
| 其他 | `file` |

附件 URL 是限时链接，插件需要即时下载。

## 四、Action 支持（MVP）

| action | 实现 |
| --- | --- |
| `get_login_info` | 直接返回 WS READY 缓存的 `user.id` / `user.username`，不额外发请求 |
| `send_private_msg` | `POST /v2/users/{openid}/messages` |
| `send_group_msg` | `POST /v2/groups/{openid}/messages` |
| `send_msg` | 按 `message_type` 分派到上面两个 |
| `get_status` | 本地返回 `{"online":true,"good":true}` |
| `get_version_info` | 本地返回 `app_name=go-onebots`、`app_version` |
| 其它所有 action | `retcode=1404`，`wording` 说明 QQ 官方 API 不支持 |

### 4.1 被动回复上下文（关键）

QQ 官方只允许在收到消息后 **5 分钟内、最多 5 条** 的被动回复，否则报 40054。

实现：`replyctx` 按 `(bot, scene, target_openid)` 记录最近一条入站消息：

```
{msg_id, expire_at, seq}
```

- 事件到达时写入/刷新，`expire_at = now + 5min`
- 发送时若未过期：带 `msg_id`，`msg_seq = ++seq`
- `seq > 5` 或已过期：清空 `msg_id`，作为主动消息发送（QQ 可能拒绝，日志告警）

### 4.2 消息段降级

MVP 只发送纯文本：

- 多个 `text` 段直接拼接
- `image` / `record` / `video` / `face` / `at` / `reply` 等段落**跳过并打 warning**，
  不做静默丢弃，便于用户判断为什么没发出去

## 五、服务端设计

### 5.1 WS 端点

- 挂在 PocketBase 同一端口：`GET /onebot/v11/{botId}/ws`
- `botId` 是 `qq_bots` 记录 id（PocketBase 生成的 15 位字符串），不重名
- 鉴权：`Authorization: Bearer <onebot_token>`，字段为空则跳过校验
- 与 ZeroBot 的 `driver.NewWebSocketClient("ws://host/onebot/v11/<id>/ws", "<token>")` 对应

### 5.2 连接行为

1. 鉴权通过后立刻发首帧 `{"self_id": <self num>, "status":"ok", "retcode":0}`
2. 之后只推送事件，不推送心跳
3. 事件广播给该 bot 的所有连接（正常只有 ZeroBot 一个）
4. 服务端每 30s ping 一次，清理死连接
5. 写操作串行化（websocket 连接不是并发安全的）

### 5.3 生命周期

| 触发 | 行为 |
| --- | --- |
| PocketBase 启动 | 启动所有 `enabled=true` 的机器人：QQ WS 连接 + OneBot WS 端点可用 |
| `do_action=restart` | 关闭旧连接后重建 |
| `do_action=stop` | 关闭 QQ WS，`ws_status=手动停止`，OneBot WS 仍可连但无事件 |
| 删除机器人记录 | 关闭 QQ WS 连接 |

`ws_status` 取值：`启动中` / `运行中` / `出错了` / `手动停止` / `已停用`。

## 六、数据模型

### 6.1 `qq_bots`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| name | text | 显示名 |
| app_id | text | QQ 开放平台 AppID |
| app_secret | text (hidden) | AppSecret |
| enabled | bool | 是否随服务启动 |
| onebot_token | text | OneBot WS 鉴权，空则不校验 |
| ws_status | text | 运行状态 |
| do_action | select | `stop` / `restart` |

### 6.2 `onebot_id_map`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| bot | relation → qq_bots | 按机器人生效 |
| kind | select | `self` / `user` / `group` |
| openid | text | 官方原始 id |
| num | number(int) | 分配到的 int64 |
| extra | json | 附加信息（昵称 / 名片 / 角色 / 头像） |

索引：`(bot, kind, openid)` 唯一，`(bot, kind, num)` 唯一。

### 6.2.1 `onebot_message_map`（消息 id 映射）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| bot | relation → qq_bots | 按机器人生效 |
| openid | text | 官方消息 id |
| num | number(int) | 分配到的 int64 |
| extra | json | `scene`（private/group）与 `target`，供撤回使用 |

索引：`(bot, openid)` 唯一，`(bot, num)` 唯一。

消息映射量级大，且与用户/群 ID 的生命周期不同，单独存储避免拖慢 id 映射操作。

### 6.3 `onebot_events`（事件日志 / 离线补发）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| bot | relation → qq_bots | 所属机器人 |
| seq | number(int) | 每机器人单调递增的事件序号 |
| message_type | select | `private` / `group` |
| user_id / group_id / message_id | number(int) | 映射后的 OneBot id |
| content | text | 纯文本内容，便于后台阅读 |
| event | json | 完整的 OneBot v11 事件，用于补发 |
| delivered | bool | 是否已投递给 OneBot 客户端 |
| delivered_at | date | 投递时间 |

索引：`(bot, seq)` 唯一，`(bot, delivered)` 普通索引。

## 六点五、消息不丢失策略

OneBot v11 没有 ACK / 游标，服务端无法知道客户端处理到哪一条，所以只做「**离线期间补发**」，
不做全量重放（重放会导致插件重复回复）。

| 场景 | 行为 |
| --- | --- |
| 有客户端在线 | 直接推送，同时写 `onebot_events`，200ms 内批量标记 `delivered=true` |
| 没有客户端在线 | 事件写入 `onebot_events`（`delivered=false`），并进入内存待补发队列 |
| 客户端重连 | 握手成功后按 `seq` 升序补发队列，成功后批量标记 `delivered=true` |
| 服务重启 | 启动时从 `onebot_events` 恢复 `delivered=false` 且 30 分钟内的事件到队列 |
| 队列上限 | 30 分钟 / 2000 条，超出丢弃最旧的并告警 |
| 历史清理 | 每天 03:30 删除超过 PocketBase `logs.maxDays` 的旧事件 |

**已知边界**

- 超过 5 分钟后补发的消息，QQ 被动回复窗口已过，插件即使处理也发不出回复（会尝试主动消息并失败）
- 崩溃瞬间正在处理的那一条可能「已投递未处理」
- 连接假死时（TCP 半开）要等 30s ping 超时才判定断开，这期间的事件会发给黑洞
- 后台可用过滤 `delivered = false` 查看漏掉的消息，人工补位

## 七、目录结构

```
go-onebots/
├── main.go                      # PocketBase 启动 + hook 注册
├── db/
│   ├── table.go                 # 表名常量
│   └── migrations/              # qq_bots / onebot_id_map
├── internal/qqbot/              # 自研 QQ 官方 SDK
│   ├── client.go                # token 获取与刷新、REST 调用、错误码
│   ├── gateway.go               # /gateway、op10/2/6/1/11、断线 resume
│   ├── message.go               # 发文本、被动回复
│   └── event.go                 # 事件结构体
├── internal/idmap/idmap.go      # openid ↔ int64
├── internal/eventlog/           # 事件日志与未投递查询
├── internal/onebot/
│   ├── server.go                # WS 挂载、握手、广播
│   ├── action.go                # action 路由
│   ├── convert.go               # QQ 事件 → OneBot 事件
│   └── replyctx.go              # 被动回复上下文
└── design.md
```

## 八、明确不做

- 图片 / 语音 / 视频 / 文件（富媒体需"上传拿 file_info → msg_type=7 发送"两步流程，第二版）
- 频道（guild）相关事件与接口
- `get_group_list` / `get_group_member_list` / `set_group_ban` / `delete_msg` 等
  QQ 官方未开放的接口，统一返回 `retcode=1404`
- 主动消息推送（需单独申请权限，只有被动回复可用）

## 九、QQ 官方的能力天花板（使用前须知）

- 群聊**只能收到 @ 机器人的消息**（`GROUP_AT_MESSAGE_CREATE`），拿不到全量群消息
- 私聊只能收到用户主动发起的会话
- 拿不到群列表、群成员列表、群名
- 无法主动添加好友、无法审批好友申请（受权限限制）
- 富媒体消息不能与多张图片混排在一条里

## 十、实施顺序

1. 骨架：`go.mod` + PocketBase 启动 + migration（`qq_bots` / `onebot_id_map`）
2. QQ 官方 SDK：token、gateway、websocket 心跳与 resume、发文本
3. `idmap`：openid ↔ int64
4. OneBot v11：WS 握手、事件转换、action 分发
5. 端到端验证：ZeroBot demo `OnCommand("hello")` → `world`

## 十一、实现说明（与初稿的差异）

以下为实现过程中定下的细节，均已验证：

- **群消息始终附加 `at` 段**：即使 QQ 的 `content` 里没有 `<@!...>` 标记，
  也会在消息段首位补一个 `{"type":"at","data":{"qq":"<self_id>"}}`，
  保证 ZeroBot 的 `IsToMe` 判定成立。
- **发送结果也做 id 映射**：`send_*` 返回的 `message_id` 是本地分配的 int64，
  原始消息 id 存进 `onebot_id_map.extra`，为后续 `delete_msg` 留好数据。
- **未知 group_id / user_id 直接报错**：QQ 官方没有查询接口，
  必须先收到过该群/该用户的消息，否则返回 `retcode=1400` 并说明原因。
- **WS 握手额外返回 `app` 字段**：不影响 ZeroBot，便于排查接入方。
- **服务端每 30s ping**：清理 ZeroBot 重连后残留的死连接。
- **ZeroBot 示例独立成 module**（`examples/zerobot`），不污染主模块依赖。
- 已验证：网关 identify/READY、OneBot 握手 `self_id`、`get_login_info`、
  QQ 私聊/群 @ 的 `/hello` → `world` 全链路。

