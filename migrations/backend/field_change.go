package backend

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/schema/ir"
)

// ChangedField verifies the complete model delta. Backend-specific exact
// IR, identifiers, target bindings and physical catalog checks still apply.
// Returned fields are detached from both input models.
func ChangedField(before, after ir.Model) (ir.Field, ir.Field, ir.FieldChangeKind, error) {
	if before.Name != after.Name || before.GoName != after.GoName || before.DBTable != after.DBTable || len(before.Fields) != len(after.Fields) || !slices.EqualFunc(before.ManyToMany, after.ManyToMany, ir.ManyToManyField.Equal) || !slices.EqualFunc(before.UniqueConstraints, after.UniqueConstraints, ir.UniqueConstraint.Equal) {
		return ir.Field{}, ir.Field{}, 0, fmt.Errorf("AlterField must preserve model identity and field count")
	}
	changed := -1
	var kind ir.FieldChangeKind
	for index := range before.Fields {
		if before.Fields[index].Equal(after.Fields[index]) {
			continue
		}
		if changed >= 0 {
			return ir.Field{}, ir.Field{}, 0, fmt.Errorf("AlterField must change exactly one field")
		}
		var err error
		kind, err = ir.ClassifyFieldChange(before.Fields[index], after.Fields[index])
		if err != nil {
			return ir.Field{}, ir.Field{}, 0, err
		}
		changed = index
	}
	if changed < 0 {
		return ir.Field{}, ir.Field{}, 0, fmt.Errorf("AlterField must change one field")
	}
	return before.Fields[changed].Clone(), after.Fields[changed].Clone(), kind, nil
}
