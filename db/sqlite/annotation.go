package sqlite

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileAnnotated(plan query.Plan, input string, inputArguments []any) (string, []any, error) {
	if _, _, derived := plan.DerivedSource(); !derived {
		if err := validateReadSourceFields(plan.SourceFields()); err != nil {
			return "", nil, err
		}
	}
	return queryplan.CompileAnnotated(plan, input, inputArguments, queryplan.AnnotationDialect{
		BackendName: "SQLite", ParameterLimit: sqliteBulkParameters, JoinLimit: 63,
		QuoteTable:      func(table string) (string, error) { return quoteQualified("main", table) },
		QuoteIdentifier: quoteIdentifier, Qualify: quoteQualified,
		Value: func(value query.ResultExpression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
			var statement strings.Builder
			if err := appendSourceResultValue(&statement, value, source, arguments, false); err != nil {
				return "", err
			}
			return statement.String(), nil
		},
		Aggregate: func(value query.ResultExpression, operand, filter string) (string, int, error) {
			statement, err := renderAggregate(value, operand, filter)
			copies := 1
			if value.OperandKind() == query.FieldFloat && (value.Kind() == query.ResultSum || value.Kind() == query.ResultAvg) {
				copies = 2
			}
			return statement, copies, err
		},
		Predicate: compileScalarPredicate,
		OrderValue: func(kind query.FieldKind, value string) string {
			if kind == query.FieldJSON {
				return "godj_json_sort_key(" + value + ")"
			}
			return value
		},
		Pagination: func(statement *strings.Builder, arguments *[]any, plan query.Plan) error {
			*arguments = appendPagination(statement, *arguments, plan)
			return nil
		},
		GroupLiteral: func(value query.Value, arguments *[]any) (string, error) {
			argument, err := sqliteValue(value)
			if err != nil {
				return "", err
			}
			*arguments = append(*arguments, argument)
			return "?", nil
		},
		GroupComparison: sqliteGroupComparisonLiteral,
	})
}
