package sqlite

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
		if scalar, present := ordering.Expression().Scalar(); present {
			arguments := []any{}
			prepared, err := queryplan.PrepareJoins(plan, "sqlite")
			if err != nil {
				return err
			}
			if _, err := compileReadScalar(scalar, queryplan.ScalarSource{Plan: plan, Alias: "t0", Joins: prepared.ByKey}, &arguments); err != nil {
				return err
			}
			continue
		}
		field := ordering.Field()
		if _, err := quoteIdentifier(field.Name()); err != nil {
			return err
		}
		if _, err := quoteIdentifier(field.Column()); err != nil {
			return err
		}
	}
	return nil
}

func appendResultValue(statement *strings.Builder, expression query.ResultExpression, plan query.Plan, alias string, joins map[queryplan.RelationKey]queryplan.Join, arguments *[]any, ordering bool) error {
	return appendSourceResultValue(statement, expression, queryplan.ScalarSource{Plan: plan, Alias: alias, Joins: joins, Aggregate: expression.IsAggregateCall()}, arguments, ordering)
}

func appendSourceResultValue(statement *strings.Builder, expression query.ResultExpression, source queryplan.ScalarSource, arguments *[]any, ordering bool) error {
	if scalar, present := expression.Scalar(); present {
		value, err := compileReadScalar(scalar, source, arguments)
		if err != nil {
			return err
		}
		if ordering && scalar.ResultKind() == query.FieldJSON {
			statement.WriteString("godj_json_sort_key(" + value + ")")
		} else {
			statement.WriteString(value)
		}
		return nil
	}
	field, _ := expression.Field()
	var column string
	var err error
	if path, related := expression.RelationPath(); related {
		column, err = source.ResolveRelated(path, quoteQualified)
	} else {
		column, err = source.ResolveField(field, quoteIdentifier, quoteQualified)
	}
	if err != nil {
		return err
	}
	if path, ok := expression.JSONPath(); ok {
		if ordering {
			statement.WriteString("godj_json_sort_key(")
		}
		statement.WriteString("godj_json_at(" + column + ", ?)")
		*arguments = append(*arguments, sqliteJSONPathArgument(path))
		if ordering {
			statement.WriteByte(')')
		}
	} else {
		statement.WriteString(column)
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
		if err := appendResultValue(statement, expression, plan, alias, joins, &arguments, false); err != nil {
			return nil, err
		}
		if len(hidden) > 0 || window {
			statement.WriteString(` AS "c` + strconv.Itoa(index) + `"`)
		}
	}
	for index, expression := range hidden {
		statement.WriteString(", ")
		if err := appendResultValue(statement, expression, plan, alias, joins, &arguments, true); err != nil {
			return nil, err
		}
		statement.WriteString(` AS "o` + strconv.Itoa(index) + `"`)
	}
	if err := queryplan.AppendPrefetchWindow(statement, plan, func(expression query.ResultExpression) error {
		return appendResultValue(statement, expression, plan, alias, joins, &arguments, true)
	}); err != nil {
		return nil, err
	}
	return arguments, nil
}

func finishOrderedRows(inner string, plan query.Plan, selected, hidden []query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join, arguments []any) (string, []any, error) {
	if _, window := plan.PrefetchWindow(); window {
		sql, err := queryplan.FinishPrefetchRows(inner, plan, selected, hidden, quoteIdentifier, func(value int64) string { arguments = append(arguments, value); return "?" }, func(values []query.Value) (string, bool) {
			packed, ok := queryplan.PackIntegerMembership(values, '[', ']')
			if !ok {
				return "", false
			}
			arguments = append(arguments, packed)
			return " IN (SELECT value FROM json_each(?))", true
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
		} else if _, scalar := expression.Scalar(); scalar && expression.ResultValueKind() != query.FieldJSON && queryplan.ExpressionIndex(selected, expression) >= 0 {
			statement.WriteString(strconv.Itoa(queryplan.ExpressionIndex(selected, expression) + 1))
		} else if err := appendResultValue(&statement, expression, plan, alias, joins, &arguments, true); err != nil {
			return "", nil, err
		}
		queryplan.AppendOrderDirection(&statement, ordering.Direction(), ordering.Nulls())
	}
	arguments = appendPagination(&statement, arguments, plan)
	return statement.String(), arguments, nil
}
