package postgres

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileResultStage(schema string, plan, input query.Plan) (string, []any, error) {
	if err := plan.ValidateDerived(); err != nil {
		return "", nil, err
	}
	statement, arguments, err := compileReadPlan(schema, input)
	if err != nil {
		return "", nil, err
	}
	if input.EmptyResult() {
		statement, arguments, err = queryplan.CompileEmptyInput(input.ResultShape(), queryplan.ScalarDialect{
			ParameterLimit: postgresBulkParameters, Value: postgresValue, Placeholder: placeholder, Literal: readScalarLiteral, Binary: compilePostgresBinary, Expression: wrapReadScalar,
		})
		if err != nil {
			return "", nil, err
		}
	}
	return queryplan.CompileDerived(plan, statement, arguments, queryplan.DerivedDialect{
		Scalar: queryplan.ScalarDialect{
			ParameterLimit: postgresBulkParameters, Value: postgresValue,
			Placeholder: placeholder, Literal: readScalarLiteral, Binary: compilePostgresBinary, Expression: wrapReadScalar,
		},
		Aggregate: func(value query.ResultExpression, operand, filter string) (string, int, error) {
			statement, err := renderAggregate(value, operand, filter, true)
			return statement, 1, err
		},
		Pagination: func(statement *strings.Builder, arguments *[]any, plan query.Plan) error {
			appendPagination(statement, arguments, plan)
			return nil
		},
	})
}

func compileResultPage(schema string, plan, input query.Plan) (string, []any, error) {
	if err := plan.ValidatePage(); err != nil {
		return "", nil, err
	}
	statement, arguments, err := compileReadPlan(schema, input)
	if err != nil {
		return "", nil, err
	}
	if input.EmptyResult() {
		statement, arguments, err = queryplan.CompileEmptyInput(input.ResultShape(), queryplan.ScalarDialect{
			ParameterLimit: postgresBulkParameters, Value: postgresValue, Placeholder: placeholder, Literal: readScalarLiteral, Binary: compilePostgresBinary, Expression: wrapReadScalar,
		})
		if err != nil {
			return "", nil, err
		}
	}
	return queryplan.CompilePage(plan, statement, arguments, queryplan.PageDialect{
		ParameterLimit: postgresBulkParameters,
		Pagination: func(statement *strings.Builder, arguments *[]any, plan query.Plan) error {
			appendPagination(statement, arguments, plan)
			return nil
		},
	})
}
