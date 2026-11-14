package migrations

import (
	"errors"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

var ErrNoRollback = errors.New("no rollback")

func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		bots := core.NewBaseCollection(db.TableQQBots, ID(db.TableQQBots))
		bots.Fields.Add(
			&core.TextField{
				Name: "name", Id: ID("name"), System: true,
				Required: true, Presentable: true,
			},
			&core.TextField{
				Name: "app_id", Id: ID("app_id"), System: true,
				Required: true,
			},
			&core.TextField{
				Name: "app_secret", Id: ID("app_secret"), System: true,
				Required: true, Hidden: true,
			},
			&core.BoolField{
				Name: "enabled", Id: ID("enabled"), System: true,
				Required: false,
			},
			&core.TextField{
				Name: "onebot_token", Id: ID("onebot_token"), System: true,
				Required: false,
				Help:     "OneBot WebSocket 鉴权 token, 留空则不校验",
			},
			&core.TextField{
				Name: "ws_status", Id: ID("ws_status"), System: true,
				Required: false,
			},
			&core.SelectField{
				Name: "do_action", Id: ID("do_action"), System: true,
				Required: false,
				Values:   []string{"stop", "restart"},
			},
		)
		addUpdatedFields(bots)
		try.To(app.Save(bots))

		idmap := core.NewBaseCollection(db.TableIDMap, ID(db.TableIDMap))
		idmap.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1,
			},
			&core.SelectField{
				Name: "kind", Id: ID("kind"), System: true,
				Required: true,
				Values:   []string{"self", "user", "group", "message"},
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
			},
		)
		addUpdatedFields(idmap)
		idmap.AddIndex("idx_idmap_openid", true, "bot,kind,openid", "")
		idmap.AddIndex("idx_idmap_num", true, "bot,kind,num", "")
		try.To(app.Save(idmap))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
