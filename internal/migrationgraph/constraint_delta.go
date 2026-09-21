package migrationgraph

import (
	"errors"
	"slices"

	"github.com/progresshans/godj/schema/ir"
)

// ChangedConstraint verifies a single named constraint delta with unchanged
// model identity, fields and retained constraints. The returned member list
// borrows the admitted intent; callers clone it before mutation. Exact IR
// normalization and resource admission belong to the caller.
func (operation MigrationOperation) ChangedConstraint() (ir.UniqueConstraint, error) {
	smaller, larger := operation.Before, operation.After
	switch operation.Kind {
	case MigrationAddConstraint:
	case MigrationRemoveConstraint:
		smaller, larger = larger, smaller
	default:
		return ir.UniqueConstraint{}, errors.New("constraint delta requires AddConstraint or RemoveConstraint")
	}
	if smaller.Name != larger.Name || smaller.GoName != larger.GoName || smaller.DBTable != larger.DBTable ||
		!slices.EqualFunc(smaller.Fields, larger.Fields, ir.Field.Equal) || len(larger.UniqueConstraints) != len(smaller.UniqueConstraints)+1 {
		return ir.UniqueConstraint{}, errors.New("constraint delta must change exactly one constraint in the same model")
	}
	position := 0
	for position < len(smaller.UniqueConstraints) && smaller.UniqueConstraints[position].Equal(larger.UniqueConstraints[position]) {
		position++
	}
	for index := position; index < len(smaller.UniqueConstraints); index++ {
		if !smaller.UniqueConstraints[index].Equal(larger.UniqueConstraints[index+1]) {
			return ir.UniqueConstraint{}, errors.New("constraint delta changes a retained constraint or its order")
		}
	}
	return larger.UniqueConstraints[position], nil
}
