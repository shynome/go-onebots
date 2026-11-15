package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 中转媒体增加 last_used: 最近一次被使用的时刻
// 清理不再按 created(上传时间), 改为按 last_used, 被反复引用的图片不会被误删
// 旧数据从未记录过使用时间, 用 updated 回填(最近一次写库时间)兜底
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		media := try.To1(app.FindCollectionByNameOrId(db.TableMedia))
		if media.Fields.GetByName("last_used") == nil {
			media.Fields.Add(&core.DateField{
				Name: "last_used", Id: ID("last_used"),
				Help: "最近一次被使用的时刻, 媒体清理以此为基准",
			})
			try.To(app.Save(media))
		}

		// 旧数据没有使用记录, 拿 updated 顶替, 否则空值会被当成"很久没用"
		_, err = app.DB().
			NewQuery("UPDATE " + db.TableMedia + " SET last_used = updated WHERE last_used IS NULL OR last_used = ''").
			Execute()
		try.To(err)

		media = try.To1(app.FindCollectionByNameOrId(db.TableMedia))
		media.AddIndex("idx_media_last_used", false, "last_used", "")
		try.To(app.Save(media))

		// 新字段默认追加在末尾, 按可读顺序重排
		try.To(reorderFields(app, db.TableMedia, []string{
			"id", "bot", "kind", "source", "width", "height", "file", "key", "last_used",
		}))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
