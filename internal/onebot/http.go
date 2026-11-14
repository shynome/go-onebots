package onebot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
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

// ServeHTTP 指定机器人的 HTTP 接口, 支持 GET 与 POST
//
//	GET  /onebot/v11/{botId}/{action}?key=value
//	POST /onebot/v11/{botId}/{action}          JSON / urlencoded / query 均可
//
// 认证方式与 WS 一致(Bearer 或 access_token 查询参数), 机器人没配 token 时免校验。
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
