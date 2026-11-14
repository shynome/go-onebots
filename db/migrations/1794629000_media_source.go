package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 中转媒体按来源去重: 同一个图片链接只下载/转存一次
// source 存原始来源(url:xxx 或 sha256:xxx), width/height 缓存图片尺寸
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		media := try.To1(app.FindCollectionByNameOrId(db.TableMedia))
		if media.Fields.GetByName("source") == nil {
			media.Fields.Add(&core.TextField{
				Name: "source", Id: ID("source"), Max: 2000,
				Help: "原始来源(URL 或内容 sha256), 用于跨消息去重",
			})
		}
		if media.Fields.GetByName("width") == nil {
			media.Fields.Add(&core.NumberField{
				Name: "width", Id: ID("width"), OnlyInt: true,
				Help: "图片宽度(px), markdown 消息必须带尺寸",
			})
		}
		if media.Fields.GetByName("height") == nil {
			media.Fields.Add(&core.NumberField{
				Name: "height", Id: ID("height"), OnlyInt: true,
				Help: "图片高度(px)",
			})
		}
		media.AddIndex("idx_media_source", false, "bot, source", "")
		try.To(app.Save(media))

		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}
