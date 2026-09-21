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
	write, err := m.prepareCreate(ctx, backend, input)
	if err != nil {
		return zero, err
	}
	lastInsertID, err := backend.Insert(ctx, query.NewInsertPlanReturningKey(
		write.model.metadata.DBTable,
		write.mutation.assignments,
		fieldReference(write.model.primaryKey),
	))
	if err != nil {
		return zero, err
	}
	value := write.mutation.value
	write.descriptor.SetPrimaryKey(&value, lastInsertID)
	return value, nil
}

// Update applies only fields explicitly present in the generated patch. The
// model's hidden primary-key presence flag, not ID's numeric zero value,
// determines whether an instance is eligible for an update.
func (m Manager[M]) Update(ctx context.Context, backend db.Mutator, current M, input PatchInput[M]) (M, error) {
	var zero M
	write, err := m.prepareUpdate(ctx, backend, current, input)
	if err != nil {
		return zero, err
	}
	plan := query.NewUpdatePlan(
		write.model.metadata.DBTable,
		write.mutation.assignments,
		fieldReference(write.model.primaryKey),
		write.key,
	)
	rowsAffected, err := backend.Update(ctx, plan)
	if err != nil {
		return zero, err
	}
	if rowsAffected != 1 {
		return zero, unexpectedRows("update", rowsAffected)
	}
	return write.mutation.value, nil
}

// Delete removes one explicit-key instance. Once the backend reports success,
// generated descriptor code clears both the key value and its hidden presence
// flag on the caller's instance.
func (m Manager[M]) Delete(ctx context.Context, backend db.Mutator, value *M) (int64, error) {
	descriptor, prepared, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return 0, err
	}
	metadata, primaryKey := prepared.metadata, prepared.primaryKey
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

func (m Manager[M]) writeConfiguration(ctx context.Context, backend any) (WriteDescriptor[M], *preparedModel, error) {
	var zeroDescriptor WriteDescriptor[M]
	if interfaceIsNil(ctx) {
		return zeroDescriptor, nil, invalidWritePlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return zeroDescriptor, nil, err
	}
	if interfaceIsNil(backend) {
		return zeroDescriptor, nil, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeInvalidPlan,
			Detail:   "backend is nil",
		}
	}
	if m.prepared == nil {
		return zeroDescriptor, nil, invalidWritePlan("descriptor is nil")
	}
	descriptor, ok := m.descriptor.(WriteDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return zeroDescriptor, nil, invalidWritePlan("descriptor does not implement write key state")
	}
	if !m.prepared.writeValid {
		return zeroDescriptor, nil, invalidWritePlan("metadata must contain exactly one AutoField primary key")
	}
	return descriptor, m.prepared, nil
}

// Write execution and advisory validation share the same owned candidate and
// mutation checks. Public entry points still require their narrow DB port.
type preparedWrite[M any] struct {
	descriptor WriteDescriptor[M]
	model      *preparedModel
	mutation   Mutation[M]
	key        query.Value
}

func (m Manager[M]) prepareCreate(ctx context.Context, backend any, input CreateInput[M]) (preparedWrite[M], error) {
	descriptor, prepared, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return preparedWrite[M]{}, err
	}
	if interfaceIsNil(input) {
		return preparedWrite[M]{}, invalidWritePlan("create input is nil")
	}
	mutation := input.BuildCreate()
	if err := validateMutation(mutation, MutationCreate, prepared, descriptor, nil); err != nil {
		return preparedWrite[M]{}, err
	}
	return preparedWrite[M]{descriptor: descriptor, model: prepared, mutation: mutation}, nil
}

func (m Manager[M]) prepareUpdate(ctx context.Context, backend any, current M, input PatchInput[M]) (preparedWrite[M], error) {
	descriptor, prepared, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return preparedWrite[M]{}, err
	}
	primaryKey := prepared.primaryKey
	keyValue, present := descriptor.PrimaryKey(current)
	if !present {
		return preparedWrite[M]{}, &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeMissingPrimaryKey,
			Field:    primaryKey.Name,
			Detail:   "model instance has no explicit primary key state",
		}
	}
	if !mutationValueMatches(primaryKey, keyValue) || keyValue.IsNull() {
		return preparedWrite[M]{}, invalidWritePlan("descriptor returned an invalid primary key value")
	}
	if interfaceIsNil(input) {
		return preparedWrite[M]{}, invalidWritePlan("patch input is nil")
	}
	// PatchInput is an extension point. Neither the build callback nor omitted
	// field validation may borrow the caller's nullable pointers.
	baseline := descriptor.CloneWriteModel(current)
	buildCurrent := descriptor.CloneWriteModel(current)
	mutation := input.BuildPatch(buildCurrent)
	if err := validateMutation(mutation, MutationPatch, prepared, descriptor, &baseline); err != nil {
		return preparedWrite[M]{}, err
	}
	mutationKey, mutationKeyPresent := descriptor.PrimaryKey(mutation.value)
	if !mutationKeyPresent || !mutationKey.Equal(keyValue) {
		return preparedWrite[M]{}, invalidWritePlan("patch result primary key does not match the current model")
	}
	return preparedWrite[M]{descriptor: descriptor, model: prepared, mutation: mutation, key: keyValue}, nil
}

func validateMutation[M any](mutation Mutation[M], expected MutationKind, prepared *preparedModel, descriptor WriteDescriptor[M], current *M) error {
	metadata := prepared.metadata
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
		field, ok := prepared.mutationField(assignment.Field())
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
		modelValue, ok := descriptor.WriteFieldValue(mutation.value, field.Clone())
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
			before, beforeOK := descriptor.WriteFieldValue(*current, field.Clone())
			after, afterOK := descriptor.WriteFieldValue(mutation.value, field.Clone())
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

// Internal lookups borrow canonical fields. Clone immediately before every
// user-owned WriteFieldValue callback, including repeated omitted-field reads.
func (prepared *preparedModel) mutationField(reference query.FieldRef) (ir.Field, bool) {
	index, found := prepared.byReference[reference]
	if !found {
		return ir.Field{}, false
	}
	return prepared.metadata.Fields[index], true
}

func mutationValueMatches(field ir.Field, value query.Value) bool {
	if value.IsNull() {
		return field.Nullable
	}
	switch field.Kind {
	case ir.FieldAuto, ir.FieldInteger, ir.FieldForeignKey:
		return value.Kind() == query.ValueInteger
	case ir.FieldJSON:
		_, ok := value.JSON()
		return ok
	case ir.FieldUUID:
		_, ok := value.UUID()
		return ok
	case ir.FieldDecimal:
		number, ok := value.Decimal()
		return ok && field.Decimal != nil && number.Fits(field.Decimal.MaxDigits, field.Decimal.DecimalPlaces)
	case ir.FieldFloat:
		return value.Kind() == query.ValueFloat
	case ir.FieldDuration:
		return value.Kind() == query.ValueDuration
	case ir.FieldTime:
		return value.Kind() == query.ValueTime
	case ir.FieldDate:
		return value.Kind() == query.ValueDate
	case ir.FieldDateTime:
		return value.Kind() == query.ValueDateTime
	case ir.FieldChar, ir.FieldText:
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
