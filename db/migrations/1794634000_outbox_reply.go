package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 引用回复: OneBot 的 reply 段映射成 QQ 的 message_reference
// 值存的是入站事件 message_scene.ext 里的 msg_idx(REFIDX_...)
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		col := try.To1(app.FindCollectionByNameOrId(db.TableOutbox))
		if f, ok := col.Fields.GetByName("reply_to").(*core.TextField); ok {
			// 早期写入时限制过长度, msg_idx 很长, 去掉限制
			f.Max = 0
			try.To(app.Save(col))
		} else if col.Fields.GetByName("reply_to") == nil {
			col.Fields.Add(&core.TextField{
				Name: "reply_to", Id: ID("reply_to"),
				Help: "引用回复的 msg_idx, 空表示不引用",
			})
			try.To(app.Save(col))
		}
		// msg_seq 属于被动回复方案, 已废弃
		if f := col.Fields.GetByName("msg_seq"); f != nil {
			f.SetSystem(false)
			col.Fields.RemoveById(f.GetId())
			try.To(app.Save(col))
		}
		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
