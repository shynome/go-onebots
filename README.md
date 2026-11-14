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
| `send_private_msg` / `send_group_msg` / `send_msg` | 纯文本，自动作为被动回复发送 |
| `get_status` / `get_version_info` | 本地返回 |

**事件**：`C2C_MESSAGE_CREATE` → 私聊消息；`GROUP_AT_MESSAGE_CREATE` → 群消息。

**消息缓存**：所有入站事件都会写进 `onebot_events`。客户端掉线期间到达的消息会在
重连后自动补发（保留 30 分钟 / 最多 2000 条），服务重启也会从库里恢复未投递事件。
超出 QQ 被动回复窗口（5 分钟）的消息补发后插件可能发不出回复，但可以在后台按
`delivered = false` 查到。

**历史清理**：`onebot_events` 按 PocketBase 的 `Settings → Logs → Max days` 定期清理
（每天 03:30），无需单独配置。

**限制（QQ 官方 API 本身）**

- 群聊只能收到 @ 机器人的消息，拿不到全量群消息
- 私聊只能收到用户主动发起的会话
- 拿不到群列表 / 群成员列表 / 群昵称
- 被动回复必须在收到消息后 5 分钟内、最多 5 条
- 图片 / 语音 / 视频 / 文件、频道相关、`delete_msg`、`get_msg` 等暂未实现，
  未实现的 action 统一返回 `retcode=1404`

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
db/                      表结构常量与 migration
internal/qqbot/          QQ 官方 SDK（token / 网关 / 心跳 / 发消息）
internal/idmap/          openid <-> int64 映射（PocketBase 持久化）
internal/eventlog/       事件日志与未投递查询（离线补发）
internal/bot/            机器人运行时（连接、状态、事件分发）
internal/onebot/         OneBot v11 协议层（握手、事件转换、action 分发）
cmd/obtest/              OneBot 冒烟测试客户端
examples/zerobot/        ZeroBot 示例（独立 module）
```

设计细节见 [design.md](./design.md)。
