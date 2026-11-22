package migrations

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// RenameField is an authored assertion that two identities are the same
// historical field. The before and after declarations also bind the physical
// columns, including an explicitly retained database column.
type RenameField AlterField

func (RenameField) operation()     {}
func (RenameField) Kind() string   { return "RenameField" }
func (op RenameField) App() string { return op.AppLabel }

func (op RenameField) fields(reverse bool) (ir.Field, ir.Field) {
	if reverse {
		return op.After, op.Before
	}
	return op.Before, op.After
}

func (op RenameField) stateForward(state ProjectState) (ProjectState, error) {
	return op.replaceState(state, false)
}
func (op RenameField) stateBackward(state ProjectState) (ProjectState, error) {
	return op.replaceState(state, true)
}

func (op RenameField) replaceState(state ProjectState, reverse bool) (ProjectState, error) {
	before, after := op.fields(reverse)
	source, exists := state.Model(op.AppLabel, op.ModelName)
	if !exists {
		return state, fmt.Errorf("RenameField model %s.%s is missing", op.AppLabel, op.ModelName)
	}
	renamed, err := ir.RenameModelField(op.AppLabel, source, before, after)
	if err != nil {
		return state, err
	}
	out := state.Clone()
	id := ir.ModelIdentity{AppLabel: op.AppLabel, ModelName: op.ModelName}
	for app, s := range out.apps {
		for i, m := range s.Models {
			if app == op.AppLabel && m.Name == op.ModelName {
				s.Models[i] = renamed
				continue
			}
			if changed, ok := ir.RenameThroughField(m, id, before.Name, after.Name); ok {
				s.Models[i] = changed
			}
		}
	}
	return out, nil
}

func (op RenameField) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return op.rename(ctx, editor, from, false)
}
func (op RenameField) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return op.rename(ctx, editor, from, true)
}

func (op RenameField) rename(ctx context.Context, editor backend.SchemaEditor, from ProjectState, reverse bool) error {
	capable, ok := editor.(backend.FieldRenameEditor)
	if !ok {
		return backend.NewCapabilityError("rename_field", "schema editor does not support RenameField", nil)
	}
	model, ok := from.Model(op.AppLabel, op.ModelName)
	if !ok {
		return fmt.Errorf("RenameField source model is missing")
	}
	before, after := op.fields(reverse)
	return capable.RenameField(ctx, model, before, after)
}

func (builder *loadedStateBuilder) renameField(op RenameField, reverse bool) error {
	id := loadedModelIdentity{app: op.AppLabel, model: op.ModelName}
	model, ok := builder.model(id)
	if !ok {
		return fmt.Errorf("missing RenameField model %s.%s", id.app, id.model)
	}
	before, after := op.fields(reverse)
	result, err := ir.RenameModelField(id.app, model.value, before, after)
	if err != nil {
		return err
	}
	if _, taken := builder.reverse[id][after.Name]; taken {
		return fmt.Errorf("RenameField name %s is owned by an incoming relation", after.Name)
	}
	if before.Relation != nil {
		target := loadedModelIdentity{app: before.Relation.Target.AppLabel, model: before.Relation.Target.ModelName}
		oldOwner, newOwner := loadedReverseOwner{source: id, field: before.Name}, loadedReverseOwner{source: id, field: after.Name}
		incoming := builder.incoming[target]
		if _, ok := incoming[oldOwner]; !ok {
			return fmt.Errorf("missing incoming RenameField owner")
		}
		if _, exists := incoming[newOwner]; exists {
			return fmt.Errorf("duplicate incoming RenameField owner")
		}
		name := before.Relation.Reverse.Name
		if name != "" && builder.reverse[target][name] != oldOwner {
			return fmt.Errorf("invalid reverse RenameField owner")
		}
		delete(incoming, oldOwner)
		incoming[newOwner] = struct{}{}
		if name != "" {
			builder.reverse[target][name] = newOwner
		}
		// Every owner of an explicit through field is the target of the
		// intermediary's source FK, including self-referential links.
		for _, field := range model.value.Fields {
			if field.Relation == nil {
				continue
			}
			ownerID := field.Relation.Target
			if ownerID.AppLabel == id.app && ownerID.ModelName == id.model {
				continue
			}
			if owner, ok := builder.model(loadedModelIdentity{app: ownerID.AppLabel, model: ownerID.ModelName}); ok {
				if changed, ok := ir.RenameThroughField(owner.value, ir.ModelIdentity{AppLabel: id.app, ModelName: id.model}, before.Name, after.Name); ok {
					owner.value = changed
				}
			}
		}
	}
	key := model.primaryKey
	if before.PrimaryKey {
		key = after
	}
	builder.apps[id.app].models[id.model] = newLoadedStateModel(result, key)
	return nil
}

// A PK column rename also updates existing FK constraints in other tables.
// Include explicit and automatic intermediary owners in the sealed graph.
func (builder *loadedStateBuilder) primaryRenameRoots(ctx context.Context, roots []migrationgraph.MigrationModel, key ir.ModelIdentity) ([]migrationgraph.MigrationModel, error) {
	seen := map[ir.ModelIdentity]bool{key: true}
	add := func(id loadedModelIdentity) error {
		identity := ir.ModelIdentity{AppLabel: id.app, ModelName: id.model}
		if seen[identity] {
			return nil
		}
		if len(seen) >= loadedDerivedIntentMaxTargets {
			return fmt.Errorf("primary-key rename exceeds the historical model limit")
		}
		model, ok := builder.storageModel(id)
		if !ok {
			return fmt.Errorf("missing historical incoming model %s.%s", id.app, id.model)
		}
		roots = append(roots, migrationgraph.MigrationModel{AppLabel: id.app, Model: model})
		seen[identity] = true
		return nil
	}
	for owner := range builder.incoming[loadedModelIdentity{app: key.AppLabel, model: key.ModelName}] {
		if err := add(owner.source); err != nil {
			return nil, err
		}
	}
	if builder.manyCount != 0 {
		for app, group := range builder.apps {
			for name, model := range group.models {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				for _, field := range model.value.ManyToMany {
					if field.Target == key || field.Through != nil && field.Through.Model == key {
						if err := add(loadedModelIdentity{app: app, model: name}); err != nil {
							return nil, err
						}
						break
					}
				}
			}
		}
	}
	slices.SortFunc(roots[1:], func(a, b migrationgraph.MigrationModel) int { return compareDataModel(a.Identity(), b.Identity()) })
	return roots, nil
}
