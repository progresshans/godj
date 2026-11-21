package postgres

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileAnnotated(schema string, plan query.Plan, input string, inputArguments []any) (string, []any, error) {
	if err := validateSchemaIdentifier(schema); err != nil {
		return "", nil, invalidPlan(err.Error())
	}
	if _, _, derived := plan.DerivedSource(); !derived {
		if _, err := validateReadSourceFields(plan.SourceFields()); err != nil {
			return "", nil, err
		}
	}
	return queryplan.CompileAnnotated(plan, input, inputArguments, queryplan.AnnotationDialect{
		BackendName: "PostgreSQL", ParameterLimit: postgresBulkParameters,
		QuoteTable:      func(table string) (string, error) { return quoteTable(schema, table) },
		QuoteIdentifier: quoteIdentifier, Qualify: quoteQualified,
		Value: func(value query.ResultExpression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
			var statement strings.Builder
			if err := appendSourceResultValue(&statement, value, source, arguments, false); err != nil {
				return "", err
			}
			return statement.String(), nil
		},
		Aggregate: func(value query.ResultExpression, operand, filter string) (string, int, error) {
			statement, err := renderAggregate(value, operand, filter, true)
			return statement, 1, err
		},
		CompleteAggregates: finishPostgresAggregates,
		Predicate:          compileScalarPredicate,
		Pagination: func(statement *strings.Builder, arguments *[]any, plan query.Plan) error {
			appendPagination(statement, arguments, plan)
			return nil
		},
		RowLock: appendRowLock,
		GroupLiteral: func(value query.Value, arguments *[]any) (string, error) {
			argument, err := postgresValue(value)
			if err != nil {
				return "", err
			}
			*arguments = append(*arguments, argument)
			return placeholder(len(*arguments)), nil
		},
		GroupComparison: postgresGroupComparisonLiteral,
	})
}
