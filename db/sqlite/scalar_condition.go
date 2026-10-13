package sqlite

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
	analysis, err := analyzeWhere(plan)
	if err != nil {
		return "", err
	}
	for _, leaf := range analysis.leaves {
		if _, computed := leaf.condition.Scalar(); computed {
			leaf.scalarSource = source
			continue
		}
		leaf.fieldSQL, err = source.ResolveCondition(leaf.condition, quoteIdentifier, quoteQualified)
		if err != nil {
			return "", err
		}
		if right, present := leaf.condition.RHSField(); present {
			leaf.rhsFieldSQL, err = source.ResolveField(right, quoteIdentifier, quoteQualified)
			if err != nil {
				return "", err
			}
		}
	}
	var statement strings.Builder
	if err := appendWhereNode(&statement, analysis.root, false, false, arguments); err != nil {
		return "", err
	}
	return statement.String(), nil
}
