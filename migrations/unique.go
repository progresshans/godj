package migrations

import "github.com/progresshans/godj/schema/ir"

func modelHasUniqueConstraints(model ir.Model) bool {
	if len(model.UniqueConstraints) != 0 {
		return true
	}
	for _, field := range model.Fields {
		if field.Unique {
			return true
		}
	}
	return false
}
