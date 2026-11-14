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
// 地址 / token / 群号全部从 .env 读取, 不提供默认值
func TestSendAtAll(t *testing.T) {
	groupNumber, err := strconv.ParseInt(os.Getenv("ATALL_GROUP"), 10, 64)
	if err != nil {
		t.Fatalf("ATALL_GROUP 不是合法群号: %v", err)
	}
	c := &Client{caller: &driver.HTTPCaller{
		URL:         os.Getenv("ATALL_URL"),
		AccessToken: os.Getenv("ATALL_TOKEN"),
	}}
	if err := c.SendAtAll(context.Background(), groupNumber); err != nil {
		t.Fatal(err)
	}
}
