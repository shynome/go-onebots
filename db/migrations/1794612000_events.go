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

		events := core.NewBaseCollection(db.TableEvents, ID(db.TableEvents))
		events.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1,
			},
			&core.NumberField{
				Name: "seq", Id: ID("seq"), System: true,
				Required: true, OnlyInt: true,
			},
			&core.SelectField{
				Name: "message_type", Id: ID("message_type"), System: true,
				Required: false,
				Values:   []string{"private", "group"},
			},
			&core.NumberField{
				Name: "user_id", Id: ID("user_id"), System: true,
				Required: false, OnlyInt: true,
			},
			&core.NumberField{
				Name: "group_id", Id: ID("group_id"), System: true,
				Required: false, OnlyInt: true,
			},
			&core.NumberField{
				Name: "message_id", Id: ID("message_id"), System: true,
				Required: false, OnlyInt: true,
			},
			&core.TextField{
				Name: "content", Id: ID("content"), System: true,
				Required: false, Max: 4000,
			},
			&core.JSONField{
				Name: "event", Id: ID("event"), System: true,
				Required: false, MaxSize: 65536,
				Help: "完整的 OneBot v11 事件, 用于重连补发",
			},
			&core.BoolField{
				Name: "delivered", Id: ID("delivered"), System: true,
				Required: false,
			},
			&core.DateField{
				Name: "delivered_at", Id: ID("delivered_at"), System: true,
				Required: false,
			},
		)
		addUpdatedFields(events)
		events.AddIndex("idx_events_bot_seq", true, "bot,seq", "")
		events.AddIndex("idx_events_undelivered", false, "bot,delivered", "")
		try.To(app.Save(events))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
