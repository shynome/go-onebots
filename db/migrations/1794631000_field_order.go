package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 调整后台字段展示顺序: 定位信息 -> 内容 -> 媒体 -> 状态 -> 结果
// 只重排顺序, 不动字段本身
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		reorder := func(table string, order []string) error {
			col, err := app.FindCollectionByNameOrId(table)
			if err != nil {
				return err
			}

			byName := make(map[string]core.Field, len(col.Fields))
			for _, f := range col.Fields {
				byName[f.GetName()] = f
			}

			next := make([]core.Field, 0, len(col.Fields))
			used := make(map[string]bool, len(order))
			for _, name := range order {
				if f, ok := byName[name]; ok && !used[name] {
					next = append(next, f)
					used[name] = true
				}
			}
			// 未列出的字段(created/updated 等)保持原顺序追加
			for _, f := range col.Fields {
				if !used[f.GetName()] {
					next = append(next, f)
				}
			}

			col.Fields = next
			return app.Save(col)
		}

		try.To(reorder(db.TableOutbox, []string{
			"id", "bot", "scene", "target",
			"content", "markdown", "segments",
			"media_kind", "media_url", "media_file",
			"message_id", "state", "attempts", "next_retry_at", "last_error",
		}))
		try.To(reorder(db.TableEvents, []string{
			"id", "bot", "seq", "message_type",
			"message_id", "user_id", "group_id",
			"content", "delivered", "delivered_at", "event",
		}))
		try.To(reorder(db.TableMedia, []string{
			"id", "bot", "kind", "source", "width", "height", "file", "key",
		}))
		try.To(reorder(db.TableIDMap, []string{
			"id", "bot", "kind", "openid", "num", "qq_number", "extra",
		}))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
