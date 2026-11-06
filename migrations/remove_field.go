package migrations

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// RemoveField retains the exact historical declaration and insertion anchor.
// Forward execution drops that column and its values. Reverse execution
// restores the declared column/default, not the removed values; required
// additions still need the backend's existing-table capability checks.
// Primary keys and retained constraint/relation/image dependencies cannot be
// removed. Dependent declarations must be changed explicitly beforehand.
type RemoveField struct {
	AppLabel, ModelName string
	Field               ir.Field
	BeforeField         string
}

func (RemoveField) operation()     {}
func (RemoveField) Kind() string   { return "RemoveField" }
func (op RemoveField) App() string { return op.AppLabel }

func (op RemoveField) inverse() (AddField, error) {
	if op.Field.PrimaryKey {
		return AddField{}, fmt.Errorf("RemoveField cannot remove a primary key")
	}
	return AddField{AppLabel: op.AppLabel, ModelName: op.ModelName, Field: op.Field, BeforeField: op.BeforeField}, nil
}

func (op RemoveField) stateForward(state ProjectState) (ProjectState, error) {
	inverse, err := op.inverse()
	if err != nil {
		return state, err
	}
	return inverse.stateBackward(state)
}
func (op RemoveField) stateBackward(state ProjectState) (ProjectState, error) {
	inverse, err := op.inverse()
	if err != nil {
		return state, err
	}
	return inverse.stateForward(state)
}
func (op RemoveField) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	inverse, err := op.inverse()
	if err != nil {
		return err
	}
	return inverse.databaseBackward(ctx, editor, from, to)
}
func (op RemoveField) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	inverse, err := op.inverse()
	if err != nil {
		return err
	}
	return inverse.databaseForward(ctx, editor, from, to)
}
