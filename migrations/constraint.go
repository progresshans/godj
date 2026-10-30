package migrations

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// AddConstraint adds a named unique constraint over existing logical fields.
// Member order is historical metadata; declaration-list order is canonical.
type AddConstraint struct {
	AppLabel   string
	ModelName  string
	Constraint ir.UniqueConstraint
}

// RemoveConstraint retains the complete historical definition so removal and
// reversal cannot silently adopt a different constraint with the same name.
type RemoveConstraint struct {
	AppLabel   string
	ModelName  string
	Constraint ir.UniqueConstraint
}

func (AddConstraint) operation()        {}
func (AddConstraint) Kind() string      { return "AddConstraint" }
func (op AddConstraint) App() string    { return op.AppLabel }
func (RemoveConstraint) operation()     {}
func (RemoveConstraint) Kind() string   { return "RemoveConstraint" }
func (op RemoveConstraint) App() string { return op.AppLabel }

func (op AddConstraint) stateForward(state ProjectState) (ProjectState, error) {
	return changeConstraintState(state, op.AppLabel, op.ModelName, op.Constraint, true)
}

func (op AddConstraint) stateBackward(state ProjectState) (ProjectState, error) {
	return changeConstraintState(state, op.AppLabel, op.ModelName, op.Constraint, false)
}

func (op RemoveConstraint) stateForward(state ProjectState) (ProjectState, error) {
	return changeConstraintState(state, op.AppLabel, op.ModelName, op.Constraint, false)
}

func (op RemoveConstraint) stateBackward(state ProjectState) (ProjectState, error) {
	return changeConstraintState(state, op.AppLabel, op.ModelName, op.Constraint, true)
}

func changeConstraintState(state ProjectState, app, name string, constraint ir.UniqueConstraint, add bool) (ProjectState, error) {
	schema, exists := state.Schema(app)
	if exists {
		for index, model := range schema.Models {
			if model.Name != name {
				continue
			}
			changed, err := changedConstraintModel(app, model, constraint, add)
			if err != nil {
				return state, err
			}
			schema.Models[index] = changed
			return state.withSchema(schema), nil
		}
	}
	return state, fmt.Errorf("constraint model %s.%s does not exist", app, name)
}

func changedConstraintModel(app string, model ir.Model, constraint ir.UniqueConstraint, add bool) (ir.Model, error) {
	position := -1
	for index, existing := range model.UniqueConstraints {
		if existing.Name == constraint.Name {
			position = index
			break
		}
	}
	if add && position >= 0 {
		return ir.Model{}, fmt.Errorf("constraint %s.%s.%s already exists", app, model.Name, constraint.Name)
	}
	if !add && (position < 0 || !model.UniqueConstraints[position].Equal(constraint)) {
		return ir.Model{}, fmt.Errorf("constraint %s.%s.%s is missing or differs from the historical definition", app, model.Name, constraint.Name)
	}
	// Operation views may still borrow the previous slice. Detach it before
	// insertion or deletion, then normalize and own all nested member lists.
	model.UniqueConstraints = slices.Clone(model.UniqueConstraints)
	if add {
		model.UniqueConstraints = append(model.UniqueConstraints, constraint)
	} else {
		model.UniqueConstraints = slices.Delete(model.UniqueConstraints, position, position+1)
	}
	return normalizedSingleModel(app, model)
}

func (builder *loadedStateBuilder) changeConstraint(app, name string, constraint ir.UniqueConstraint, add bool) error {
	model, exists := builder.model(loadedModelIdentity{app: app, model: name})
	if !exists {
		return fmt.Errorf("constraint model %s.%s does not exist", app, name)
	}
	changed, err := changedConstraintModel(app, model.value, constraint, add)
	if err != nil {
		return err
	}
	model.value = changed
	return nil
}

func (op AddConstraint) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, _ ProjectState) error {
	return changeDatabaseConstraint(ctx, editor, from, op.AppLabel, op.ModelName, op.Constraint, true)
}

func (op AddConstraint) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, _ ProjectState) error {
	return changeDatabaseConstraint(ctx, editor, from, op.AppLabel, op.ModelName, op.Constraint, false)
}

func (op RemoveConstraint) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, _ ProjectState) error {
	return changeDatabaseConstraint(ctx, editor, from, op.AppLabel, op.ModelName, op.Constraint, false)
}

func (op RemoveConstraint) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, _ ProjectState) error {
	return changeDatabaseConstraint(ctx, editor, from, op.AppLabel, op.ModelName, op.Constraint, true)
}

func changeDatabaseConstraint(ctx context.Context, editor backend.SchemaEditor, from ProjectState, app, name string, constraint ir.UniqueConstraint, add bool) error {
	model, exists := from.Model(app, name)
	if !exists {
		return fmt.Errorf("constraint source model %s.%s is missing", app, name)
	}
	if add {
		return editor.AddConstraint(ctx, model, constraint.Clone())
	}
	return editor.RemoveConstraint(ctx, model, constraint.Clone())
}
