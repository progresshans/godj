package migrations

import "github.com/progresshans/godj/schema/ir"

func modelHasUniqueFields(model ir.Model) bool {
	for _, field := range model.Fields {
		if field.Unique {
			return true
		}
	}
	return false
}
