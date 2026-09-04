package orm

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// SaveOption is an immutable, model-specific configuration value for
// Manager.Save. Its state is private, so callers construct only supported
// options, while the zero value remains available for defensive validation.
// The marker keeps different model instantiations distinct at compile time.
type SaveOption[M any] struct {
	kind   saveOptionKind
	fields []WritableField[M]
	names  []string
	marker [0]func(M)
}

type saveOptionKind uint8

const (
	saveOptionTypedFields saveOptionKind = iota + 1
	saveOptionDynamicFields
	saveOptionForceInsert
	saveOptionForceUpdate
)

func (option SaveOption[M]) apply(state *saveOptionState[M]) {
	switch option.kind {
	case saveOptionTypedFields:
		state.setFieldMask(option.fields, nil)
	case saveOptionDynamicFields:
		state.setFieldMask(nil, option.names)
	case saveOptionForceInsert:
		if state.forceInsert {
			state.setError(invalidSaveArgument("force_insert was supplied more than once"))
		}
		state.forceInsert = true
	case saveOptionForceUpdate:
		if state.forceUpdate {
			state.setError(invalidSaveArgument("force_update was supplied more than once"))
		}
		state.forceUpdate = true
	default:
		state.setError(invalidWritePlan("unknown save option"))
	}
}

// UpdateFields limits Save to the supplied generated writable fields. Calling
// it with no fields is an explicit zero-I/O no-op, distinct from omitting the
// option (which saves every writable concrete field).
func UpdateFields[M any](fields ...WritableField[M]) SaveOption[M] {
	return SaveOption[M]{
		kind:   saveOptionTypedFields,
		fields: append([]WritableField[M](nil), fields...),
	}
}

// UpdateFieldNames is the dynamic compatibility path for update_fields. Names
// are resolved against descriptor metadata before any backend call and then
// converge on the same immutable execution plan as UpdateFields.
func UpdateFieldNames[M any](names ...string) SaveOption[M] {
	return SaveOption[M]{
		kind:  saveOptionDynamicFields,
		names: append([]string(nil), names...),
	}
}

func ForceInsert[M any]() SaveOption[M] {
	return SaveOption[M]{kind: saveOptionForceInsert}
}

func ForceUpdate[M any]() SaveOption[M] {
	return SaveOption[M]{kind: saveOptionForceUpdate}
}

type saveOptionState[M any] struct {
	fieldMaskSet bool
	typedFields  []WritableField[M]
	dynamicNames []string
	forceInsert  bool
	forceUpdate  bool
	err          error
}

func (state *saveOptionState[M]) setFieldMask(fields []WritableField[M], names []string) {
	if state.fieldMaskSet {
		state.setError(invalidSaveArgument("an update field mask was supplied more than once"))
		return
	}
	state.fieldMaskSet = true
	state.typedFields = append([]WritableField[M](nil), fields...)
	state.dynamicNames = append([]string(nil), names...)
}

func (state *saveOptionState[M]) setError(err error) {
	if state.err == nil {
		state.err = err
	}
}

type saveZeroRowsAction uint8

const (
	saveZeroRowsUnexpected saveZeroRowsAction = iota
	saveZeroRowsInsert
	saveZeroRowsNotUpdated
)

// saveExecutionPlan is an immutable ORM-internal orchestration value. Query
// plans clone their assignment storage, and the remaining fields are scalar
// policy values, so neither options nor the caller's model can alter a built
// plan while it is executing.
type saveExecutionPlan struct {
	noOp               bool
	hasUpdate          bool
	update             query.UpdatePlan
	hasInsert          bool
	insert             query.InsertPlan
	assignGeneratedKey bool
	zeroRows           saveZeroRowsAction
	missingRowCode     string
}

// Save persists one mutable model instance without opening a transaction.
// Callers that need rollback semantics pass a transaction-bound db.Session
// supplied by db.Atomic; successful generated-key assignment intentionally
// remains on value if that outer transaction later rolls back.
func (m Manager[M]) Save(ctx context.Context, backend db.Mutator, value *M, options ...SaveOption[M]) error {
	descriptor, metadata, primaryKey, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return err
	}
	if value == nil {
		return invalidWritePlan("save model pointer is nil")
	}
	// A deep snapshot keeps nullable pointees and any future reference-like
	// generated fields from mutating an already-built plan through the caller.
	snapshot := descriptor.CloneWriteModel(*value)
	plan, err := buildSaveExecutionPlan(descriptor, metadata, primaryKey, snapshot, options)
	if err != nil {
		return err
	}
	return executeSavePlan(ctx, backend, descriptor, value, plan)
}

func buildSaveExecutionPlan[M any](
	descriptor WriteDescriptor[M],
	metadata ir.Model,
	primaryKey ir.Field,
	value M,
	options []SaveOption[M],
) (saveExecutionPlan, error) {
	state := saveOptionState[M]{}
	for _, option := range options {
		if option.kind == 0 {
			return saveExecutionPlan{}, invalidWritePlan("save option is the zero value")
		}
		option.apply(&state)
	}
	if state.err != nil {
		return saveExecutionPlan{}, state.err
	}
	if state.forceInsert && state.forceUpdate {
		return saveExecutionPlan{}, &query.Error{
			Category: query.CategoryArgument,
			Code:     query.CodeMutuallyExclusiveForceFlags,
			Detail:   "force_insert and force_update cannot both be true",
		}
	}

	selectedFields, err := resolveSaveFields[M](metadata, state)
	if err != nil {
		return saveExecutionPlan{}, err
	}
	if state.fieldMaskSet && len(selectedFields) == 0 {
		return saveExecutionPlan{noOp: true}, nil
	}
	if state.forceInsert && state.fieldMaskSet {
		return saveExecutionPlan{}, &query.Error{
			Category: query.CategoryArgument,
			Code:     query.CodeMutuallyExclusiveForceFlags,
			Detail:   "force_insert cannot be combined with a non-empty update field mask",
		}
	}

	keyValue, keyPresent := descriptor.PrimaryKey(value)
	if !mutationValueMatches(primaryKey, keyValue) || keyValue.IsNull() {
		return saveExecutionPlan{}, invalidWritePlan("descriptor returned an invalid primary key value")
	}
	if !keyPresent {
		keyInteger, _ := keyValue.Integer()
		if keyInteger != 0 {
			return saveExecutionPlan{}, invalidWritePlan("model has a non-zero primary key value without explicit primary-key presence")
		}
	}

	writableFields := selectedFields
	if !state.fieldMaskSet {
		writableFields = nonPrimaryFields(metadata)
	}
	writableAssignments, err := saveAssignments(descriptor, value, writableFields)
	if err != nil {
		return saveExecutionPlan{}, err
	}

	if !keyPresent {
		if state.forceUpdate || state.fieldMaskSet {
			return saveExecutionPlan{}, &query.Error{
				Category: query.CategoryModelState,
				Code:     query.CodeForceUpdateWithoutPrimaryKey,
				Field:    primaryKey.Name,
				Detail:   "an update-only save requires explicit primary-key presence",
			}
		}
		return saveExecutionPlan{
			hasInsert:          true,
			insert:             query.NewInsertPlanReturningKey(metadata.DBTable, writableAssignments, fieldReference(primaryKey)),
			assignGeneratedKey: true,
		}, nil
	}

	keyAssignment := NewAssignment(primaryKey, keyValue)
	insertAssignments := make([]query.Assignment, 0, len(writableAssignments)+1)
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			insertAssignments = append(insertAssignments, keyAssignment)
			continue
		}
		assignment, ok := assignmentForField(writableAssignments, field)
		if ok {
			insertAssignments = append(insertAssignments, assignment)
		}
	}

	if state.forceInsert {
		return saveExecutionPlan{
			hasInsert: true,
			insert:    query.NewInsertPlanReturningKey(metadata.DBTable, insertAssignments, fieldReference(primaryKey)),
		}, nil
	}

	zeroRows := saveZeroRowsInsert
	hasInsert := true
	missingRowCode := ""
	if state.forceUpdate || state.fieldMaskSet {
		zeroRows = saveZeroRowsNotUpdated
		hasInsert = false
		missingRowCode = query.CodeUpdateFieldsMissingRow
		if state.forceUpdate {
			missingRowCode = query.CodeForceUpdateMissingRow
		}
	}
	return saveExecutionPlan{
		hasUpdate: true,
		update: query.NewUpdatePlan(
			metadata.DBTable,
			writableAssignments,
			fieldReference(primaryKey),
			keyValue,
		),
		hasInsert:      hasInsert,
		insert:         query.NewInsertPlanReturningKey(metadata.DBTable, insertAssignments, fieldReference(primaryKey)),
		zeroRows:       zeroRows,
		missingRowCode: missingRowCode,
	}, nil
}

func executeSavePlan[M any](
	ctx context.Context,
	backend db.Mutator,
	descriptor WriteDescriptor[M],
	value *M,
	plan saveExecutionPlan,
) error {
	if plan.noOp {
		return nil
	}
	if plan.hasUpdate {
		rowsAffected, err := backend.Update(ctx, plan.update)
		if err != nil {
			return err
		}
		switch {
		case rowsAffected == 1:
			return nil
		case rowsAffected == 0 && plan.zeroRows == saveZeroRowsInsert:
			// Continue into the already-built explicit-key insert plan.
		case rowsAffected == 0 && plan.zeroRows == saveZeroRowsNotUpdated:
			return &query.Error{
				Category: query.CategoryNotUpdated,
				Code:     plan.missingRowCode,
				Detail:   "save update did not affect an existing row",
			}
		default:
			return unexpectedRows("save update", rowsAffected)
		}
	}
	if !plan.hasInsert {
		return invalidWritePlan("save execution plan has no operation")
	}
	lastInsertID, err := backend.Insert(ctx, plan.insert)
	if err != nil {
		return err
	}
	if plan.assignGeneratedKey {
		descriptor.SetPrimaryKey(value, lastInsertID)
	}
	return nil
}

func resolveSaveFields[M any](metadata ir.Model, state saveOptionState[M]) ([]ir.Field, error) {
	if !state.fieldMaskSet {
		return nil, nil
	}
	selected := make(map[string]struct{}, len(state.typedFields)+len(state.dynamicNames))
	var zero M
	for _, typedField := range state.typedFields {
		if interfaceIsNil(typedField) {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnknownField,
				Detail:   "typed update field is nil",
			}
		}
		reference, err := typedField.writableField(zero)
		if err != nil {
			return nil, err
		}
		field, ok := mutationField(metadata, reference)
		if !ok {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnknownField,
				Field:    reference.Name(),
				Detail:   "typed update field is not bound to descriptor metadata",
			}
		}
		if field.PrimaryKey {
			return nil, primaryKeyUpdateField(field.Name)
		}
		selected[field.Name] = struct{}{}
	}
	for _, name := range state.dynamicNames {
		field, ok := namedMetadataField(metadata, name)
		if !ok {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnknownField,
				Field:    name,
				Detail:   "dynamic update field is not descriptor metadata",
			}
		}
		if field.PrimaryKey {
			return nil, primaryKeyUpdateField(field.Name)
		}
		selected[field.Name] = struct{}{}
	}
	fields := make([]ir.Field, 0, len(selected))
	for _, field := range metadata.Fields {
		if _, ok := selected[field.Name]; ok {
			fields = append(fields, field)
		}
	}
	return fields, nil
}

func nonPrimaryFields(metadata ir.Model) []ir.Field {
	fields := make([]ir.Field, 0, len(metadata.Fields)-1)
	for _, field := range metadata.Fields {
		if !field.PrimaryKey {
			fields = append(fields, field)
		}
	}
	return fields
}

func saveAssignments[M any](descriptor WriteDescriptor[M], value M, fields []ir.Field) ([]query.Assignment, error) {
	assignments := make([]query.Assignment, 0, len(fields))
	for _, field := range fields {
		if field.PrimaryKey {
			return nil, primaryKeyUpdateField(field.Name)
		}
		fieldValue, ok := descriptor.WriteFieldValue(value, field)
		if !ok || !mutationValueMatches(field, fieldValue) {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeInvalidValue,
				Field:    field.Name,
				Detail:   "descriptor returned an invalid save field value",
			}
		}
		assignments = append(assignments, NewAssignment(field, fieldValue))
	}
	return assignments, nil
}

func assignmentForField(assignments []query.Assignment, field ir.Field) (query.Assignment, bool) {
	reference := fieldReference(field)
	for _, assignment := range assignments {
		if assignment.Field().Equal(reference) {
			return assignment, true
		}
	}
	return query.Assignment{}, false
}

func namedMetadataField(metadata ir.Model, name string) (ir.Field, bool) {
	for _, field := range metadata.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return ir.Field{}, false
}

func primaryKeyUpdateField(name string) error {
	return &query.Error{
		Category: query.CategoryField,
		Code:     query.CodePrimaryKeyUpdateField,
		Field:    name,
		Detail:   "primary key cannot be included in an update field mask",
	}
}

func invalidSaveArgument(detail string) error {
	return &query.Error{
		Category: query.CategoryArgument,
		Code:     query.CodeInvalidPlan,
		Detail:   detail,
	}
}
