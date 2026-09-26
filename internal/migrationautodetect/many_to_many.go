package migrationautodetect

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func manyChanges(app string, before, after ir.Model) ([]migrations.Operation, error) {
	fail := func() ([]migrations.Operation, error) {
		return nil, detectionError(CodeUnsupportedChange, app, after.Name, "", fmt.Errorf("ManyToMany changes must preserve retained declaration order and bindings; ambiguous renames require an authored migration"))
	}
	old, newFields := map[string]ir.ManyToManyField{}, map[string]ir.ManyToManyField{}
	for _, field := range before.ManyToMany {
		old[field.Name] = field
	}
	for _, field := range after.ManyToMany {
		newFields[field.Name] = field
	}
	renames := map[string]ir.ManyToManyField{}
	for _, field := range before.ManyToMany {
		if current, exists := newFields[field.Name]; exists {
			if field.Equal(current) {
				continue
			}
			if !ir.ManyToManyRename(field, current) {
				return fail()
			}
			renames[field.Name] = current
			continue
		}
		var candidates []ir.ManyToManyField
		for _, candidate := range after.ManyToMany {
			if _, retained := old[candidate.Name]; !retained && ir.ManyToManyRename(field, candidate) {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) > 1 {
			return fail()
		}
		if len(candidates) == 1 {
			for _, previous := range renames {
				if previous.Name == candidates[0].Name {
					return fail()
				}
			}
			renames[field.Name] = candidates[0]
		}
	}
	working := slices.Clone(before.ManyToMany)
	var operations []migrations.Operation
	for index := len(working) - 1; index >= 0; index-- {
		field := working[index]
		if _, kept := newFields[field.Name]; kept {
			continue
		}
		if _, renamed := renames[field.Name]; renamed {
			continue
		}
		anchor := ""
		if index+1 < len(working) {
			anchor = working[index+1].Name
		}
		operations = append(operations, migrations.RemoveManyToMany{AppLabel: app, ModelName: after.Name, Field: field.Clone(), BeforeField: anchor})
		working = slices.Delete(working, index, index+1)
	}
	for index, field := range working {
		if renamed, ok := renames[field.Name]; ok {
			operations = append(operations, migrations.RenameManyToMany{AppLabel: app, ModelName: after.Name, Before: field.Clone(), After: renamed.Clone()})
			working[index] = renamed.Clone()
		}
	}
	for index := len(after.ManyToMany) - 1; index >= 0; index-- {
		field := after.ManyToMany[index]
		if slices.ContainsFunc(working, func(value ir.ManyToManyField) bool { return value.Name == field.Name }) {
			continue
		}
		anchor := ""
		position := len(working)
		if index+1 < len(after.ManyToMany) {
			anchor = after.ManyToMany[index+1].Name
			position = slices.IndexFunc(working, func(value ir.ManyToManyField) bool { return value.Name == anchor })
		}
		if position < 0 {
			return fail()
		}
		operations = append(operations, migrations.AddManyToMany{AppLabel: app, ModelName: after.Name, Field: field.Clone(), BeforeField: anchor})
		working = slices.Insert(working, position, field.Clone())
	}
	if !slices.EqualFunc(working, after.ManyToMany, ir.ManyToManyField.Equal) {
		return fail()
	}
	return operations, nil
}

func collectManyOperationReferences(references *[]relationReference, operation migrations.Operation) {
	var app, model string
	var field ir.ManyToManyField
	switch op := operation.(type) {
	case migrations.AddManyToMany:
		app, model, field = op.AppLabel, op.ModelName, op.Field
	case migrations.RemoveManyToMany:
		app, model, field = op.AppLabel, op.ModelName, op.Field
	case migrations.RenameManyToMany:
		app, model, field = op.AppLabel, op.ModelName, op.Before
	default:
		return
	}
	if field.Target.AppLabel != app {
		*references = append(*references, relationReference{sourceModel: model, field: field.Name, targetApp: field.Target.AppLabel, targetModel: field.Target.ModelName})
	}
	if through := field.Through; through != nil && through.Model.AppLabel != app {
		*references = append(*references, relationReference{sourceModel: model, field: field.Name, targetApp: through.Model.AppLabel, targetModel: through.Model.ModelName, targetFields: []string{through.SourceField, through.TargetField}})
	}
}

func manyReferenceReady(reference relationReference, current migrations.ProjectState) bool {
	if len(reference.targetFields) == 0 {
		_, exists := projectStorageModel(current, reference.targetApp, reference.targetModel)
		return exists
	}
	model, exists := projectStorageModel(current, reference.targetApp, reference.targetModel)
	if !exists {
		return false
	}
	available := fieldNameSet(model.Fields)
	for _, name := range reference.targetFields {
		if !available[name] {
			return false
		}
	}
	return true
}

func manyCandidateReady(operation migrations.AddManyToMany, app string, current migrations.ProjectState, available map[string]map[string]bool) bool {
	field := operation.Field
	if field.Target.AppLabel == app {
		if _, exists := available[field.Target.ModelName]; !exists {
			return false
		}
	} else if _, exists := projectStorageModel(current, field.Target.AppLabel, field.Target.ModelName); !exists {
		return false
	}
	through := field.Through
	if through == nil {
		return true
	}
	var fields map[string]bool
	if through.Model.AppLabel == app {
		fields = available[through.Model.ModelName]
	} else {
		model, exists := projectStorageModel(current, through.Model.AppLabel, through.Model.ModelName)
		if !exists {
			return false
		}
		fields = fieldNameSet(model.Fields)
	}
	return fields[through.SourceField] && fields[through.TargetField]
}

// Storage is a derived read view; detection emits logical operations only.
func projectStorageSchema(state migrations.ProjectState, app string) (ir.Schema, error) {
	schema, exists := state.Schema(app)
	if !exists {
		return ir.Schema{}, nil
	}
	return ir.StorageSchema(schema)
}

func projectStorageModel(state migrations.ProjectState, app, name string) (ir.Model, bool) {
	if model, exists := state.Model(app, name); exists {
		return model, true
	}
	storage, err := projectStorageSchema(state, app)
	if err != nil {
		return ir.Model{}, false
	}
	for _, model := range storage.Models {
		if model.Name == name {
			return model, true
		}
	}
	return ir.Model{}, false
}
