package query

import "slices"

// BulkConflictMode selects the native conflict action for a batch. Ignore
// suppresses uniqueness conflicts only; it does not suppress CHECK, NOT NULL,
// foreign-key or operational errors. Update requires an explicit unique target.
type BulkConflictMode string

const (
	BulkConflictError  BulkConflictMode = ""
	BulkConflictIgnore BulkConflictMode = "ignore"
	BulkConflictUpdate BulkConflictMode = "update"
)

// BulkConflict owns its target and update columns. The ORM binds a requested
// target to Schema IR uniqueness; a backend validates the physical statement.
// Its zero value uses the normal constraint-error policy.
type BulkConflict struct {
	mode   BulkConflictMode
	target []FieldRef
	update []FieldRef
}

func NewBulkConflict(mode BulkConflictMode, target, update []FieldRef) (BulkConflict, error) {
	policy := BulkConflict{mode: mode, target: target, update: update}
	if err := policy.validate(); err != nil {
		return BulkConflict{}, err
	}
	policy.target, policy.update = slices.Clone(target), slices.Clone(update)
	return policy, nil
}

func (policy BulkConflict) Mode() BulkConflictMode { return policy.mode }
func (policy BulkConflict) Target() []FieldRef     { return slices.Clone(policy.target) }
func (policy BulkConflict) Update() []FieldRef     { return slices.Clone(policy.update) }
func (policy BulkConflict) Equal(other BulkConflict) bool {
	return policy.mode == other.mode && slices.Equal(policy.target, other.target) && slices.Equal(policy.update, other.update)
}

func (policy BulkConflict) validate() error {
	switch policy.mode {
	case BulkConflictError, BulkConflictIgnore:
		if len(policy.target) != 0 || len(policy.update) != 0 {
			return invalidPlanError("bulk conflict target and update columns require the update policy")
		}
	case BulkConflictUpdate:
		if len(policy.target) == 0 || len(policy.update) == 0 {
			return invalidPlanError("bulk conflict update requires a unique target and update columns")
		}
		for _, fields := range [][]FieldRef{policy.target, policy.update} {
			if len(fields) > MaximumBulkValues {
				return invalidPlanError("bulk conflict exceeds its field bound")
			}
			seen := make(map[string]bool, len(fields))
			for _, field := range fields {
				if !field.ValidType() || field.Name() == "" || field.Column() == "" || seen[field.Column()] {
					return invalidPlanError("bulk conflict contains an invalid or repeated column")
				}
				seen[field.Column()] = true
			}
		}
	default:
		return invalidPlanError("unknown bulk conflict policy")
	}
	return nil
}

// A plan represents one bounded native statement. ORM orchestration divides a
// larger input according to the backend's parameter and row limits, retaining
// one transaction around all statements. These bounds apply before copying.
const MaximumBulkRows = 65535
const MaximumBulkValues = 65535

// BulkInsertPlan is an immutable matrix of rows in one common field order.
// Empty fields with nonempty rows represent an auto-key-only model. An explicit
// primary-key column must match Key exactly; generated-key rows omit it.
// Mixing the two shapes is handled by the operation's transaction owner.
type BulkInsertPlan struct {
	table    string
	fields   []FieldRef
	rows     [][]Value
	key      FieldRef
	conflict BulkConflict
}

func NewBulkInsertPlan(table string, fields []FieldRef, rows [][]Value, key FieldRef, conflict BulkConflict) (BulkInsertPlan, error) {
	plan := BulkInsertPlan{table: table, fields: fields, rows: rows, key: key, conflict: conflict}
	if err := plan.Validate(); err != nil {
		return BulkInsertPlan{}, err
	}
	plan.fields = slices.Clone(fields)
	plan.rows = cloneBulkRows(rows)
	return plan, nil
}

func (plan BulkInsertPlan) Table() string          { return plan.table }
func (plan BulkInsertPlan) Fields() []FieldRef     { return slices.Clone(plan.fields) }
func (plan BulkInsertPlan) Rows() [][]Value        { return cloneBulkRows(plan.rows) }
func (plan BulkInsertPlan) Key() FieldRef          { return plan.key }
func (plan BulkInsertPlan) Conflict() BulkConflict { return plan.conflict }
func (plan BulkInsertPlan) RowCount() int          { return len(plan.rows) }
func (plan BulkInsertPlan) ValueCount() int        { return len(plan.fields) * len(plan.rows) }
func (plan BulkInsertPlan) ReturnsKeys() bool      { return plan.conflict.mode != BulkConflictIgnore }
func (plan BulkInsertPlan) Equal(other BulkInsertPlan) bool {
	return plan.table == other.table && plan.key == other.key && plan.conflict.Equal(other.conflict) &&
		slices.Equal(plan.fields, other.fields) && slices.EqualFunc(plan.rows, other.rows, slices.Equal[[]Value])
}

// Validate checks the portable shape and field provenance even for a zero
// value. Backend identifier, scalar encoding and parameter limits are separate.
func (plan BulkInsertPlan) Validate() error {
	if plan.table == "" || plan.key.Kind() != FieldInteger || plan.key.Nullable() || plan.key.Name() == "" || plan.key.Column() == "" {
		return invalidPlanError("bulk insert requires a table and a non-null integer primary key")
	}
	if len(plan.rows) == 0 || len(plan.rows) > MaximumBulkRows || len(plan.fields) > MaximumBulkValues ||
		(len(plan.fields) > 0 && len(plan.rows) > MaximumBulkValues/len(plan.fields)) {
		return invalidPlanError("bulk insert exceeds its row or value bounds, or has no rows")
	}
	if err := plan.conflict.validate(); err != nil {
		return err
	}
	columns := make(map[string]FieldRef, len(plan.fields))
	names := make(map[string]bool, len(plan.fields))
	for _, field := range plan.fields {
		if !field.ValidType() || field.Name() == "" || field.Column() == "" || names[field.Name()] {
			return invalidPlanError("bulk insert has an invalid or repeated field")
		}
		if _, repeated := columns[field.Column()]; repeated {
			return invalidPlanError("bulk insert repeats a column")
		}
		if (field.Name() == plan.key.Name() || field.Column() == plan.key.Column()) && field != plan.key {
			return invalidPlanError("bulk insert primary-key metadata disagrees with the declared key")
		}
		columns[field.Column()], names[field.Name()] = field, true
	}
	for _, row := range plan.rows {
		if len(row) != len(plan.fields) {
			return invalidPlanError("bulk insert rows do not share their declared field order")
		}
	}
	for _, field := range plan.conflict.target {
		if columns[field.Column()] != field && field != plan.key {
			return invalidPlanError("bulk conflict target is not a declared insert field or primary key")
		}
	}
	for _, field := range plan.conflict.update {
		if field == plan.key || field.Name() == plan.key.Name() || field.Column() == plan.key.Column() || columns[field.Column()] != field {
			return invalidPlanError("bulk conflict update must select declared non-primary insert fields")
		}
	}
	return nil
}

func cloneBulkRows(rows [][]Value) [][]Value {
	result := make([][]Value, len(rows))
	for index, row := range rows {
		result[index] = slices.Clone(row)
	}
	return result
}
