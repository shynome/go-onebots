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

		// file 字段设为 protected, /api/files 访问需要 token
		if field, ok := col.Fields.GetByName("file").(*core.FileField); ok {
			field.Protected = true
		}

		// 收回匿名访问, 仅超级用户可列出/查看记录
		col.ListRule = nil
		col.ViewRule = nil

		try.To(app.Save(col))
		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
