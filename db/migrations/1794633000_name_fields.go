package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"github.com/shynome/go-onebots/db"
)

// 给映射表与 @全体 备用端表加备注名, 方便在后台一眼认出是哪个人 / 哪个群 / 哪个实例
func init() {
	migrations.Register(func(app core.App) (err error) {
		defer err0.Then(&err, nil, nil)

		addName := func(table, help string) error {
			col, err := app.FindCollectionByNameOrId(table)
			if err != nil {
				return err
			}
			if col.Fields.GetByName("name") != nil {
				return nil
			}
			col.Fields.Add(&core.TextField{
				Name: "name", Id: ID(table + "_name"), Max: 100, Help: help,
			})
			return app.Save(col)
		}

		try.To(addName(db.TableIDMap, "备注名, 仅用于在后台识别这条映射(群名 / 昵称等)"))
		try.To(addName(db.TableAtFallback, "备注名, 如\"备用机器人的 NapCat 实例\""))

		// 新字段默认追加在末尾, 按可读顺序重排
		try.To(reorderFields(app, db.TableIDMap, []string{
			"id", "bot", "kind", "name", "openid", "num", "qq_number", "at_fallback", "extra",
		}))
		try.To(reorderFields(app, db.TableAtFallback, []string{
			"id", "name", "base_url", "access_token",
		}))
		return nil
	}, func(app core.App) error {
		return ErrNoRollback
	})
}

// reorderFields 按给定顺序重排字段, 未列出的字段保持原有相对顺序排在后面
func reorderFields(app core.App, table string, order []string) error {
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
	for _, f := range col.Fields {
		if !used[f.GetName()] {
			next = append(next, f)
		}
	}
	col.Fields = next
	return app.Save(col)
}
