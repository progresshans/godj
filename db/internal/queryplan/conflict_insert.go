package queryplan

import (
	"strings"

	"github.com/progresshans/godj/query"
)

type ConflictInsertParts struct {
	Columns, Target []string
	Key             string
	Arguments       []any
}

// PrepareConflictInsert validates all values before compiling an explicit conflict
// target. Every target member must have the exact metadata and a non-NULL
// assignment. Nullable columns still enforce uniqueness for non-NULL tuples.
func PrepareConflictInsert(plan query.ConflictInsertPlan, validateValue func(query.FieldRef, query.Value) error, quoteIdentifier func(string) (string, error), columnKey func(string) string, encodeValue func(query.Value) (any, error)) (ConflictInsertParts, error) {
	var zero ConflictInsertParts
	key, present := plan.Insert().ReturningKey()
	if !present || key.Kind() != query.FieldInteger || key.Nullable() || key.Name() == "" || strings.ContainsRune(key.Name(), '\x00') {
		return zero, invalidPlan("conflict insert requires one named non-null integer returning key")
	}
	if err := validateValue(key, query.Integer(0)); err != nil {
		return zero, err
	}
	keyColumn, err := quoteIdentifier(key.Column())
	if err != nil {
		return zero, err
	}
	assignments, target := plan.Assignments(), plan.Target()
	if len(target) == 0 || len(target) > len(assignments) {
		return zero, invalidPlan("conflict insert requires a nonempty, fully assigned unique tuple")
	}
	columns, arguments, err := Assignments(assignments, validateValue, quoteIdentifier, columnKey, encodeValue)
	if err != nil {
		return zero, err
	}
	if columnKey == nil {
		columnKey = func(value string) string { return value }
	}
	assigned := make(map[string]query.Assignment, len(assignments))
	for _, assignment := range assignments {
		if columnKey(assignment.Field().Column()) == columnKey(key.Column()) && !assignment.Field().Equal(key) {
			return zero, invalidPlan("conflict insert aliases its returning key")
		}
		assigned[columnKey(assignment.Field().Column())] = assignment
	}
	seen := make(map[string]bool, len(target))
	conflictColumns := make([]string, len(target))
	for index, field := range target {
		key := columnKey(field.Column())
		assignment, present := assigned[key]
		if seen[key] || !present || !field.Equal(assignment.Field()) || assignment.Value().IsNull() ||
			field.Name() == "" || strings.ContainsRune(field.Name(), '\x00') {
			return zero, invalidPlan("conflict target must contain distinct, named, assigned non-null fields")
		}
		seen[key] = true
		column, err := quoteIdentifier(field.Column())
		if err != nil {
			return zero, err
		}
		conflictColumns[index] = column
	}
	return ConflictInsertParts{Columns: columns, Arguments: arguments, Target: conflictColumns, Key: keyColumn}, nil
}
