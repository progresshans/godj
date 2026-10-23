package query

import "slices"

// ConflictInsertPlan inserts one row, doing nothing only on a conflict with
// the specified non-null unique tuple. It never requests a generated key.
// The caller binds the target to its model's owned uniqueness metadata; the
// compiler validates its complete assignment and native SQL enforces it.
type ConflictInsertPlan struct {
	table       string
	assignments []Assignment
	target      []FieldRef
}

func NewConflictInsertPlan(table string, assignments []Assignment, target []FieldRef) ConflictInsertPlan {
	return ConflictInsertPlan{table: table, assignments: slices.Clone(assignments), target: slices.Clone(target)}
}

func (p ConflictInsertPlan) Table() string             { return p.table }
func (p ConflictInsertPlan) Assignments() []Assignment { return slices.Clone(p.assignments) }
func (p ConflictInsertPlan) Target() []FieldRef        { return slices.Clone(p.target) }
func (p ConflictInsertPlan) Equal(other ConflictInsertPlan) bool {
	return p.table == other.table && slices.Equal(p.target, other.target) && slices.Equal(p.assignments, other.assignments)
}
