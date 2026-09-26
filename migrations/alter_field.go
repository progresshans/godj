package migrations

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// AlterField carries both historical values so forward and backward execution
// reject stale metadata instead of guessing a previous definition. The current
// operation supports choices, uniqueness, relation cardinality/reverse namespace
// and exact Decimal precision changes, with separate backend capabilities.
type AlterField struct {
	AppLabel  string
	ModelName string
	Before    ir.Field
	After     ir.Field
}

func (AlterField) operation()     {}
func (AlterField) Kind() string   { return "AlterField" }
func (op AlterField) App() string { return op.AppLabel }

func (op AlterField) normalized() (AlterField, error) {
	before, err := normalizeLoadedAddedField(op.AppLabel, op.Before)
	if err != nil {
		return AlterField{}, fmt.Errorf("normalize previous field: %w", err)
	}
	after, err := normalizeLoadedAddedField(op.AppLabel, op.After)
	if err != nil {
		return AlterField{}, fmt.Errorf("normalize changed field: %w", err)
	}
	if _, err := ir.ClassifyFieldChange(before, after); err != nil {
		return AlterField{}, err
	}
	op.Before, op.After = before, after
	return op, nil
}

func (op AlterField) stateForward(state ProjectState) (ProjectState, error) {
	return op.replaceState(state, false)
}

func (op AlterField) stateBackward(state ProjectState) (ProjectState, error) {
	return op.replaceState(state, true)
}

func (op AlterField) replaceState(state ProjectState, reverse bool) (ProjectState, error) {
	normalized, err := op.normalized()
	if err != nil {
		return state, err
	}
	before, after := normalized.Before, normalized.After
	if reverse {
		before, after = after, before
	}
	schema, exists := state.Schema(op.AppLabel)
	if !exists {
		return state, fmt.Errorf("app %s does not exist", op.AppLabel)
	}
	for modelIndex := range schema.Models {
		model := &schema.Models[modelIndex]
		if model.Name != op.ModelName {
			continue
		}
		for fieldIndex, field := range model.Fields {
			if field.Name == before.Name {
				if !field.Equal(before) {
					return state, fmt.Errorf("field %s.%s.%s differs from AlterField source", op.AppLabel, op.ModelName, before.Name)
				}
				model.Fields[fieldIndex] = after.Clone()
				return state.withSchema(schema), nil
			}
		}
	}
	return state, fmt.Errorf("field %s.%s.%s does not exist", op.AppLabel, op.ModelName, before.Name)
}

func (op AlterField) databaseForward(ctx context.Context, editor backend.SchemaEditor, from ProjectState, _ ProjectState) error {
	op, err := op.normalized()
	if err != nil {
		return err
	}
	model, exists := from.Model(op.AppLabel, op.ModelName)
	if !exists {
		return fmt.Errorf("AlterField source model is missing")
	}
	return editor.AlterField(ctx, model, op.Before, op.After)
}

func (op AlterField) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from ProjectState, _ ProjectState) error {
	op, err := op.normalized()
	if err != nil {
		return err
	}
	model, exists := from.Model(op.AppLabel, op.ModelName)
	if !exists {
		return fmt.Errorf("AlterField source model is missing")
	}
	return editor.AlterField(ctx, model, op.After, op.Before)
}

func (builder *loadedStateBuilder) alterField(operation AlterField, reverse bool) error {
	operation, err := operation.normalized()
	if err != nil {
		return err
	}
	before, after := operation.Before, operation.After
	if reverse {
		before, after = after, before
	}
	model, exists := builder.model(loadedModelIdentity{app: operation.AppLabel, model: operation.ModelName})
	if !exists {
		return fmt.Errorf("AlterField model %s.%s does not exist", operation.AppLabel, operation.ModelName)
	}
	index, exists := model.fieldNames[before.Name]
	if !exists || !model.value.Fields[index].Equal(before) {
		return fmt.Errorf("AlterField source field %s.%s.%s is missing or changed", operation.AppLabel, operation.ModelName, before.Name)
	}
	if before.Relation != nil && after.Relation != nil && before.Relation.Reverse != after.Relation.Reverse {
		if err := builder.alterReverseNamespace(loadedModelIdentity{app: operation.AppLabel, model: operation.ModelName}, before, after); err != nil {
			return err
		}
	}
	// Materialized operation views borrow the previous immutable field slice.
	// Replacing an element must detach that slice before publishing the new
	// field, or the sealed before-state would become the after-state as well.
	model.value.Fields = slices.Clone(model.value.Fields)
	model.value.Fields[index] = after.Clone()
	return nil
}

// The classifier preserves target, delete policy and source identity. Only
// the reverse namespace changes here; incoming ownership and counts do not.
// Validate the complete replacement before publishing any map mutation.
func (builder *loadedStateBuilder) alterReverseNamespace(source loadedModelIdentity, before, after ir.Field) error {
	target := loadedModelIdentity{app: before.Relation.Target.AppLabel, model: before.Relation.Target.ModelName}
	model, exists := builder.model(target)
	if !exists {
		return fmt.Errorf("AlterField relation target is missing")
	}
	owner := loadedReverseOwner{source: source, field: before.Name}
	if _, exists := builder.incoming[target][owner]; !exists {
		return fmt.Errorf("AlterField incoming relation owner is inconsistent")
	}
	owners := builder.reverse[target]
	oldName, newName := before.Relation.Reverse.Name, after.Relation.Reverse.Name
	if oldName != "" && owners[oldName] != owner {
		return fmt.Errorf("AlterField reverse relation owner is inconsistent")
	}
	if newName != "" {
		if _, collision := model.fieldNames[newName]; collision {
			return fmt.Errorf("AlterField reverse name collides with target field %s", newName)
		}
		if _, collision := owners[newName]; collision {
			return fmt.Errorf("AlterField reverse name collides with relation %s", newName)
		}
	}
	updated := make(map[string]loadedReverseOwner, len(owners)+1)
	for name, value := range owners {
		if name != oldName {
			updated[name] = value
		}
	}
	if newName != "" {
		updated[newName] = owner
	}
	if len(updated) == 0 {
		delete(builder.reverse, target)
	} else {
		builder.reverse[target] = updated
	}
	return nil
}
