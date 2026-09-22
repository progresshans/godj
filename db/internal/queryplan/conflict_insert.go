package queryplan

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
)

// ConflictInsert validates all values before compiling an explicit conflict
// target. Every target member must be the exact assigned non-null field.
func ConflictInsert(plan query.ConflictInsertPlan, validateValue func(query.FieldRef, query.Value) error, quoteIdentifier func(string) (string, error), columnKey func(string) string, encodeValue func(query.Value) (any, error)) ([]string, []any, []string, error) {
	assignments, target := plan.Assignments(), plan.Target()
	if len(target) == 0 || len(target) > len(assignments) {
		return nil, nil, nil, invalidPlan("conflict insert requires a nonempty, fully assigned unique tuple")
	}
	columns, arguments, err := Assignments(assignments, validateValue, quoteIdentifier, columnKey, encodeValue)
	if err != nil {
		return nil, nil, nil, err
	}
	if columnKey == nil {
		columnKey = func(value string) string { return value }
	}
	assigned := make(map[string]query.Assignment, len(assignments))
	for _, assignment := range assignments {
		assigned[columnKey(assignment.Field().Column())] = assignment
	}
	seen := make(map[string]bool, len(target))
	conflictColumns := make([]string, len(target))
	for index, field := range target {
		key := columnKey(field.Column())
		assignment, present := assigned[key]
		if seen[key] || !present || !field.Equal(assignment.Field()) || field.Nullable() || assignment.Value().IsNull() ||
			field.Name() == "" || strings.ContainsRune(field.Name(), '\x00') {
			return nil, nil, nil, invalidPlan("conflict target must contain distinct, named, assigned non-null fields")
		}
		seen[key] = true
		column, err := quoteIdentifier(field.Column())
		if err != nil {
			return nil, nil, nil, err
		}
		conflictColumns[index] = column
	}
	return columns, arguments, conflictColumns, nil
}

// ConflictInsertResult does not ask for LastInsertId: on a skipped SQLite
// insert that value can be an unrelated previous row's identifier.
func ConflictInsertResult(result sql.Result) (bool, error) {
	if result == nil {
		return false, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "conflict insert returned a nil result"}
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read conflict insert rows affected: %w", err)
	}
	if count != 0 && count != 1 {
		return false, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows,
			Detail: fmt.Sprintf("conflict insert affected %d rows, want 0 or 1", count)}
	}
	return count == 1, nil
}
