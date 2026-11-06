package sqlite

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileResultStage(plan, input query.Plan) (string, []any, error) {
	if err := plan.ValidateDerived(); err != nil {
		return "", nil, err
	}
	statement, arguments, err := compileReadPlan(input)
	if err != nil {
		return "", nil, err
	}
	if input.EmptyResult() {
		statement, arguments, err = queryplan.CompileEmptyInput(input.ResultShape(), queryplan.ScalarDialect{
			ParameterLimit: sqliteBulkParameters, Value: sqliteValue, Placeholder: func(int) string { return "?" },
			Binary: compileSQLiteBinary, Negate: compileSQLiteNegate, Function: compileSQLiteFunction, Expression: wrapReadScalar,
		})
		if err != nil {
			return "", nil, err
		}
	}
	if plan.HasAnnotations() {
		return compileAnnotated(plan, statement, arguments)
	}
	return queryplan.CompileDerived(plan, statement, arguments, queryplan.DerivedDialect{
		ComparisonValue: sqliteComparisonValue, GroupComparisonLiteral: sqliteGroupComparisonLiteral,
		Scalar: queryplan.ScalarDialect{
			ParameterLimit: sqliteBulkParameters, Value: sqliteValue,
			Placeholder: func(int) string { return "?" }, Binary: compileSQLiteBinary, Negate: compileSQLiteNegate, Function: compileSQLiteFunction, Expression: wrapReadScalar,
		},
		Aggregate: func(value query.ResultExpression, operand, filter string) (string, int, error) {
			statement, err := renderAggregate(value, operand, filter)
			copies := 1
			if value.OperandKind() == query.FieldFloat && (value.Kind() == query.ResultSum || value.Kind() == query.ResultAvg) {
				copies = 2
			}
			return statement, copies, err
		},
		OrderValue: func(kind query.FieldKind, statement string) string {
			if kind == query.FieldJSON {
				return "godj_json_sort_key(" + statement + ")"
			}
			return statement
		},
		Pagination: func(statement *strings.Builder, arguments *[]any, plan query.Plan) error {
			*arguments = appendPagination(statement, *arguments, plan)
			return nil
		},
	})
}

func compileResultPage(plan, input query.Plan) (string, []any, error) {
	if err := plan.ValidatePage(); err != nil {
		return "", nil, err
	}
	statement, arguments, err := compileReadPlan(input)
	if err != nil {
		return "", nil, err
	}
	if input.EmptyResult() {
		statement, arguments, err = queryplan.CompileEmptyInput(input.ResultShape(), queryplan.ScalarDialect{
			ParameterLimit: sqliteBulkParameters, Value: sqliteValue, Placeholder: func(int) string { return "?" },
			Binary: compileSQLiteBinary, Negate: compileSQLiteNegate, Function: compileSQLiteFunction, Expression: wrapReadScalar,
		})
		if err != nil {
			return "", nil, err
		}
	}
	return queryplan.CompilePage(plan, statement, arguments, queryplan.PageDialect{
		ParameterLimit: sqliteBulkParameters,
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
	})
}
