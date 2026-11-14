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

		state := core.NewBaseCollection(db.TableWSState, ID(db.TableWSState))
		state.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1,
			},
			&core.TextField{
				Name: "session_id", Id: ID("session_id"), System: true,
				Required: false,
				Help:     "QQ 网关会话 id, 用于重启后 resume",
			},
			&core.NumberField{
				Name: "seq", Id: ID("seq"), System: true,
				Required: false, OnlyInt: true,
			},
		)
		addUpdatedFields(state)
		state.AddIndex("idx_wsstate_bot", true, "bot", "")
		try.To(app.Save(state))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
