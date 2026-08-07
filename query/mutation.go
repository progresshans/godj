package query

import "slices"

// Assignment binds one typed field to one explicit scalar or NULL value.
// Its fields are private so values returned from a plan cannot mutate the
// plan's storage.
type Assignment struct {
	field FieldRef
	value Value
}

func NewAssignment(field FieldRef, value Value) Assignment {
	return Assignment{field: field, value: value}
}

func (a Assignment) Field() FieldRef { return a.field }
func (a Assignment) Value() Value    { return a.value }
func (a Assignment) Equal(other Assignment) bool {
	return a.field == other.field && a.value == other.value
}

// InsertPlan is an immutable, database-independent one-row insert plan.
type InsertPlan struct {
	table       string
	assignments []Assignment
}

func NewInsertPlan(table string, assignments []Assignment) InsertPlan {
	return InsertPlan{table: table, assignments: append([]Assignment(nil), assignments...)}
}

func (p InsertPlan) Table() string { return p.table }
func (p InsertPlan) Assignments() []Assignment {
	return append([]Assignment(nil), p.assignments...)
}
func (p InsertPlan) Equal(other InsertPlan) bool {
	return p.table == other.table && slices.EqualFunc(p.assignments, other.assignments, func(left, right Assignment) bool {
		return left.Equal(right)
	})
}

// UpdatePlan is an immutable, database-independent explicit-field update.
// The key predicate is deliberately separate from assignments so generated
// patches cannot accidentally overwrite the primary key.
type UpdatePlan struct {
	table       string
	assignments []Assignment
	keyField    FieldRef
	keyValue    Value
}

func NewUpdatePlan(table string, assignments []Assignment, keyField FieldRef, keyValue Value) UpdatePlan {
	return UpdatePlan{
		table:       table,
		assignments: append([]Assignment(nil), assignments...),
		keyField:    keyField,
		keyValue:    keyValue,
	}
}

func (p UpdatePlan) Table() string { return p.table }
func (p UpdatePlan) Assignments() []Assignment {
	return append([]Assignment(nil), p.assignments...)
}
func (p UpdatePlan) KeyField() FieldRef { return p.keyField }
func (p UpdatePlan) KeyValue() Value    { return p.keyValue }
func (p UpdatePlan) Equal(other UpdatePlan) bool {
	return p.table == other.table && p.keyField == other.keyField && p.keyValue == other.keyValue &&
		slices.EqualFunc(p.assignments, other.assignments, func(left, right Assignment) bool {
			return left.Equal(right)
		})
}

// DeletePlan is an immutable, database-independent one-key delete plan.
type DeletePlan struct {
	table    string
	keyField FieldRef
	keyValue Value
}

func NewDeletePlan(table string, keyField FieldRef, keyValue Value) DeletePlan {
	return DeletePlan{table: table, keyField: keyField, keyValue: keyValue}
}

func (p DeletePlan) Table() string      { return p.table }
func (p DeletePlan) KeyField() FieldRef { return p.keyField }
func (p DeletePlan) KeyValue() Value    { return p.keyValue }
func (p DeletePlan) Equal(other DeletePlan) bool {
	return p == other
}
