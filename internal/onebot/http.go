package onebot

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// maxActionBody HTTP action 请求体上限
const maxActionBody = 4 << 20

// ServeHTTP 处理 OneBot v11 HTTP API, 与 WebSocket 共用同一套 action 实现
//
//	POST /onebot/v11/{botId}/{action}
//
// 请求体即为 params, 认证方式与 WS 一致(Bearer 或 access_token 查询参数)。
// 与 OneBot v11 的约定一致: 无论成功失败都返回 200, 由 retcode 区分结果。
func (m *Manager) ServeHTTP(e *core.RequestEvent) error {
	botID := e.Request.PathValue("botId")
	action := e.Request.PathValue("action")
	if action == "" {
		writeJSONError(e, http.StatusBadRequest, "缺少 action")
		return nil
	}
	if _, ok := m.checkAuth(e, botID); !ok {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(e.Request.Body, maxActionBody))
	if err != nil {
		writeJSONError(e, http.StatusBadRequest, "读取请求体失败: "+err.Error())
		return nil
	}
	if len(body) == 0 {
		body = []byte("{}")
	}

	c := m.Get(botID)
	resp := c.handleAction(e.Request.Context(), action, json.RawMessage(body))

	e.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	return json.NewEncoder(e.Response).Encode(resp)
}
