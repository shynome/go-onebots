package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// @全体 备用端: 主端(QQ 官方 API)因为无管理员权限或当日已达上限发不了 @全体 时,
// 由这条记录指向的备用 OneBot 实现补发一条只含 @全体 的消息。
// 备用端按群配置, 通过 onebot_id_map 的群记录字段引用。
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		col := core.NewBaseCollection(db.TableAtFallback, ID(db.TableAtFallback))
		col.Fields.Add(
			&core.TextField{
				Name: "base_url", Id: ID("base_url"), Required: true, Max: 2000,
				Help: "备用 OneBot 实现的 HTTP 地址, 如 http://127.0.0.1:3000",
			},
			&core.TextField{
				Name: "access_token", Id: ID("access_token"), Max: 512,
				Help: "可选, 调用备用端时带上 Authorization: Bearer <token>",
			},
		)
		addUpdatedFields(col)
		try.To(app.Save(col))

		idmapCol := try.To1(app.FindCollectionByNameOrId(db.TableIDMap))
		if idmapCol.Fields.GetByName("at_fallback") == nil {
			idmapCol.Fields.Add(&core.RelationField{
				Name: "at_fallback", Id: ID("at_fallback"),
				CollectionId: col.Id, MaxSelect: 1, CascadeDelete: true,
				Help: "群记录可选, 主端 @全体 发不出去时由该备用端补发",
			})
			try.To(app.Save(idmapCol))
		}

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
