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

		bots := try.To1(app.FindCollectionByNameOrId(db.TableQQBots))

		msgs := core.NewBaseCollection(db.TableMessageMap, ID(db.TableMessageMap))
		msgs.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1,
			},
			&core.TextField{
				Name: "openid", Id: ID("openid"), System: true,
				Required: true,
			},
			&core.NumberField{
				Name: "num", Id: ID("num"), System: true,
				Required: true, OnlyInt: true,
			},
			&core.JSONField{
				Name: "extra", Id: ID("extra"), System: true,
				Required: false, MaxSize: 65536,
				Help: "场景与归属目标, 供后续撤回使用",
			},
		)
		addUpdatedFields(msgs)
		msgs.AddIndex("idx_msgmap_openid", true, "bot,openid", "")
		msgs.AddIndex("idx_msgmap_num", true, "bot,num", "")
		try.To(app.Save(msgs))

		// 把已有的 message 映射搬到新表, 保持 num 不变
		recs := try.To1(app.FindRecordsByFilter(db.TableIDMap, "kind = 'message'", "num", 0, 0))
		for _, rec := range recs {
			newRec := core.NewRecord(msgs)
			newRec.Set("bot", rec.GetString("bot"))
			newRec.Set("openid", rec.GetString("openid"))
			newRec.Set("num", rec.GetFloat("num"))
			if v := rec.Get("extra"); v != nil {
				newRec.Set("extra", v)
			}
			try.To(app.Save(newRec))
			try.To(app.Delete(rec))
		}

		// kind 收敛为 self / user / group
		idmapCol := try.To1(app.FindCollectionByNameOrId(db.TableIDMap))
		if f, ok := idmapCol.Fields.GetByName("kind").(*core.SelectField); ok {
			f.Values = []string{"self", "user", "group"}
		}
		try.To(app.Save(idmapCol))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
