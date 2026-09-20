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

func appendRowSelection(statement *strings.Builder, selected, hidden []query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join) ([]any, error) {
	arguments := make([]any, 0)
	for index, expression := range selected {
		if index > 0 {
			statement.WriteString(", ")
		}
		if err := appendResultValue(statement, expression, alias, joins, &arguments, false); err != nil {
			return nil, err
		}
		if len(hidden) > 0 {
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
	return arguments, nil
}

func finishOrderedRows(inner string, plan query.Plan, selected, hidden []query.ResultExpression, alias string, joins map[queryplan.RelationKey]queryplan.Join, arguments []any) (string, []any, error) {
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
		} else if err := appendResultValue(&statement, expression, alias, joins, &arguments, true); err != nil {
			return "", nil, err
		}
		if ordering.Direction() == query.Ascending {
			statement.WriteString(" ASC")
		} else {
			statement.WriteString(" DESC")
		}
	}
	arguments = appendPagination(&statement, arguments, plan)
	return statement.String(), arguments, nil
}
