package query

import "fmt"

// RelationSetNullPlan is the immutable, database-independent description of
// one bulk SET_NULL relation mutation. Backend compilers validate the complete
// plan before executing it, including values forged through the zero value.
type RelationSetNullPlan struct {
	table      string
	foreignKey FieldRef
	targetKey  Value
}

func NewRelationSetNullPlan(table string, foreignKey FieldRef, targetKey Value) RelationSetNullPlan {
	return RelationSetNullPlan{
		table:      table,
		foreignKey: foreignKey,
		targetKey:  targetKey,
	}
}

func (p RelationSetNullPlan) Table() string        { return p.table }
func (p RelationSetNullPlan) ForeignKey() FieldRef { return p.foreignKey }
func (p RelationSetNullPlan) TargetKey() Value     { return p.targetKey }
func (p RelationSetNullPlan) Equal(other RelationSetNullPlan) bool {
	return p == other
}

// ProtectedForeignKeyError reports the number of distinct source rows that
// prevent one relation-aware delete. The count is private so callers can only
// obtain a valid value through NewProtectedForeignKeyError.
type ProtectedForeignKeyError struct {
	protectedSourceRows int64
}

func NewProtectedForeignKeyError(protectedSourceRows int64) (*ProtectedForeignKeyError, error) {
	if protectedSourceRows <= 0 {
		return nil, &Error{
			Category: CategoryQuery,
			Code:     CodeInvalidPlan,
			Detail:   "protected source row count must be greater than zero",
		}
	}
	return &ProtectedForeignKeyError{protectedSourceRows: protectedSourceRows}, nil
}

func (e *ProtectedForeignKeyError) Error() string {
	classification := e.classification()
	if classification == nil {
		return "protected foreign key error"
	}
	return classification.Error()
}

// Unwrap exposes the stable query error taxonomy while the outer typed error
// retains the structured protected-row count.
func (e *ProtectedForeignKeyError) Unwrap() error {
	classification := e.classification()
	if classification == nil {
		return nil
	}
	return classification
}

func (e *ProtectedForeignKeyError) ProtectedSourceRows() int64 {
	if e == nil {
		return 0
	}
	return e.protectedSourceRows
}

func (e *ProtectedForeignKeyError) classification() *Error {
	if e == nil || e.protectedSourceRows <= 0 {
		return nil
	}
	return &Error{
		Category: CategoryIntegrity,
		Code:     CodeProtectedForeignKey,
		Detail:   fmt.Sprintf("delete is blocked by %d protected source rows", e.protectedSourceRows),
	}
}
