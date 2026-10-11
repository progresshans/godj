package sqlite

import (
	"strings"

	"github.com/progresshans/godj/query"
)

func compileScalarPredicate(expression query.Expression, resolve func(query.FieldRef) (string, error), arguments *[]any) (string, error) {
	if expression.HasRelations() {
		return "", unsupportedResult("conditional scalar predicates require same-row fields")
	}
	plan, err := query.NewPlan("", expression.SourceFields()).WithWhere(expression)
	if err != nil {
		return "", err
	}
	analysis, err := analyzeWhere(plan)
	if err != nil {
		return "", err
	}
	for _, leaf := range analysis.leaves {
		if _, computed := leaf.condition.Scalar(); computed {
			leaf.scalarResolver = resolve
			continue
		}
		leaf.fieldSQL, err = resolve(leaf.condition.Field())
		if err != nil {
			return "", err
		}
		if right, present := leaf.condition.RHSField(); present {
			leaf.rhsFieldSQL, err = resolve(right)
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
