package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// ID 生成稳定且可读的字段 id, 便于后续 migration 引用
func ID(name string) string {
	return fmt.Sprintf("__%s__", name)
}

func addUpdatedFields(collection *core.Collection) {
	collection.Fields.Add(
		&core.AutodateField{
			Id: ID("created"), Name: "created", System: true,
			OnCreate: true,
		},
		&core.AutodateField{
			Id: ID("updated"), Name: "updated", System: true,
			OnCreate: true,
			OnUpdate: true,
		},
	)
}
