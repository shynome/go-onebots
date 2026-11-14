package onebot

import (
	"bytes"
	"strconv"
)

// flexInt 兼容 OneBot v11 的宽松数字参数
//
// 规范允许用字符串传数字, 例如 {"group_id": "123456789"} 或 ?group_id=123456789,
// 直接用 int64 会在 json.Unmarshal 时报
// "cannot unmarshal string into Go struct field ... of type int64"。
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*f = 0
		return nil
	}

	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		*f = 0
		return nil
	}

	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt(n)
		return nil
	}

	// 兼容 "123456789.0" 这类浮点写法
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = flexInt(int64(v))
	return nil
}

// Int64 取出实际数值
func (f flexInt) Int64() int64 { return int64(f) }
