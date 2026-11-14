package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 出站消息队列: 先落库再发送, 进程崩溃后由定时任务补发
// 每条记录对应一条 QQ 消息(一条 OneBot 请求可能拆成多条)
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		bots := try.To1(app.FindCollectionByNameOrId(db.TableQQBots))

		outbox := core.NewBaseCollection(db.TableOutbox, ID(db.TableOutbox))
		outbox.Fields.Add(
			&core.RelationField{
				Name: "bot", Id: ID("bot"), System: true,
				Required:     true,
				CollectionId: bots.Id, MaxSelect: 1, CascadeDelete: true,
			},
			&core.SelectField{
				Name: "scene", Id: ID("scene"), System: true,
				Required: true, MaxSelect: 1,
				Values: []string{"group", "private"},
			},
			&core.TextField{
				Name: "target", Id: ID("target"), System: true,
				Required: true, Max: 128,
				Help: "群或用户的 openid",
			},
			&core.TextField{
				Name: "content", Id: ID("content"), System: true,
				Max:  5000,
				Help: "发给 QQ 的文本内容, 已完成 at 段转义",
			},
			&core.TextField{
				Name: "media_kind", Id: ID("media_kind"), System: true,
				Max:  16,
				Help: "image / record / video / file, 空表示纯文本",
			},
			&core.TextField{
				Name: "media_file", Id: ID("media_file"), System: true,
				Max:  2000,
				Help: "原始媒体引用(URL / base64:// / 本地路径)",
			},
			&core.TextField{
				Name: "media_url", Id: ID("media_url"), System: true,
				Max:  2000,
				Help: "首次解析出的可访问 URL, 重试时直接复用, 避免重复存文件中转",
			},
			&core.SelectField{
				Name: "state", Id: ID("state"), System: true,
				Required: true, MaxSelect: 1,
				Values: []string{"pending", "sent", "failed"},
			},
			&core.NumberField{
				Name: "attempts", Id: ID("attempts"), System: true,
				OnlyInt: true,
			},
			&core.DateField{
				Name: "next_retry_at", Id: ID("next_retry_at"), System: true,
			},
			&core.TextField{
				Name: "last_error", Id: ID("last_error"), System: true,
				Max: 1000,
			},
			&core.TextField{
				Name: "message_id", Id: ID("message_id"), System: true,
				Max:  128,
				Help: "QQ 返回的消息 id, 发送成功后回填",
			},
		)
		addUpdatedFields(outbox)
		outbox.AddIndex("idx_outbox_due", false, "bot, state, next_retry_at", "")
		try.To(app.Save(outbox))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
