package query

import "slices"

// BulkUpdateSpec binds one selected-field matrix to a root primary key and
// the complete query predicate. It retains collection-filter identities;
// rebuilding Where as one new Filter would incorrectly merge separate joins.
// Ordering, distinct, row locks and eager result shapes do not constrain an
// UPDATE. Slices and prefetch windows are rejected rather than widened.
type BulkUpdateSpec struct {
	selection Plan
	fields    []FieldRef
	key       FieldRef
}

func NewBulkUpdateSpec(source Plan, fields []FieldRef, key FieldRef) (BulkUpdateSpec, error) {
	selection, err := updateSelection(source, key)
	if err != nil {
		return BulkUpdateSpec{}, err
	}
	spec := BulkUpdateSpec{selection: selection, fields: fields, key: key}
	if err := spec.Validate(); err != nil {
		return BulkUpdateSpec{}, err
	}
	spec.selection.sourceFields = slices.Clone(source.sourceFields)
	spec.fields = slices.Clone(fields)
	return spec, nil
}

func (spec BulkUpdateSpec) Selection() Plan    { return spec.selection }
func (spec BulkUpdateSpec) Fields() []FieldRef { return slices.Clone(spec.fields) }
func (spec BulkUpdateSpec) Key() FieldRef      { return spec.key }
func (spec BulkUpdateSpec) Equal(other BulkUpdateSpec) bool {
	return spec.key == other.key && slices.Equal(spec.fields, other.fields) && spec.selection.Equal(other.selection)
}

func (spec BulkUpdateSpec) Validate() error {
	if spec.selection.table == "" || spec.key.Name() == "" || spec.key.Column() == "" || spec.key.Kind() != FieldInteger || spec.key.Nullable() {
		return invalidPlanError("bulk update requires a table and a non-null integer primary key")
	}
	if len(spec.selection.sourceFields) == 0 || len(spec.selection.sourceFields) > MaximumBulkValues || len(spec.fields) == 0 || len(spec.fields) >= MaximumBulkValues {
		return invalidPlanError("bulk update requires bounded source fields and selected columns")
	}
	columns, names := make(map[string]FieldRef, len(spec.selection.sourceFields)), make(map[string]bool, len(spec.selection.sourceFields))
	for _, field := range spec.selection.sourceFields {
		if !field.ValidType() || field.Name() == "" || field.Column() == "" || names[field.Name()] {
			return invalidPlanError("bulk update source has an invalid or repeated field")
		}
		if _, repeated := columns[field.Column()]; repeated {
			return invalidPlanError("bulk update source repeats a column")
		}
		columns[field.Column()], names[field.Name()] = field, true
	}
	if columns[spec.key.Column()] != spec.key {
		return invalidPlanError("bulk update key differs from its source metadata")
	}
	seen := make(map[string]bool, len(spec.fields))
	for _, field := range spec.fields {
		if columns[field.Column()] != field || field == spec.key || seen[field.Column()] {
			return invalidPlanError("bulk update selects a foreign, primary or repeated field")
		}
		seen[field.Column()] = true
	}
	if spec.selection.where.node != nil {
		if err := spec.selection.where.validate(); err != nil {
			return err
		}
		if err := spec.selection.validateWhereSource(spec.selection.where); err != nil {
			return err
		}
	}
	return nil
}

// BulkUpdatePlan is one native statement. Keys may be repeated: the first
// input for a key in this statement wins. Later statements may update that key
// again. Missing keys are not inserted and need not contribute to the count.
// Key parameters are included in the portable value budget.
type BulkUpdatePlan struct {
	spec BulkUpdateSpec
	keys []int64
	rows [][]Value
}

func NewBulkUpdatePlan(spec BulkUpdateSpec, keys []int64, rows [][]Value) (BulkUpdatePlan, error) {
	plan := BulkUpdatePlan{spec: spec, keys: keys, rows: rows}
	if err := plan.Validate(); err != nil {
		return BulkUpdatePlan{}, err
	}
	plan.keys, plan.rows = slices.Clone(keys), cloneBulkRows(rows)
	return plan, nil
}

func (plan BulkUpdatePlan) Spec() BulkUpdateSpec { return plan.spec }
func (plan BulkUpdatePlan) Keys() []int64        { return slices.Clone(plan.keys) }
func (plan BulkUpdatePlan) Rows() [][]Value      { return cloneBulkRows(plan.rows) }
func (plan BulkUpdatePlan) RowCount() int        { return len(plan.keys) }
func (plan BulkUpdatePlan) ValueCount() int      { return len(plan.keys) * (len(plan.spec.fields) + 1) }
func (plan BulkUpdatePlan) Equal(other BulkUpdatePlan) bool {
	return plan.spec.Equal(other.spec) && slices.Equal(plan.keys, other.keys) && slices.EqualFunc(plan.rows, other.rows, slices.Equal[[]Value])
}

func (plan BulkUpdatePlan) Validate() error {
	if err := plan.spec.Validate(); err != nil {
		return err
	}
	if len(plan.keys) == 0 || len(plan.keys) > MaximumBulkRows || len(plan.keys) != len(plan.rows) || len(plan.keys) > MaximumBulkValues/(len(plan.spec.fields)+1) {
		return invalidPlanError("bulk update requires matching bounded keys and value rows")
	}
	for _, row := range plan.rows {
		if len(row) != len(plan.spec.fields) {
			return invalidPlanError("bulk update rows do not share the selected field order")
		}
	}
	return nil
}
