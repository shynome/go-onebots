package onebot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
)

// maxActionBody HTTP action 请求体上限
const maxActionBody = 4 << 20

// retcodeUnsupported 未实现的 action, 标准 HTTP 接口下要求返回 404
const retcodeUnsupported = 1404

// requestToken 取出请求携带的 OneBot access_token
// 支持 Authorization: Bearer 与 ?access_token= 两种约定
func requestToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}
	return r.URL.Query().Get("access_token")
}

// ServeStdAction 实现 OneBot v11 标准 HTTP 接口
//
//	GET  /{action}?key=value
//	POST /{action}          支持 JSON / urlencoded / query 三种传参
//
// 机器人靠 access_token 反查; 未配置 token 且只有一个启用中的机器人时直接命中。
func (m *Manager) ServeStdAction(e *core.RequestEvent) error {
	action := e.Request.PathValue("action")
	if action == "" {
		writeJSONError(e, http.StatusBadRequest, "缺少 action")
		return nil
	}

	token := requestToken(e.Request)
	botID, err := m.resolveBotByToken(token)
	if err != nil {
		// 规范: 未提供 token 返回 401, token 不符返回 403
		status := http.StatusForbidden
		if token == "" {
			status = http.StatusUnauthorized
		}
		writeJSONError(e, status, err.Error())
		return nil
	}

	params, err := paramsFromRequest(e.Request)
	if err != nil {
		writeJSONError(e, http.StatusBadRequest, err.Error())
		return nil
	}

	c := m.Get(botID)
	resp := c.handleAction(e.Request.Context(), action, params)

	e.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	if resp.RetCode == retcodeUnsupported {
		e.Response.WriteHeader(http.StatusNotFound)
	}
	return json.NewEncoder(e.Response).Encode(resp)
}

// ServeHTTP 指定机器人的 HTTP 接口, 便于多机器人场景直接寻址
//
//	POST /onebot/v11/{botId}/{action}
//
// 请求体即为 params, 认证方式与 WS 一致(Bearer 或 access_token 查询参数)。
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

	params, err := paramsFromRequest(e.Request)
	if err != nil {
		writeJSONError(e, http.StatusBadRequest, err.Error())
		return nil
	}

	c := m.Get(botID)
	resp := c.handleAction(e.Request.Context(), action, params)

	e.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	if resp.RetCode == retcodeUnsupported {
		e.Response.WriteHeader(http.StatusNotFound)
	}
	return json.NewEncoder(e.Response).Encode(resp)
}

// resolveBotByToken 根据 access_token 反查机器人
// 标准接口没有 botId, 必须靠 token 定位
func (m *Manager) resolveBotByToken(token string) (string, error) {
	if token == "" {
		return "", errors.New("缺少 access_token")
	}

	recs, err := m.app.FindRecordsByFilter(db.TableQQBots,
		"onebot_token = {:token}", "", 2, 0, dbx.Params{"token": token})
	if err != nil {
		return "", err
	}
	switch len(recs) {
	case 1:
		return recs[0].Id, nil
	case 0:
		return "", errors.New("access_token 无效")
	default:
		return "", errors.New("access_token 对应多个机器人, 请改用 /onebot/v11/{botId}/{action}")
	}
}

// paramsFromRequest 按规范和请求形态组装 params
// JSON 请求体直接透传; query / urlencoded 需要做字符串到数字的类型推断
func paramsFromRequest(r *http.Request) (json.RawMessage, error) {
	if r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxActionBody))
		if err != nil {
			return nil, errors.New("读取请求体失败: " + err.Error())
		}
		if len(body) == 0 {
			return json.RawMessage("{}"), nil
		}
		return json.RawMessage(body), nil
	}

	values := url.Values{}
	for k, v := range r.URL.Query() {
		if k == "access_token" {
			continue
		}
		values[k] = v
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return nil, errors.New("解析表单失败: " + err.Error())
		}
		for k, v := range r.PostForm {
			if k == "access_token" {
				continue
			}
			values[k] = v
		}
	}

	obj := make(map[string]any, len(values))
	for k, v := range values {
		if len(v) == 0 {
			continue
		}
		obj[k] = inferValue(v[0])
	}
	return json.Marshal(obj)
}

// inferValue 把表单里的字符串还原成合适的 JSON 类型
// 规范要求实现方从字符串解析出对应类型, 否则 user_id 之类的数字字段会解析失败
func inferValue(s string) any {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !strings.ContainsAny(s, "eE") {
		return f
	}
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	return s
}
