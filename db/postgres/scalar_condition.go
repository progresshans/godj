package postgres

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileScalarPredicate(expression query.Expression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	analysis, err := analyzePredicate(expression, source, source.Plan.SourceFields())
	if err != nil {
		return "", err
	}
	var conditions []query.Condition
	if len(source.Exists) != 0 {
		var visit func(query.Expression)
		visit = func(value query.Expression) {
			if condition, leaf := value.Condition(); leaf {
				conditions = append(conditions, condition)
			}
			for _, child := range value.Children() {
				visit(child)
			}
		}
		visit(expression)
	}
	for index, exists := range source.Exists {
		if source.QuoteTable == nil || index < 0 || index >= len(analysis.leaves) {
			return "", invalidPlan("collection predicate has no bounded table source")
		}
		qualify := source.ExistsQualify
		if qualify == nil {
			qualify = quoteQualified
		}
		prefix, err := queryplan.CollectionExistsPrefix(exists, source.QuoteTable, quoteIdentifier, qualify)
		if err != nil {
			return "", err
		}
		field, err := quoteQualified(exists.TerminalAlias, conditions[index].Field().Column())
		if err != nil {
			return "", err
		}
		analysis.leaves[index].existsPrefix, analysis.leaves[index].existsField = prefix, field
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
