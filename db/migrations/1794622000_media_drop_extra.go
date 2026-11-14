package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 中转媒体改为 FileField 后, size / content_type 变成死字段
// 两者从未写入也从未读取, 文件真实大小与 MIME 由 PocketBase 在存储层管理
//
// PocketBase 不允许删除 System 字段, 而这两个字段当初是带 System 标记建的,
// 所以先摘掉标记保存一次, 再删除。
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		media := try.To1(app.FindCollectionByNameOrId(db.TableMedia))

		for _, name := range []string{"size", "content_type"} {
			if f := media.Fields.GetByName(name); f != nil {
				f.SetSystem(false)
			}
		}
		try.To(app.Save(media))

		media.Fields.RemoveByName("size")
		media.Fields.RemoveByName("content_type")
		try.To(app.Save(media))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
