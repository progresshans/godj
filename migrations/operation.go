package migrations

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// Operation is intentionally limited to built-in typed operations in this
// first migration slice. New operation kinds extend this package after their
// state and database semantics are specified.
type Operation interface {
	operation()
	Kind() string
	App() string
	stateForward(ProjectState) (ProjectState, error)
	stateBackward(ProjectState) (ProjectState, error)
	databaseForward(context.Context, backend.SchemaEditor, ProjectState, ProjectState) error
	databaseBackward(context.Context, backend.SchemaEditor, ProjectState, ProjectState) error
}

type CreateModel struct {
	AppLabel string
	Model    ir.Model
}

func (CreateModel) operation()   {}
func (CreateModel) Kind() string { return "CreateModel" }
func (op CreateModel) App() string {
	return op.AppLabel
}

func (op CreateModel) stateForward(state ProjectState) (ProjectState, error) {
	model, err := normalizedSingleModel(op.AppLabel, op.Model)
	if err != nil {
		return state, fmt.Errorf("normalize model: %w", err)
	}
	if _, exists := state.Model(op.AppLabel, model.Name); exists {
		return state, fmt.Errorf("model %s.%s already exists", op.AppLabel, model.Name)
	}
	schema, exists := state.Schema(op.AppLabel)
	if !exists {
		schema = ir.Schema{FormatVersion: ir.FormatVersion, AppLabel: op.AppLabel}
	}
	schema.Models = append(schema.Models, model)
	normalized, err := ir.Normalize(schema)
	if err != nil {
		return state, fmt.Errorf("normalize project app %s: %w", op.AppLabel, err)
	}
	return state.withSchema(normalized), nil
}

func (op CreateModel) stateBackward(state ProjectState) (ProjectState, error) {
	want, err := normalizedSingleModel(op.AppLabel, op.Model)
	if err != nil {
		return state, fmt.Errorf("normalize model: %w", err)
	}
	actual, exists := state.Model(op.AppLabel, want.Name)
	if !exists {
		return state, fmt.Errorf("model %s.%s does not exist", op.AppLabel, want.Name)
	}
	if !modelEqual(actual, want) {
		return state, fmt.Errorf("model %s.%s does not match CreateModel state", op.AppLabel, want.Name)
	}
	schema, _ := state.Schema(op.AppLabel)
	models := make([]ir.Model, 0, len(schema.Models)-1)
	for _, model := range schema.Models {
		if model.Name != want.Name {
			models = append(models, model.Clone())
		}
	}
	if len(models) == 0 {
		return state.withoutApp(op.AppLabel), nil
	}
	schema.Models = models
	normalized, err := ir.Normalize(schema)
	if err != nil {
		return state, fmt.Errorf("normalize project app %s: %w", op.AppLabel, err)
	}
	return state.withSchema(normalized), nil
}

func (op CreateModel) databaseForward(ctx context.Context, editor backend.SchemaEditor, _ ProjectState, to ProjectState) error {
	model, exists := to.Model(op.AppLabel, op.Model.Name)
	if !exists {
		return fmt.Errorf("preflight target model %s.%s is missing", op.AppLabel, op.Model.Name)
	}
	return editor.CreateModel(ctx, model)
}

func (op CreateModel) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from ProjectState, _ ProjectState) error {
	model, exists := from.Model(op.AppLabel, op.Model.Name)
	if !exists {
		return fmt.Errorf("preflight source model %s.%s is missing", op.AppLabel, op.Model.Name)
	}
	return editor.DeleteModel(ctx, model)
}

type AddField struct {
	AppLabel  string
	ModelName string
	Field     ir.Field
}

func (AddField) operation()   {}
func (AddField) Kind() string { return "AddField" }
func (op AddField) App() string {
	return op.AppLabel
}

func (op AddField) stateForward(state ProjectState) (ProjectState, error) {
	schema, exists := state.Schema(op.AppLabel)
	if !exists {
		return state, fmt.Errorf("app %s does not exist", op.AppLabel)
	}
	modelIndex := -1
	for index, model := range schema.Models {
		if model.Name == op.ModelName {
			modelIndex = index
			for _, field := range model.Fields {
				if field.Name == op.Field.Name || (op.Field.Column != "" && field.Column == op.Field.Column) {
					return state, fmt.Errorf("field %s.%s.%s already exists", op.AppLabel, op.ModelName, op.Field.Name)
				}
			}
			break
		}
	}
	if modelIndex < 0 {
		return state, fmt.Errorf("model %s.%s does not exist", op.AppLabel, op.ModelName)
	}
	schema.Models[modelIndex].Fields = append(schema.Models[modelIndex].Fields, op.Field)
	normalized, err := ir.Normalize(schema)
	if err != nil {
		return state, fmt.Errorf("normalize added field: %w", err)
	}
	return state.withSchema(normalized), nil
}

func (op AddField) stateBackward(state ProjectState) (ProjectState, error) {
	schema, exists := state.Schema(op.AppLabel)
	if !exists {
		return state, fmt.Errorf("app %s does not exist", op.AppLabel)
	}
	modelIndex := -1
	fieldIndex := -1
	for index, model := range schema.Models {
		if model.Name != op.ModelName {
			continue
		}
		modelIndex = index
		for current, field := range model.Fields {
			if field.Name == op.Field.Name {
				fieldIndex = current
				normalizedField := op.Field
				if normalizedField.Column == "" {
					normalizedField.Column = normalizedField.Name
				}
				if !fieldEqual(field, normalizedField) {
					return state, fmt.Errorf("field %s.%s.%s does not match AddField state", op.AppLabel, op.ModelName, op.Field.Name)
				}
				break
			}
		}
		break
	}
	if modelIndex < 0 {
		return state, fmt.Errorf("model %s.%s does not exist", op.AppLabel, op.ModelName)
	}
	if fieldIndex < 0 {
		return state, fmt.Errorf("field %s.%s.%s does not exist", op.AppLabel, op.ModelName, op.Field.Name)
	}
	fields := schema.Models[modelIndex].Fields
	schema.Models[modelIndex].Fields = append(fields[:fieldIndex:fieldIndex], fields[fieldIndex+1:]...)
	normalized, err := ir.Normalize(schema)
	if err != nil {
		return state, fmt.Errorf("normalize removed field: %w", err)
	}
	return state.withSchema(normalized), nil
}

func (op AddField) databaseForward(ctx context.Context, editor backend.SchemaEditor, from ProjectState, to ProjectState) error {
	model, exists := from.Model(op.AppLabel, op.ModelName)
	if !exists {
		return fmt.Errorf("preflight source model %s.%s is missing", op.AppLabel, op.ModelName)
	}
	field, exists := findField(to, op.AppLabel, op.ModelName, op.Field.Name)
	if !exists {
		return fmt.Errorf("preflight target field %s.%s.%s is missing", op.AppLabel, op.ModelName, op.Field.Name)
	}
	return editor.AddField(ctx, model, field)
}

func (op AddField) databaseBackward(ctx context.Context, editor backend.SchemaEditor, from ProjectState, _ ProjectState) error {
	model, exists := from.Model(op.AppLabel, op.ModelName)
	if !exists {
		return fmt.Errorf("preflight source model %s.%s is missing", op.AppLabel, op.ModelName)
	}
	field, exists := findField(from, op.AppLabel, op.ModelName, op.Field.Name)
	if !exists {
		return fmt.Errorf("preflight source field %s.%s.%s is missing", op.AppLabel, op.ModelName, op.Field.Name)
	}
	return editor.RemoveField(ctx, model, field)
}

func findField(state ProjectState, app, modelName, fieldName string) (ir.Field, bool) {
	model, exists := state.Model(app, modelName)
	if !exists {
		return ir.Field{}, false
	}
	for _, field := range model.Fields {
		if field.Name == fieldName {
			return field, true
		}
	}
	return ir.Field{}, false
}
