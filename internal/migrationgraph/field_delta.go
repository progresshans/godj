package migrationgraph

import (
	"errors"
	"reflect"
	"slices"

	"github.com/progresshans/godj/schema/ir"
)

// ChangedField validates an AddField/RemoveField delta at any logical field
// position. It preserves the identity and exact order/metadata of every retained
// field. The returned field borrows the operation snapshot; callers must clone
// it before mutation. Normalization and resource admission belong to the caller.
func (operation MigrationOperation) ChangedField() (ir.Field, error) {
	smaller, larger := operation.Before, operation.After
	switch operation.Kind {
	case MigrationAddField:
	case MigrationRemoveField:
		smaller, larger = larger, smaller
	default:
		return ir.Field{}, errors.New("field delta requires AddField or RemoveField")
	}
	if smaller.Name != larger.Name || smaller.GoName != larger.GoName || smaller.DBTable != larger.DBTable || len(larger.Fields) != len(smaller.Fields)+1 || !slices.EqualFunc(smaller.ManyToMany, larger.ManyToMany, ir.ManyToManyField.Equal) || !slices.EqualFunc(smaller.UniqueConstraints, larger.UniqueConstraints, ir.UniqueConstraint.Equal) {
		return ir.Field{}, errors.New("field delta must change exactly one field in the same model")
	}
	position := 0
	for position < len(smaller.Fields) && reflect.DeepEqual(smaller.Fields[position], larger.Fields[position]) {
		position++
	}
	for index := position; index < len(smaller.Fields); index++ {
		if !reflect.DeepEqual(smaller.Fields[index], larger.Fields[index+1]) {
			return ir.Field{}, errors.New("field delta changes a retained field or its order")
		}
	}
	return larger.Fields[position], nil
}
