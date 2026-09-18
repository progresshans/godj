package backend

import (
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

// ChangedChoiceField verifies the complete model delta. Backend-specific exact
// IR, identifiers, target bindings and physical catalog checks still apply.
func ChangedChoiceField(before, after ir.Model) (ir.Field, ir.Field, error) {
	if before.Name != after.Name || before.GoName != after.GoName || before.DBTable != after.DBTable || len(before.Fields) != len(after.Fields) {
		return ir.Field{}, ir.Field{}, fmt.Errorf("AlterField must preserve model identity and field count")
	}
	changed := -1
	for index := range before.Fields {
		if before.Fields[index].Equal(after.Fields[index]) {
			continue
		}
		if changed >= 0 {
			return ir.Field{}, ir.Field{}, fmt.Errorf("AlterField must change exactly one field")
		}
		if err := ir.ValidateChoiceChange(before.Fields[index], after.Fields[index]); err != nil {
			return ir.Field{}, ir.Field{}, err
		}
		changed = index
	}
	if changed < 0 {
		return ir.Field{}, ir.Field{}, fmt.Errorf("AlterField must change choices")
	}
	return before.Fields[changed], after.Fields[changed], nil
}
