package postgres

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileScalarPredicate(expression query.Expression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	plan, err := query.NewPlan(source.Plan.Table(), source.Plan.SourceFields()).WithWhere(expression)
	if err != nil {
		return "", err
	}
	analysis, err := analyzeWhere(plan, plan.SourceFields())
	if err != nil {
		return "", err
	}
	var statement strings.Builder
	leaves := analysis.leaves
	if err := appendWhereExpression(&statement, analysis.expression, &leaves, func(condition query.Condition) (string, error) {
		return source.ResolveCondition(condition, quoteIdentifier, quoteQualified)
	}, func(field query.FieldRef) (string, error) {
		return source.ResolveField(field, quoteIdentifier, quoteQualified)
	}, source, arguments, false); err != nil {
		return "", err
	}
	if len(leaves) != 0 {
		return "", invalidPlan("conditional scalar has unused predicate leaves")
	}
	return statement.String(), nil
}
