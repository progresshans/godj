package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
	"strconv"
	"strings"
)

// Preflight runs before aggregate/empty-source elision. Query owns expression
// identity; each backend still validates its identifiers and path capabilities.
func validateOrderings(plan query.Plan) error {
	if err := plan.ValidateOrderings(); err != nil {
		return err
	}
	for _, ordering := range plan.Orderings() {
		field := ordering.Field()
		if _, err := quoteIdentifier(field.Name()); err != nil {
			return err
		}
		if _, err := quoteIdentifier(field.Column()); err != nil {
			return err
		}
		if path, ok := ordering.Expression().JSONPath(); ok {
			if err := validateJSONPath(field, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendResultValue(statement *strings.Builder, expression query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join, arguments *[]any, ordering bool) error {
	field, _ := expression.Field()
	if path, related := expression.RelationPath(); related {
		joined, ok := joins[queryplan.KeyForPath(path.Hops())]
		if !ok {
			return invalidPlan("value relation was not materialized")
		}
		alias = joined.Alias
	}
	var column string
	var err error
	if alias == "" {
		column, err = quoteIdentifier(field.Column())
	} else {
		column, err = quoteQualified(alias, field.Column())
	}
	if err != nil {
		return err
	}
	if path, ok := expression.JSONPath(); ok {
		if err := validateJSONPath(field, path); err != nil {
			return err
		}
		appendJSONPath(statement, column, path, arguments)
	} else {
		statement.WriteString(column)
		if !ordering {
			appendDecimalResultPrecision(statement, field)
		}
	}
	return nil
}

func appendRowSelection(statement *strings.Builder, plan query.Plan, selected, hidden []query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join) ([]any, error) {
	arguments := make([]any, 0)
	_, window := plan.PrefetchWindow()
	for index, expression := range selected {
		if index > 0 {
			statement.WriteString(", ")
		}
		if err := appendResultValue(statement, expression, alias, joins, &arguments, false); err != nil {
			return nil, err
		}
		if len(hidden) > 0 || window {
			statement.WriteString(` AS "c` + strconv.Itoa(index) + `"`)
		}
	}
	for index, expression := range hidden {
		statement.WriteString(", ")
		if err := appendResultValue(statement, expression, alias, joins, &arguments, true); err != nil {
			return nil, err
		}
		statement.WriteString(` AS "o` + strconv.Itoa(index) + `"`)
	}
	if err := queryplan.AppendPrefetchWindow(statement, plan, func(expression query.ResultExpression) error {
		return appendResultValue(statement, expression, alias, joins, &arguments, true)
	}); err != nil {
		return nil, err
	}
	return arguments, nil
}

func finishOrderedRows(inner string, plan query.Plan, selected, hidden []query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join, arguments []any) (string, []any, error) {
	if _, window := plan.PrefetchWindow(); window {
		sql, err := queryplan.FinishPrefetchRows(inner, plan, selected, hidden, quoteIdentifier, func(value int64) string {
			arguments = append(arguments, value)
			return "$" + strconv.Itoa(len(arguments))
		}, func(values []query.Value) (string, bool) {
			packed, ok := queryplan.PackIntegerMembership(values, '{', '}')
			if !ok {
				return "", false
			}
			arguments = append(arguments, packed)
			return " = ANY($" + strconv.Itoa(len(arguments)) + "::bigint[])", true
		})
		return sql, arguments, err
	}
	if len(hidden) > 0 {
		var err error
		inner, err = queryplan.WrapHiddenOrderings(inner, selected, quoteIdentifier)
		if err != nil {
			return "", nil, err
		}
	}
	var statement strings.Builder
	statement.WriteString(inner)
	for index, ordering := range plan.Orderings() {
		if index == 0 {
			statement.WriteString(" ORDER BY ")
		} else {
			statement.WriteString(", ")
		}
		expression := ordering.Expression()
		if len(hidden) > 0 {
			statement.WriteString(queryplan.HiddenOrderingColumn(selected, hidden, expression))
		} else if _, path := expression.JSONPath(); path && plan.Distinct() {
			// Reuse the selected cell: two bound path parameters are not the same
			// PostgreSQL DISTINCT expression, even when their values happen to match.
			statement.WriteString(strconv.Itoa(queryplan.ExpressionIndex(selected, expression) + 1))
		} else if err := appendResultValue(&statement, expression, alias, joins, &arguments, true); err != nil {
			return "", nil, err
		}
		if ordering.Direction() == query.Ascending {
			statement.WriteString(" ASC")
		} else {
			statement.WriteString(" DESC")
		}
	}
	appendPagination(&statement, &arguments, plan)
	return statement.String(), arguments, nil
}
