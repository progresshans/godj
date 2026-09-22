package migrationgraph

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/schema/ir"
)

// References includes the models needed to interpret both stored and columnless
// relations. It does not treat a ManyToMany declaration as a synthetic FK.
func References(model ir.Model) []ir.ModelIdentity {
	var result []ir.ModelIdentity
	for _, field := range model.Fields {
		if field.Relation != nil {
			result = append(result, field.Relation.Target)
		}
	}
	for _, field := range model.ManyToMany {
		result = append(result, field.Target)
		if field.Through != nil {
			result = append(result, field.Through.Model)
		}
	}
	return result
}

// ChangedManyToMany admits one addition, removal or name-only change. All
// concrete storage and retained declarations must stay identical and ordered.
// A zero Before/After declaration denotes addition/removal respectively.
func (op MigrationOperation) ChangedManyToMany() (ir.ManyToManyField, ir.ManyToManyField, error) {
	var zero ir.ManyToManyField
	before, after := op.Before, op.After
	fail := func() (ir.ManyToManyField, ir.ManyToManyField, error) {
		return zero, zero, fmt.Errorf("ManyToMany delta must change one declaration while preserving model storage and retained bindings")
	}
	if op.Kind != MigrationAlterManyToMany || before.Name != after.Name || before.GoName != after.GoName || before.DBTable != after.DBTable ||
		!slices.EqualFunc(before.Fields, after.Fields, ir.Field.Equal) || !slices.EqualFunc(before.UniqueConstraints, after.UniqueConstraints, ir.UniqueConstraint.Equal) {
		return fail()
	}
	left, right := before.ManyToMany, after.ManyToMany
	if len(left) == len(right) {
		changed := -1
		for index := range left {
			if left[index].Equal(right[index]) {
				continue
			}
			if changed >= 0 || !ir.ManyToManyRename(left[index], right[index]) {
				return fail()
			}
			changed = index
		}
		if changed < 0 {
			return fail()
		}
		return left[changed].Clone(), right[changed].Clone(), nil
	}
	removed := len(left) > len(right)
	if removed {
		left, right = right, left
	}
	if len(right) != len(left)+1 {
		return fail()
	}
	position := 0
	for position < len(left) && left[position].Equal(right[position]) {
		position++
	}
	for index := position; index < len(left); index++ {
		if !left[index].Equal(right[index+1]) {
			return fail()
		}
	}
	if removed {
		return right[position].Clone(), zero, nil
	}
	return zero, right[position].Clone(), nil
}
