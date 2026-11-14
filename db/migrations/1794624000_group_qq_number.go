package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 把真实群号直接放在 onebot_id_map 的群记录上, 后台一眼就能填,
// 不再需要复制 openid 到单独的绑定表
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		idmapCol := try.To1(app.FindCollectionByNameOrId(db.TableIDMap))
		if idmapCol.Fields.GetByName("qq_number") == nil {
			// 不设 System, 便于以后还能调整
			idmapCol.Fields.Add(&core.NumberField{
				Name: "qq_number", Id: ID("qq_number"),
				OnlyInt: true,
				Help:    "群记录可填真实 QQ 群号, 填了之后收发都用真实群号",
			})
			try.To(app.Save(idmapCol))
		}

		// 迁移旧的 onebot_group_bind 数据(如果存在)
		if old, err := app.FindCollectionByNameOrId("onebot_group_bind"); err == nil {
			binds := try.To1(app.FindRecordsByFilter(old.Id, "", "", 0, 0))
			for _, b := range binds {
				recs, ferr := app.FindRecordsByFilter(db.TableIDMap,
					"bot = {:bot} && kind = 'group' && openid = {:openid}", "", 1, 0,
					dbx.Params{
						"bot":    b.GetString("bot"),
						"openid": b.GetString("group_openid"),
					})
				if ferr != nil || len(recs) == 0 {
					continue
				}
				recs[0].Set("qq_number", b.GetInt("qq_number"))
				if serr := app.Save(recs[0]); serr != nil {
					return serr
				}
			}
			try.To(app.Delete(old))
		}

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
