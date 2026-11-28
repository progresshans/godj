package migrations

import "github.com/progresshans/godj/schema/ir"

func modelHasColumnIndexes(model ir.Model) bool {
	for _, field := range model.Fields {
		if field.DBIndex {
			return true
		}
	}
	return false
}
