package queryplan

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// UpdateDialect owns physical identifiers, values, parameter syntax and
// numeric execution. Shared traversal owns argument order and row selection.
type UpdateDialect struct {
	ParameterLimit              int
	Source                      func(query.Plan) (UpdateSource, error)
	QuoteTable, QuoteIdentifier func(string) (string, error)
	ColumnKey                   func(string) string
	Value                       func(query.Value) (any, error)
	Placeholder                 func(int) string
	Literal                     func(query.Value, query.FieldKind, string) (string, error)
	Expression                  func(query.ScalarExpression, string) (string, error)
}

func CompileQueryUpdate(plan query.QueryUpdatePlan, dialect UpdateDialect) (string, []any, error) {
	if err := plan.Validate(); err != nil {
		return "", nil, err
	}
	selection := plan.Selection()
	table, err := dialect.QuoteTable(selection.Table())
	if err != nil {
		return "", nil, err
	}
	key, err := dialect.QuoteIdentifier(plan.Key().Column())
	if err != nil {
		return "", nil, err
	}
	columnKey := dialect.ColumnKey
	if columnKey == nil {
		columnKey = func(value string) string { return value }
	}
	seen := make(map[string]bool)
	for _, field := range selection.SourceFields() {
		name := columnKey(field.Column())
		if seen[name] {
			return "", nil, invalidPlan("query update repeats a physical source column")
		}
		seen[name] = true
	}
	source, err := dialect.Source(selection)
	if err != nil {
		return "", nil, err
	}
	if source.Direct && source.Selection != "" || !source.Direct && (source.Selection == "" || source.Where != "" || source.Alias != "") {
		return "", nil, invalidPlan("query update compiler returned an ambiguous source")
	}
	arguments := slices.Clone(source.Arguments)
	if dialect.ParameterLimit <= 0 || len(arguments) > dialect.ParameterLimit {
		return "", nil, invalidPlan("query update predicate exceeds its parameter budget")
	}
	var scalar func(query.ScalarExpression, query.FieldKind) (string, error)
	scalar = func(expression query.ScalarExpression, expected query.FieldKind) (string, error) {
		var sql string
		if value, ok := expression.Literal(); ok {
			if len(arguments) >= dialect.ParameterLimit {
				return "", invalidPlan("query update exceeds its parameter budget")
			}
			argument, err := dialect.Value(value)
			if err != nil {
				return "", err
			}
			arguments = append(arguments, argument)
			sql = dialect.Placeholder(len(arguments))
			if dialect.Literal != nil {
				sql, err = dialect.Literal(value, expected, sql)
				if err != nil {
					return "", err
				}
			}
		} else if field, ok := expression.Field(); ok {
			sql, err = dialect.QuoteIdentifier(field.Column())
			if err != nil {
				return "", err
			}
			if source.Alias != "" {
				sql = source.Alias + "." + sql
			}
		} else if operator, left, right, ok := expression.Binary(); ok {
			leftSQL, err := scalar(left, expression.ResultKind())
			if err != nil {
				return "", err
			}
			rightSQL, err := scalar(right, expression.ResultKind())
			if err != nil {
				return "", err
			}
			sql = "(" + leftSQL + " " + string(operator) + " " + rightSQL + ")"
		} else if value, ok := expression.Negated(); ok {
			inner, err := scalar(value, expression.ResultKind())
			if err != nil {
				return "", err
			}
			sql = "(-" + inner + ")"
		} else {
			return "", invalidPlan("query update has an unknown scalar node")
		}
		if dialect.Expression != nil {
			return dialect.Expression(expression, sql)
		}
		return sql, nil
	}
	assignments := plan.Assignments()
	clauses := make([]string, len(assignments))
	for index, assignment := range assignments {
		column, err := dialect.QuoteIdentifier(assignment.Field().Column())
		if err != nil {
			return "", nil, err
		}
		value, err := scalar(assignment.Expression(), assignment.Field().Kind())
		if err != nil {
			return "", nil, err
		}
		clauses[index] = column + " = " + value
	}
	if len(assignments) == 0 {
		return "", nil, nil
	}
	statement := "UPDATE " + table
	if source.Direct {
		if source.Alias != "" {
			statement += " AS " + source.Alias
		}
		return statement + " SET " + strings.Join(clauses, ", ") + source.Where, arguments, nil
	}
	// Materialize membership before any assigned key changes. Preserve every
	// collection filter identity and deduplicate joined matches inside SQL.
	tables := map[string]bool{columnKey(selection.Table()): true}
	for _, condition := range selection.Conditions() {
		if path, ok := condition.RelationPath(); ok {
			for _, hop := range path.Hops() {
				tables[columnKey(hop.SourceTable())], tables[columnKey(hop.TargetTable())] = true, true
			}
		}
	}
	scope := "godj_update_targets"
	for index := 0; tables[columnKey(scope)]; index++ {
		scope = "godj_update_targets_" + strconv.Itoa(index)
	}
	scope, err = dialect.QuoteIdentifier(scope)
	if err != nil {
		return "", nil, err
	}
	return `WITH ` + scope + ` ("godj_update_key") AS MATERIALIZED (SELECT DISTINCT "godj_update_input".` + key + ` FROM (` +
		source.Selection + `) AS "godj_update_input") ` + statement + " SET " + strings.Join(clauses, ", ") +
		" WHERE " + key + ` IN (SELECT "godj_update_key" FROM ` + scope + ")", arguments, nil
}

func ExecuteQueryUpdate(ctx context.Context, executor BulkUpdateExecutor, plan query.QueryUpdatePlan, statement string, arguments []any, classify func(error) error) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if plan.NoOp() {
		return 0, nil
	}
	if statement == "" {
		return 0, invalidPlan("query update compiled an empty statement")
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classify(err)
	}
	if result == nil {
		return 0, invalidPlan("query update returned no result metadata")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read query update affected rows: %w", err)
	}
	if count < 0 {
		return 0, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows, Detail: "query update returned a negative matched count"}
	}
	return count, nil
}
