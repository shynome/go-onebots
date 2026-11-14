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

		media := core.NewBaseCollection(db.TableMedia, ID(db.TableMedia))
		media.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1,
			},
			&core.TextField{
				Name: "key", Id: ID("key"), System: true,
				Required: true, Presentable: true,
			},
			&core.SelectField{
				Name: "kind", Id: ID("kind"), System: true,
				Required: false,
				Values:   []string{"image", "record", "video", "file"},
			},
			&core.NumberField{
				Name: "size", Id: ID("size"), System: true,
				Required: false, OnlyInt: true,
			},
			&core.TextField{
				Name: "content_type", Id: ID("content_type"), System: true,
				Required: false,
			},
		)
		addUpdatedFields(media)
		media.AddIndex("idx_media_key", true, "key", "")
		media.AddIndex("idx_media_bot_created", false, "bot,created", "")
		try.To(app.Save(media))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
