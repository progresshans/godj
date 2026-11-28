package migrations

import (
	"context"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// AlterManyToMany changes only the input blank policy of a columnless field.
// Names, ordering, target, through bindings and owned storage remain identical.
type AlterManyToMany struct {
	AppLabel, ModelName string
	Before, After       ir.ManyToManyField
}

func (AlterManyToMany) operation()     {}
func (AlterManyToMany) Kind() string   { return "AlterManyToMany" }
func (op AlterManyToMany) App() string { return op.AppLabel }

func (op AlterManyToMany) stateForward(state ProjectState) (ProjectState, error) {
	return changeManyState(state, op, false)
}

func (op AlterManyToMany) stateBackward(state ProjectState) (ProjectState, error) {
	return changeManyState(state, op, true)
}

func (op AlterManyToMany) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}

func (op AlterManyToMany) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}
