package query

import "slices"

// ConflictInsertPlan inserts one row, doing nothing only on a conflict with
// the specified non-null unique tuple. The embedded insert requests its actual
// generated key; a skipped row must not report a previous or conflicting key.
// The caller binds the target to its model's owned uniqueness metadata; the
// compiler validates its complete assignment and native SQL enforces it.
type ConflictInsertPlan struct {
	insert InsertPlan
	target []FieldRef
}

func NewConflictInsertPlan(insert InsertPlan, target []FieldRef) ConflictInsertPlan {
	return ConflictInsertPlan{insert: insert, target: slices.Clone(target)}
}

func (p ConflictInsertPlan) Insert() InsertPlan        { return p.insert }
func (p ConflictInsertPlan) Table() string             { return p.insert.Table() }
func (p ConflictInsertPlan) Assignments() []Assignment { return p.insert.Assignments() }
func (p ConflictInsertPlan) Target() []FieldRef        { return slices.Clone(p.target) }
func (p ConflictInsertPlan) Equal(other ConflictInsertPlan) bool {
	return p.insert.Equal(other.insert) && slices.Equal(p.target, other.target)
}
