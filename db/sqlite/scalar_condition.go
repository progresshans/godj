package sqlite

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func sqliteComparisonValue(condition query.Condition, leftSQL, rightSQL string) (string, string, error) {
	left, _ := condition.Scalar()
	right, _ := condition.RHSScalar()
	if left.RequiresDurationFraction() || right.RequiresDurationFraction() {
		var err error
		leftSQL, err = queryplan.ScalarSQL("godj_duration_fraction(", leftSQL, ")")
		if err != nil {
			return "", "", err
		}
		rightSQL, err = queryplan.ScalarSQL("godj_duration_fraction(", rightSQL, ")")
		return leftSQL, rightSQL, err
	}
	return leftSQL, rightSQL, nil
}

func sqliteGroupComparisonLiteral(expression query.ResultExpression, statement string) (string, error) {
	if expression.RequiresDurationFraction() {
		return queryplan.ScalarSQL("godj_duration_fraction(", statement, ")")
	}
	return statement, nil
}

func compileScalarPredicate(expression query.Expression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	analysis, err := analyzePredicate(expression, source)
	if err != nil {
		return "", err
	}
	for index, leaf := range analysis.leaves {
		if _, computed := leaf.condition.Scalar(); computed {
			leaf.scalarSource = source
			continue
		}
		if exists, present := source.Exists[index]; present {
			if source.QuoteTable == nil || len(exists.Joins) > 63 {
				return "", unsupportedResult("collection predicate has no bounded table source")
			}
			qualify := source.ExistsQualify
			if qualify == nil {
				qualify = quoteQualified
			}
			leaf.existsPrefix, err = queryplan.CollectionExistsPrefix(exists, source.QuoteTable, quoteIdentifier, qualify)
			if err != nil {
				return "", err
			}
			leaf.fieldSQL, err = quoteQualified(exists.TerminalAlias, leaf.condition.Field().Column())
		} else {
			leaf.fieldSQL, err = source.ResolveCondition(leaf.condition, quoteIdentifier, quoteQualified)
		}
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
