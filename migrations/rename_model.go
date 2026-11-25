package migrations

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// RenameModel identifies the same historical rows under a new model name.
// Both normalized declarations bind the physical table and owned relations.
type RenameModel struct {
	AppLabel string
	Before   ir.Model
	After    ir.Model
}

func (RenameModel) operation()     {}
func (RenameModel) Kind() string   { return "RenameModel" }
func (op RenameModel) App() string { return op.AppLabel }

func (op RenameModel) models(reverse bool) (ir.Model, ir.Model) {
	if reverse {
		return op.After, op.Before
	}
	return op.Before, op.After
}

func (op RenameModel) stateForward(state ProjectState) (ProjectState, error) {
	return op.replaceState(state, false)
}

func (op RenameModel) stateBackward(state ProjectState) (ProjectState, error) {
	return op.replaceState(state, true)
}

func (op RenameModel) replaceState(state ProjectState, reverse bool) (ProjectState, error) {
	before, after := op.models(reverse)
	change, err := migrationgraph.NewModelRename(op.AppLabel, before, after)
	if err != nil {
		return state, err
	}
	actual, exists := state.Model(op.AppLabel, before.Name)
	if !exists || !actual.Equal(before) {
		return state, fmt.Errorf("model %s.%s differs from its rename source", op.AppLabel, before.Name)
	}
	out := state
	out.apps = maps.Clone(state.apps)
	for _, app := range state.Apps() {
		group := state.apps[app]
		changed := false
		for i, model := range group.Models {
			var next ir.Model
			if app == op.AppLabel && model.Name == before.Name {
				next = after
			} else {
				if err := change.CheckOtherStorage(model); err != nil {
					return state, err
				}
				var uses bool
				next, uses = change.RebindReferences(model)
				if !uses {
					continue
				}
			}
			if !changed {
				group.Models = slices.Clone(group.Models)
				changed = true
			}
			group.Models[i] = next
		}
		if changed {
			out.apps[app], err = ir.Normalize(group)
			if err != nil {
				return state, err
			}
		}
	}
	return out, nil
}

func (op RenameModel) databaseForward(ctx context.Context, editor backend.SchemaEditor, _, _ ProjectState) error {
	return op.edit(ctx, editor, false)
}

func (op RenameModel) databaseBackward(ctx context.Context, editor backend.SchemaEditor, _, _ ProjectState) error {
	return op.edit(ctx, editor, true)
}

func (op RenameModel) edit(ctx context.Context, editor backend.SchemaEditor, reverse bool) error {
	capable, ok := editor.(backend.ModelRenameEditor)
	if !ok {
		return backend.NewCapabilityError("rename_model", "schema editor does not support RenameModel", nil)
	}
	before, after := op.models(reverse)
	return capable.RenameModel(ctx, before.Clone(), after.Clone())
}

func (builder *loadedStateBuilder) renameModel(op RenameModel, reverse bool) error {
	before, after := op.models(reverse)
	change, err := migrationgraph.NewModelRename(op.AppLabel, before, after)
	if err != nil {
		return err
	}
	oldID := loadedModelIdentity{app: op.AppLabel, model: before.Name}
	stored, exists := builder.model(oldID)
	if !exists || !stored.value.Equal(before) {
		return fmt.Errorf("model %s.%s differs from its rename source", oldID.app, oldID.model)
	}
	app := builder.apps[op.AppLabel]
	if _, taken := app.models[after.Name]; taken ||
		after.GoName != before.GoName && app.goNames[after.GoName] != "" ||
		after.DBTable != before.DBTable && app.dbTables[after.DBTable] != "" {
		return fmt.Errorf("rename destination %s.%s is occupied", op.AppLabel, after.Name)
	}
	// Remove the old reverse owners before publishing any of the new ones.
	// This also handles self-edges and cycles between different apps.
	type edit struct {
		source   loadedModelIdentity
		from, to ir.Model
	}
	edits := []edit{{oldID, stored.value, after.Clone()}}
	for _, label := range slices.Sorted(maps.Keys(builder.apps)) {
		group := builder.apps[label]
		for _, name := range group.order {
			id := loadedModelIdentity{app: label, model: name}
			if id == oldID {
				continue
			}
			model := group.models[name].value
			if err := change.CheckOtherStorage(model); err != nil {
				return err
			}
			if next, changed := change.RebindReferences(model); changed {
				edits = append(edits, edit{id, model, next})
			}
		}
	}
	for _, e := range edits {
		for _, field := range e.from.Fields {
			if err := builder.removeRelation(e.source, field); err != nil {
				return err
			}
		}
	}
	delete(app.models, before.Name)
	delete(app.goNames, before.GoName)
	delete(app.dbTables, before.DBTable)
	position := slices.Index(app.order, before.Name)
	if position < 0 {
		return fmt.Errorf("rename source is missing from the declaration order")
	}
	app.order[position] = after.Name
	app.goNames[after.GoName], app.dbTables[after.DBTable] = after.Name, after.Name
	for _, e := range edits {
		key, err := exactAutoPrimaryKey(e.to)
		if err != nil {
			return err
		}
		builder.apps[e.source.app].models[e.to.Name] = newLoadedStateModel(e.to, key)
	}
	for _, e := range edits {
		id := loadedModelIdentity{app: e.source.app, model: e.to.Name}
		for _, f := range e.to.Fields {
			if err := builder.addRelation(id, f); err != nil {
				return err
			}
		}
	}
	return nil
}
