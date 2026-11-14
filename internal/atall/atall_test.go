package atall

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/wdvxdr1123/ZeroBot/driver"
)

// 需要备用 OneBot 端在线, 手动执行:
//
//	go test ./internal/atall -run TestSendAtAll -v
//
// 地址 / token / 群号全部从 .env 读取, 缺失时直接跳过(不提供默认值)
func TestSendAtAll(t *testing.T) {
	base := os.Getenv("ATALL_URL")
	group := os.Getenv("ATALL_GROUP")
	if base == "" || group == "" {
		t.Skip("缺少 ATALL_URL / ATALL_GROUP, 跳过备用端实测")
	}
	groupNumber, err := strconv.ParseInt(group, 10, 64)
	if err != nil {
		t.Fatalf("ATALL_GROUP 不是合法群号: %v", err)
	}
	c := &Client{caller: &driver.HTTPCaller{
		URL:         base,
		AccessToken: os.Getenv("ATALL_TOKEN"),
	}}
	if err := c.SendAtAll(context.Background(), groupNumber); err != nil {
		t.Fatal(err)
	}
}
