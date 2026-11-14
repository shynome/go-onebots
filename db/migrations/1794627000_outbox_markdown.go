package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 出站队列支持 markdown 消息:
//   - markdown: 显式 markdown 内容, 非空时直接按 msg_type=2 发送
//   - segments: 文本+图片的原始段, 发送时拼成 markdown 以保证图片顺序
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		outbox := try.To1(app.FindCollectionByNameOrId(db.TableOutbox))
		if outbox.Fields.GetByName("markdown") == nil {
			outbox.Fields.Add(&core.TextField{
				Name: "markdown", Id: ID("markdown"),
				Max:  0,
				Help: "非空时按 markdown 消息(msg_type=2)发送",
			})
		}
		if outbox.Fields.GetByName("segments") == nil {
			outbox.Fields.Add(&core.JSONField{
				Name: "segments", Id: ID("segments"),
				MaxSize: 1 << 20,
				Help:    "非空时按 markdown 发送: 文本与图片按原顺序拼接",
			})
		}
		try.To(app.Save(outbox))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
