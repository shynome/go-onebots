package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 群号绑定表: QQ 官方 API 只给 group_openid, 不提供真实群号,
// 想让 send_group_msg 支持真实群号, 只能由管理员手工绑定
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		bots := try.To1(app.FindCollectionByNameOrId(db.TableQQBots))

		bind := core.NewBaseCollection(db.TableGroupBind, ID(db.TableGroupBind))
		bind.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1, CascadeDelete: true,
			},
			&core.NumberField{
				Name: "qq_number", Id: ID("qq_number"), System: true,
				Required: true, OnlyInt: true,
				Help: "真实 QQ 群号, 例如 123456789",
			},
			&core.TextField{
				Name: "group_openid", Id: ID("group_openid"), System: true,
				Required: true, Max: 128,
				Help: "该群对应的 group_openid, 可在 onebot_id_map(kind=group) 里查到",
			},
		)
		addUpdatedFields(bind)
		bind.AddIndex("idx_group_bind", true, "bot, qq_number", "")
		try.To(app.Save(bind))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
