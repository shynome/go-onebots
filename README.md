# go-onebots

把 **QQ 官方机器人**（QQ 开放平台）包装成 **OneBot v11** WebSocket 服务端，
可直接被 [ZeroBot](https://github.com/wdvxdr1123/ZeroBot) 等 OneBot v11 客户端连接。

- 只支持 QQ 官方后端
- 只支持 OneBot v11
- 只支持 WebSocket（正向 WS）
- 后端使用 PocketBase（配置与管理界面开箱即用）

## 能力与限制

**支持**

| OneBot action | 说明 |
| --- | --- |
| `get_login_info` | 返回机器人自身 id（映射后的 int64）与名称 |
| `send_private_msg` / `send_group_msg` / `send_msg` | 文本与富媒体，自动作为被动回复发送 |
| `delete_msg` | 撤回消息（QQ 官方限时约 2 分钟） |
| `get_stranger_info` / `get_group_member_info` | 返回最后见到的昵称 / 群名片 / 角色快照 |
| `get_group_info` / `get_group_list` | 从已见过的群生成，群名无法获取 |
| `get_msg` | 从事件缓存重建原消息（保留期内有效，含机器人自己发的） |
| `get_group_msg_history` | 群消息历史，按时间升序，`count` 默认 20 / 上限 100 |
| `get_friend_list` / `get_group_member_list` | 官方无此接口，返回空数组 |
| `get_status` / `get_version_info` | 本地返回 |

**事件**：`C2C_MESSAGE_CREATE` → 私聊消息；`GROUP_AT_MESSAGE_CREATE` → 群 @ 消息；
`GROUP_MESSAGE_CREATE` → 群内全量消息（需在开放平台开启「接收所有消息」，不 @ 机器人时不触发命令）。
入站附件（图片 / 语音 / 视频 / 文件）会转成对应的 OneBot 消息段，`sender` 带真实昵称、群名片与群角色。

**消息缓存**：所有入站事件都会写进 `onebot_events`。客户端掉线期间到达的消息会在
重连后自动补发（保留 30 分钟 / 最多 2000 条），服务重启也会从库里恢复未投递事件。
超出 QQ 被动回复窗口（5 分钟）的消息补发后插件可能发不出回复，但可以在后台按
`delivered = false` 查到。

**历史清理**：`onebot_events` 按 PocketBase 的 `Settings → Logs → Max days` 定期清理
（每天 03:30），无需单独配置。`get_msg` 与 `get_group_msg_history` 都基于这份缓存，
超出保留期的消息查不到。

**发送可靠性**：每条发出的消息先写入 `onebot_outbox`（`pending`），发送成功转 `sent`
并回填 QQ 消息 id。网络错误 / 429 / 5xx 会退避重试（1s、2s，共 3 次尝试），
其余 4xx（内容审核、权限、频次限制）不重试，直接转 `failed` 并返回错误。
进程崩溃遗留的 `pending` 由每分钟一次的定时任务补发，超过被动回复窗口（5 分钟）
或累计尝试 3 次后直接标记 `failed`——避免把陈旧消息发到群里。
这张表在 PocketBase 后台可见，便于排查。

> 已知限制：如果 QQ 实际已收到但 HTTP 响应超时，重发会导致重复消息，属 at-least-once，无法完全避免。

**媒体转发**：消息段里的媒体支持四种来源——公网 URL（直接透传）、`base64://`、
`file://`（服务端本机路径）、以及**内网 URL**（`10/8`、`172.16/12`、`192.168/16`、`127/8`、
`localhost`、`*.local`、无点主机名如 `http://nas/a.png`）。内网地址 QQ 服务器拉不到，
会先由服务端下载并存入 PocketBase 中转，再用预签名 URL 发送；单文件上限 20MB，
且要求**服务端自身能访问该地址**（同内网 / VPN / 同机）。

**限制（QQ 官方 API 本身）**

- 私聊只能收到用户主动发起的会话
- 群聊需在开放平台开启「接收所有消息」才能收到全量消息，否则只有 @ 机器人
- 拿不到群名、群成员列表、成员入群时间等资料（查询类 API 只能返回消息里见过的快照）
- 机器人可以 **@全体成员**：content 里写 `<qqbot-at-everyone />`，需机器人为群管理员，
  且**每群每天 1 次**（服务端已加本地冷却）；但**无法 @ 单个成员**，官方未提供标记格式
- 频道相关、群管类（`set_group_ban` / `set_group_kick` / `get_forward_msg` 等）官方无接口，返回 `retcode=1404`

## 快速开始

```bash
go run . serve --http=127.0.0.1:8090
```

1. 打开 `http://127.0.0.1:8090/_/` 创建首个超级管理员
2. 在 `qq_bots` 中新增记录：
   - `name`：随意
   - `app_id` / `app_secret`：QQ 开放平台凭证（AppID 是纯数字）
   - `enabled`：勾选，保存后自动连接网关
   - `onebot_token`：OneBot 客户端连接时使用的 Bearer token，留空则不校验
3. `ws_status` 变为「运行中」后即可连接

### OneBot 连接地址

```
ws://<host>:8090/onebot/v11/<botId>/ws
```

`botId` 是 `qq_bots` 记录 id（后台列表里可以看到）。

### HTTP API

除正向 WebSocket 外，也提供 HTTP action 接口，与 WS 共用同一套实现：

```
GET  http://<host>:8090/onebot/v11/<botId>/<action>?key=value
POST http://<host>:8090/onebot/v11/<botId>/<action>     # JSON / urlencoded / query 均可
```

机器人由 URL 里的 `botId` 指定；`qq_bots.onebot_token` 非空时才校验 token，
留空则免校验。状态码：

| 状态码 | 含义 |
| --- | --- |
| 200 | 请求已处理，成功与否看响应体 `retcode` |
| 404 | action 不存在 |
| 401 / 403 | 机器人配了 token 但未提供 / 不匹配 |

```bash
curl 'http://127.0.0.1:8090/onebot/v11/<botId>/get_status'
# {"status":"ok","retcode":0,"data":{"good":true,"online":true}}

curl -X POST http://127.0.0.1:8090/onebot/v11/<botId>/send_group_msg \
  -d '{"group_id":10000000005,"message":[{"type":"text","data":{"text":"hi"}}]}'

# query / urlencoded 传参会被自动做类型还原（字符串 "10000000005" -> 数字）
curl -X POST http://127.0.0.1:8090/onebot/v11/<botId>/send_group_msg \
  --data-urlencode "group_id=10000000005" --data-urlencode "message=hi"
```

> 没有实现 OneBot 11 规范里的根路径形式 `POST /{action}`。PocketBase 的根路由会把任意
> 深度路径的首段当成路径参数，挂上 `/{action}` 会吞掉所有未匹配请求、把正常的 404 变成
> 401/1404，代价大于收益。客户端把 base URL 配到 `.../onebot/v11/<botId>` 即可。

注意 ZeroBot 的 HTTP driver 是**纯 HTTP 模式**：`NewHTTPClient(url, token, callerURL, callerToken)`
中 `callerURL` 是 action 请求地址（即上面的接口），`url` 是 ZeroBot 自己监听的地址，
用于**接收服务端上报的事件**（可选 `X-Signature: sha1=<hmac-sha1>` 校验）。
本项目目前只提供 action 接口，**尚未实现事件 HTTP 上报**，所以 ZeroBot 的 HTTP driver
只能完成握手与发消息，收不到事件；需要接收事件请继续用 WebSocket driver。

### ZeroBot 接入

```go
zero.RunAndBlock(&zero.Config{
    CommandPrefix: "/",
    Driver: []zero.Driver{
        driver.NewWebSocketClient("ws://127.0.0.1:8090/onebot/v11/<botId>/ws", "<onebot_token>"),
    },
}, nil)
```

完整示例见 `examples/zerobot`（独立 module）：

```bash
ONEBOT_WS=ws://127.0.0.1:8090/onebot/v11/<botId>/ws \
ONEBOT_TOKEN=<onebot_token> go -C examples/zerobot run .
```

### 冒烟测试（不依赖 ZeroBot）

```bash
go run ./cmd/obtest -url ws://127.0.0.1:8090/onebot/v11/<botId>/ws -token <onebot_token>
```

## 构建与部署

```bash
make build     # 本地静态编译到 ./go-onebots（版本号取自 git describe）
make docker    # 构建镜像 shynome/go-onebots:<版本>
make push      # 推送镜像
```

容器运行：

```bash
docker run -d --name onebots \
  -p 8090:8090 \
  -v /opt/onebots/pb_data:/app/pb_data \
  shynome/go-onebots:<版本>
```

`/app/pb_data` 是 PocketBase 数据目录（数据库、上传文件、S3 等全部设置都在这里），
必须挂载出来，否则重建容器会丢失全部配置。首次启动后访问 `/_/` 创建管理员。

## 发消息用真实群号

QQ 官方 API 只有 `group_openid`，不提供真实群号，所以需要手工绑定一次：

1. 让机器人先在该群收到一条消息，后台 `onebot_id_map` 里会出现一条 `kind=group` 的记录，
   复制它的 `openid`
2. 在 `onebot_group_bind` 里新增：`bot` + `qq_number`（真实群号）+ `group_openid`
3. 之后 `send_group_msg` 的 `group_id` 可以直接传真实群号，映射 id 仍然可用

注意这只影响**发送**；事件里的 `group_id` 仍是映射后的 id（以 `10000000000` 打头）。

## 管理动作

在 `qq_bots` 记录的 `do_action` 里选择：

| 值 | 行为 |
| --- | --- |
| `stop` | 断开 QQ 网关，状态置为「手动停止」 |
| `restart` | 重新连接（修改 app_id/secret 后用它生效） |

## 目录结构

```
main.go                  PocketBase 启动入口
bots.go                  机器人生命周期 hook + OneBot 路由挂载
events_cleanup.go        事件表与中转媒体的定时清理
Makefile / Dockerfile    构建与镜像
db/                      表结构常量与 migration
internal/qqbot/          QQ 官方 SDK（token / 网关 / 心跳 / 发消息）
internal/idmap/          openid <-> int64 映射（PocketBase 持久化）
internal/eventlog/       事件日志与未投递查询（离线补发）
internal/outbox/         出站消息队列（先落库再发送，支持崩溃补发）
internal/bot/            机器人运行时（连接、状态、事件分发）
internal/onebot/         OneBot v11 协议层（握手、事件转换、action 分发）
cmd/obtest/              OneBot 冒烟测试客户端
examples/zerobot/        ZeroBot 示例（独立 module）
```

设计细节见 [design.md](./design.md)。
