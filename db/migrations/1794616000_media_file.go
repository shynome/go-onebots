package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		col := try.To1(app.FindCollectionByNameOrId(db.TableMedia))

		// 用 PocketBase 的 file 字段管理文件, 删除记录时 PB 会自动清理 S3 对象
		col.Fields.AddAt(4, &core.FileField{
			Name: "file", Id: ID("file"), System: true,
			Required: false, MaxSelect: 1,
		})

		// 允许未认证访问 /api/files/..., QQ 下载中转媒体需要
		open := ""
		col.ListRule = &open
		col.ViewRule = &open

		try.To(app.Save(col))
		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
