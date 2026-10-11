package postgres

import (
	"strings"

	"github.com/progresshans/godj/query"
)

func compileScalarPredicate(expression query.Expression, resolve func(query.FieldRef) (string, error), arguments *[]any) (string, error) {
	if expression.HasRelations() {
		return "", unsupportedResultShape("conditional scalar predicates require same-row fields")
	}
	fields := expression.SourceFields()
	plan, err := query.NewPlan("", fields).WithWhere(expression)
	if err != nil {
		return "", err
	}
	analysis, err := analyzeWhere(plan, fields)
	if err != nil {
		return "", err
	}
	var statement strings.Builder
	leaves := analysis.leaves
	if err := appendWhereExpression(&statement, analysis.expression, &leaves, func(condition query.Condition) (string, error) {
		return resolve(condition.Field())
	}, resolve, arguments, false); err != nil {
		return "", err
	}
	if len(leaves) != 0 {
		return "", invalidPlan("conditional scalar has unused predicate leaves")
	}
	return statement.String(), nil
}
