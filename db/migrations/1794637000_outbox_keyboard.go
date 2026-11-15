package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 出站队列支持内嵌键盘(互动按钮):
// 存 keyboard 原始 JSON, 与 markdown 消息一起发送, 崩溃补发时也不会丢按钮
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		col := try.To1(app.FindCollectionByNameOrId(db.TableOutbox))
		if col.Fields.GetByName("keyboard") == nil {
			col.Fields.Add(&core.JSONField{
				Name: "keyboard", Id: ID("keyboard"),
				MaxSize: 64 << 10,
				Help:    "内嵌键盘(keyboard) JSON, 非空时随 markdown 消息一起发送",
			})
			try.To(app.Save(col))
		}
		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
