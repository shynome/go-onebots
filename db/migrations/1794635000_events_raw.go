package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 事件表增加 raw 字段: 保存 QQ 下发的原始消息 JSON
// event 存的是转成 OneBot 格式后的事件, 原始字段(如 message_scene)会丢
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		col := try.To1(app.FindCollectionByNameOrId(db.TableEvents))
		if col.Fields.GetByName("raw") == nil {
			col.Fields.Add(&core.JSONField{
				Name: "raw", Id: ID("raw"), MaxSize: 1 << 20,
				Help: "QQ 下发的原始消息 JSON",
			})
			try.To(app.Save(col))
		}
		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
