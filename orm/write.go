package orm

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Create validates and builds a generated input before performing one backend
// call. Required/default/null decisions therefore fail without database I/O.
func (m Manager[M]) Create(ctx context.Context, backend db.Mutator, input CreateInput[M]) (M, error) {
	var zero M
	descriptor, metadata, primaryKey, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return zero, err
	}
	if interfaceIsNil(input) {
		return zero, invalidWritePlan("create input is nil")
	}
	mutation := input.BuildCreate()
	if err := validateMutation(mutation, MutationCreate, metadata, descriptor, nil); err != nil {
		return zero, err
	}
	lastInsertID, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(
		metadata.DBTable,
		mutation.assignments,
		fieldReference(primaryKey),
	))
	if err != nil {
		return zero, err
	}
	value := mutation.value
	descriptor.SetPrimaryKey(&value, lastInsertID)
	return value, nil
}

// Update applies only fields explicitly present in the generated patch. The
// model's hidden primary-key presence flag, not ID's numeric zero value,
// determines whether an instance is eligible for an update.
func (m Manager[M]) Update(ctx context.Context, backend db.Mutator, current M, input PatchInput[M]) (M, error) {
	var zero M
	descriptor, metadata, primaryKey, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return zero, err
	}
	keyValue, present := descriptor.PrimaryKey(current)
	if !present {
		return zero, &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeMissingPrimaryKey,
			Field:    primaryKey.Name,
			Detail:   "model instance has no explicit primary key state",
		}
	}
	if !mutationValueMatches(primaryKey, keyValue) || keyValue.IsNull() {
		return zero, invalidWritePlan("descriptor returned an invalid primary key value")
	}
	if interfaceIsNil(input) {
		return zero, invalidWritePlan("patch input is nil")
	}
	// PatchInput is an exported extension point. Give it a deep-cloned model so
	// nullable pointer fields cannot alias and mutate the caller, and retain an
	// independent baseline for omitted-field validation.
	baseline := descriptor.CloneWriteModel(current)
	buildCurrent := descriptor.CloneWriteModel(current)
	mutation := input.BuildPatch(buildCurrent)
	if err := validateMutation(mutation, MutationPatch, metadata, descriptor, &baseline); err != nil {
		return zero, err
	}
	mutationKey, mutationKeyPresent := descriptor.PrimaryKey(mutation.value)
	if !mutationKeyPresent || !mutationKey.Equal(keyValue) {
		return zero, invalidWritePlan("patch result primary key does not match the current model")
	}
	if len(mutation.assignments) == 0 {
		return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeEmptyPatch, Detail: "patch has no explicit field changes"}
	}
	plan := query.NewUpdatePlan(
		metadata.DBTable,
		mutation.assignments,
		fieldReference(primaryKey),
		keyValue,
	)
	rowsAffected, err := backend.Update(ctx, plan)
	if err != nil {
		return zero, err
	}
	if rowsAffected != 1 {
		return zero, unexpectedRows("update", rowsAffected)
	}
	return mutation.value, nil
}

// Delete removes one explicit-key instance. Once the backend reports success,
// generated descriptor code clears both the key value and its hidden presence
// flag on the caller's instance.
func (m Manager[M]) Delete(ctx context.Context, backend db.Mutator, value *M) (int64, error) {
	descriptor, metadata, primaryKey, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return 0, err
	}
	if value == nil {
		return 0, invalidWritePlan("delete model pointer is nil")
	}
	keyValue, present := descriptor.PrimaryKey(*value)
	if !present {
		return 0, &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeMissingPrimaryKey,
			Field:    primaryKey.Name,
			Detail:   "model instance has no explicit primary key state",
		}
	}
	if !mutationValueMatches(primaryKey, keyValue) || keyValue.IsNull() {
		return 0, invalidWritePlan("descriptor returned an invalid primary key value")
	}
	rowsAffected, err := backend.Delete(ctx, query.NewDeletePlan(metadata.DBTable, fieldReference(primaryKey), keyValue))
	if err != nil {
		return 0, err
	}
	if rowsAffected != 1 {
		return rowsAffected, unexpectedRows("delete", rowsAffected)
	}
	descriptor.ClearPrimaryKey(value)
	return rowsAffected, nil
}

func (m Manager[M]) writeConfiguration(ctx context.Context, backend db.Mutator) (WriteDescriptor[M], ir.Model, ir.Field, error) {
	var zeroDescriptor WriteDescriptor[M]
	if ctx == nil {
		return zeroDescriptor, ir.Model{}, ir.Field{}, invalidWritePlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return zeroDescriptor, ir.Model{}, ir.Field{}, err
	}
	if interfaceIsNil(backend) {
		return zeroDescriptor, ir.Model{}, ir.Field{}, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeInvalidPlan,
			Detail:   "backend is nil",
		}
	}
	if descriptorIsNil(m.descriptor) {
		return zeroDescriptor, ir.Model{}, ir.Field{}, invalidWritePlan("descriptor is nil")
	}
	descriptor, ok := m.descriptor.(WriteDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return zeroDescriptor, ir.Model{}, ir.Field{}, invalidWritePlan("descriptor does not implement write key state")
	}
	metadata := descriptor.Metadata()
	primaryKey, ok := autoPrimaryKey(metadata)
	if !ok {
		return zeroDescriptor, ir.Model{}, ir.Field{}, invalidWritePlan("metadata must contain exactly one AutoField primary key")
	}
	return descriptor, metadata, primaryKey, nil
}

func validateMutation[M any](mutation Mutation[M], expected MutationKind, metadata ir.Model, descriptor WriteDescriptor[M], current *M) error {
	if mutation.err != nil {
		return mutation.err
	}
	if mutation.kind != expected {
		return invalidWritePlan("generated mutation kind does not match Manager operation")
	}
	if mutation.table != metadata.DBTable {
		return invalidWritePlan("generated mutation table does not match descriptor metadata")
	}
	if expected == MutationPatch && len(mutation.assignments) == 0 {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeEmptyPatch, Detail: "patch has no explicit field changes"}
	}
	seen := make(map[string]struct{}, len(mutation.assignments))
	for _, assignment := range mutation.assignments {
		field, ok := mutationField(metadata, assignment.Field())
		if !ok || field.PrimaryKey {
			return &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnknownField,
				Field:    assignment.Field().Name(),
				Detail:   "mutation field is not writable descriptor metadata",
			}
		}
		if _, duplicate := seen[field.Name]; duplicate {
			return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Field: field.Name, Detail: "mutation field is assigned more than once"}
		}
		seen[field.Name] = struct{}{}
		if !mutationValueMatches(field, assignment.Value()) {
			return &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field.Name, Detail: "mutation value does not match field type or nullability"}
		}
		modelValue, ok := descriptor.WriteFieldValue(mutation.value, field)
		if !ok || !modelValue.Equal(assignment.Value()) {
			return &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field.Name, Detail: "mutation result model does not match its assignment"}
		}
	}
	if expected == MutationCreate {
		for _, field := range metadata.Fields {
			if field.PrimaryKey {
				continue
			}
			if _, ok := seen[field.Name]; !ok {
				return &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField, Field: field.Name, Detail: "create mutation omitted normalized field assignment"}
			}
		}
	}
	if expected == MutationPatch {
		if current == nil {
			return invalidWritePlan("patch validation requires the current model")
		}
		for _, field := range metadata.Fields {
			if field.PrimaryKey {
				continue
			}
			if _, assigned := seen[field.Name]; assigned {
				continue
			}
			before, beforeOK := descriptor.WriteFieldValue(*current, field)
			after, afterOK := descriptor.WriteFieldValue(mutation.value, field)
			if !beforeOK || !afterOK || !before.Equal(after) {
				return &query.Error{
					Category: query.CategoryField,
					Code:     query.CodeInvalidValue,
					Field:    field.Name,
					Detail:   "patch result changed a field without an assignment",
				}
			}
		}
	}
	return nil
}

func mutationField(metadata ir.Model, reference query.FieldRef) (ir.Field, bool) {
	for _, field := range metadata.Fields {
		if fieldReference(field).Equal(reference) {
			return field, true
		}
	}
	return ir.Field{}, false
}

func mutationValueMatches(field ir.Field, value query.Value) bool {
	if value.IsNull() {
		return field.Nullable
	}
	switch field.Kind {
	case ir.FieldAuto, ir.FieldForeignKey:
		return value.Kind() == query.ValueInteger
	case ir.FieldChar:
		return value.Kind() == query.ValueString
	case ir.FieldBoolean:
		return value.Kind() == query.ValueBoolean
	default:
		return false
	}
}

func autoPrimaryKey(metadata ir.Model) (ir.Field, bool) {
	var result ir.Field
	count := 0
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			result = field
			count++
		}
	}
	return result, count == 1 && result.Kind == ir.FieldAuto
}

func invalidWritePlan(detail string) error {
	return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: detail}
}

func unexpectedRows(operation string, rows int64) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnexpectedRows,
		Detail:   operation + " affected an unexpected number of rows: " + fmt.Sprint(rows),
	}
}
