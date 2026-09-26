package migrations

import (
	"context"
	"fmt"
	"github.com/progresshans/godj/internal/irresource"
	"slices"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// AddManyToMany adds a columnless relation. BeforeField is an optional anchor
// in the ManyToMany declaration list, independent of stored column order.
type AddManyToMany struct {
	AppLabel, ModelName string
	Field               ir.ManyToManyField
	BeforeField         string
}

// RemoveManyToMany retains the exact declaration and its insertion anchor so
// reversal restores the same logical state without adopting newer metadata.
type RemoveManyToMany struct {
	AppLabel, ModelName string
	Field               ir.ManyToManyField
	BeforeField         string
}

// RenameManyToMany changes only the logical and Go names. Target, through,
// endpoint selection, reverse namespace and symmetry must remain identical.
type RenameManyToMany struct {
	AppLabel, ModelName string
	Before, After       ir.ManyToManyField
}

func (AddManyToMany) operation()        {}
func (AddManyToMany) Kind() string      { return "AddManyToMany" }
func (op AddManyToMany) App() string    { return op.AppLabel }
func (RemoveManyToMany) operation()     {}
func (RemoveManyToMany) Kind() string   { return "RemoveManyToMany" }
func (op RemoveManyToMany) App() string { return op.AppLabel }
func (RenameManyToMany) operation()     {}
func (RenameManyToMany) Kind() string   { return "RenameManyToMany" }
func (op RenameManyToMany) App() string { return op.AppLabel }

func (op AddManyToMany) stateForward(s ProjectState) (ProjectState, error) {
	return changeManyState(s, op, false)
}
func (op AddManyToMany) stateBackward(s ProjectState) (ProjectState, error) {
	return changeManyState(s, op, true)
}
func (op RemoveManyToMany) stateForward(s ProjectState) (ProjectState, error) {
	return changeManyState(s, op, false)
}
func (op RemoveManyToMany) stateBackward(s ProjectState) (ProjectState, error) {
	return changeManyState(s, op, true)
}
func (op RenameManyToMany) stateForward(s ProjectState) (ProjectState, error) {
	return changeManyState(s, op, false)
}
func (op RenameManyToMany) stateBackward(s ProjectState) (ProjectState, error) {
	return changeManyState(s, op, true)
}

func changeManyState(state ProjectState, operation Operation, reverse bool) (ProjectState, error) {
	app, name := operationSourceModel(operation)
	schema, exists := state.Schema(app)
	if exists {
		for index, model := range schema.Models {
			if model.Name != name {
				continue
			}
			changed, err := changedManyModel(app, model, operation, reverse)
			if err != nil {
				return state, err
			}
			schema.Models[index] = changed
			return state.withSchema(schema), nil
		}
	}
	return state, fmt.Errorf("ManyToMany model %s.%s does not exist", app, name)
}

func changedManyModel(app string, model ir.Model, operation Operation, reverse bool) (ir.Model, error) {
	model = model.Clone()
	var field ir.ManyToManyField
	var anchor string
	var add bool
	switch op := operation.(type) {
	case AddManyToMany:
		field, anchor, add = op.Field, op.BeforeField, !reverse
	case RemoveManyToMany:
		field, anchor, add = op.Field, op.BeforeField, reverse
	case RenameManyToMany:
		before, after := op.Before, op.After
		if reverse {
			before, after = after, before
		}
		if !ir.ManyToManyRename(before, after) {
			return ir.Model{}, fmt.Errorf("RenameManyToMany must change only names")
		}
		for index, existing := range model.ManyToMany {
			if existing.Name != before.Name {
				continue
			}
			if !existing.Equal(before) {
				return ir.Model{}, fmt.Errorf("ManyToMany %q differs from its historical definition", before.Name)
			}
			model.ManyToMany[index] = after.Clone()
			return normalizedSingleModel(app, model)
		}
		return ir.Model{}, fmt.Errorf("ManyToMany %q does not exist", before.Name)
	default:
		return ir.Model{}, fmt.Errorf("unsupported ManyToMany operation %T", operation)
	}
	position := slices.IndexFunc(model.ManyToMany, func(value ir.ManyToManyField) bool { return value.Name == field.Name })
	if add {
		if position >= 0 {
			return ir.Model{}, fmt.Errorf("ManyToMany %q already exists", field.Name)
		}
		position = len(model.ManyToMany)
		if anchor != "" {
			position = slices.IndexFunc(model.ManyToMany, func(value ir.ManyToManyField) bool { return value.Name == anchor })
			if position < 0 {
				return ir.Model{}, fmt.Errorf("ManyToMany insertion anchor %q does not exist", anchor)
			}
		}
		model.ManyToMany = slices.Insert(model.ManyToMany, position, field.Clone())
	} else {
		if position < 0 || !model.ManyToMany[position].Equal(field) {
			return ir.Model{}, fmt.Errorf("ManyToMany %q is missing or differs from its historical definition", field.Name)
		}
		if !(anchor == "" && position == len(model.ManyToMany)-1 || anchor != "" && position+1 < len(model.ManyToMany) && model.ManyToMany[position+1].Name == anchor) {
			return ir.Model{}, fmt.Errorf("ManyToMany %q differs from its historical position before %q", field.Name, anchor)
		}
		model.ManyToMany = slices.Delete(model.ManyToMany, position, position+1)
	}
	return normalizedSingleModel(app, model)
}

func (builder *loadedStateBuilder) changeMany(operation Operation, reverse bool) error {
	app, name := operationSourceModel(operation)
	model, exists := builder.model(loadedModelIdentity{app: app, model: name})
	if !exists {
		return fmt.Errorf("ManyToMany model %s.%s does not exist", app, name)
	}
	changed, err := changedManyModel(app, model.value, operation, reverse)
	if err != nil {
		return err
	}
	builder.manyCount -= uint64(len(model.value.ManyToMany))
	builder.manyCount += uint64(len(changed.ManyToMany))
	model.value = changed
	return nil
}

func changeManyDatabase(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState, app, name string) error {
	before, beforeExists := from.Model(app, name)
	after, afterExists := to.Model(app, name)
	if !beforeExists || !afterExists {
		return fmt.Errorf("ManyToMany source/target model %s.%s is missing", app, name)
	}
	capable, ok := editor.(backend.ManyToManySchemaEditor)
	if !ok {
		return backend.NewCapabilityError("many_to_many_migration", "schema editor has no columnless relation capability", nil)
	}
	return capable.AlterManyToMany(ctx, before, after)
}

func (op AddManyToMany) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}
func (op AddManyToMany) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}
func (op RemoveManyToMany) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}
func (op RemoveManyToMany) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}
func (op RenameManyToMany) databaseForward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}
func (op RenameManyToMany) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from, to ProjectState) error {
	return changeManyDatabase(ctx, editor, from, to, op.AppLabel, op.ModelName)
}

func isManyOperation(operation Operation) bool {
	switch operation.(type) {
	case AddManyToMany, RemoveManyToMany, RenameManyToMany:
		return true
	}
	return false
}

func (builder *loadedStateBuilder) validateMany() error {
	schemas := make([]ir.Schema, 0, len(builder.apps))
	budget := irresource.New(irresource.Limits{Fields: loadedDerivedIntentMaxFields, StringBytes: loadedDerivedIntentMaxStringBytes, Nodes: loadedDerivedIntentMaxNodes, Bytes: loadedDerivedIntentMaxAggregateBytes})
	physical := make(map[ir.ModelIdentity]ir.Model)
	for appLabel, app := range builder.apps {
		count := len(app.models)
		for _, model := range app.models {
			for _, field := range model.value.ManyToMany {
				if field.Through == nil {
					count++
				}
			}
		}
		if count > loadedDerivedIntentMaxTargets {
			return fmt.Errorf("historical app storage exceeds %d models", loadedDerivedIntentMaxTargets)
		}
		schema := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: appLabel}
		for _, name := range app.order {
			model := app.models[name].value
			if err := budget.ScanModel("model", model); err != nil {
				return err
			}
			id := ir.ModelIdentity{AppLabel: appLabel, ModelName: name}
			if _, exists := physical[id]; exists {
				return fmt.Errorf("automatic storage collides with a declared model")
			}
			physical[id] = model
			schema.Models = append(schema.Models, model)
			for _, field := range model.ManyToMany {
				if field.Through != nil {
					continue
				}
				if len(model.Name)+len(field.Name)+1 > loadedDerivedIntentMaxStringBytes || len(model.DBTable)+len(field.Name)+1 > loadedDerivedIntentMaxStringBytes || len(model.GoName)+len(field.GoName)+4 > loadedDerivedIntentMaxStringBytes {
					return fmt.Errorf("automatic storage identifier exceeds its resource limit")
				}
				derived, err := ir.AutomaticThroughModel(appLabel, model, field)
				if err != nil {
					return err
				}
				if err := budget.ScanModel("automatic", derived); err != nil {
					return err
				}
				key := field.StorageThrough(id).Model
				if _, exists := physical[key]; exists {
					return fmt.Errorf("automatic storage collides with another model")
				}
				physical[key] = derived
			}
		}
		schemas = append(schemas, schema)
	}
	slices.SortFunc(schemas, func(a, b ir.Schema) int {
		if a.AppLabel < b.AppLabel {
			return -1
		}
		if a.AppLabel > b.AppLabel {
			return 1
		}
		return 0
	})
	if _, err := ir.ResolveManyToMany(schemas...); err != nil {
		return err
	}
	for _, model := range physical {
		for _, field := range model.Fields {
			if field.Relation == nil {
				continue
			}
			target, exists := physical[field.Relation.Target]
			if !exists {
				return fmt.Errorf("historical graph has a missing FK target %s.%s", field.Relation.Target.AppLabel, field.Relation.Target.ModelName)
			}
			if field.Relation.Reverse.Disabled {
				continue
			}
			for _, many := range target.ManyToMany {
				if many.Name == field.Relation.Reverse.Name {
					return fmt.Errorf("relation reverse name %q collides with a ManyToMany field", many.Name)
				}
			}
		}
	}
	return nil
}

func loadedScanManyOperation(budget *loadedResourceBudget, migration Migration, index int, kind, model, anchor string, fields ...ir.ManyToManyField) {
	prefix := fmt.Sprintf("operations[%d]", index)
	loadedConsumeString(budget, migration, index, kind, prefix+".model_name", model, false)
	loadedConsumeString(budget, migration, index, kind, prefix+".before_field", anchor, false)
	for i, field := range fields {
		path := fmt.Sprintf("%s.many_to_many[%d]", prefix, i)
		loadedConsumeNodes(budget, 3)
		budget.scan.fields++
		for _, item := range []struct{ name, value string }{{"name", field.Name}, {"go_name", field.GoName}, {"symmetry", string(field.Symmetry)}, {"target.app_label", field.Target.AppLabel}, {"target.model_name", field.Target.ModelName}, {"reverse.name", field.Reverse.Name}} {
			loadedConsumeString(budget, migration, index, kind, path+"."+item.name, item.value, false)
		}
		if through := field.Through; through != nil {
			loadedConsumeNodes(budget, 2)
			for _, item := range []struct{ name, value string }{{"model.app_label", through.Model.AppLabel}, {"model.model_name", through.Model.ModelName}, {"source_field", through.SourceField}, {"target_field", through.TargetField}} {
				loadedConsumeString(budget, migration, index, kind, path+".through."+item.name, item.value, false)
			}
		}
		if budget.nodeOverflow {
			return
		}
	}
}
