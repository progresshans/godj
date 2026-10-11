package sqlite

import (
	"database/sql/driver"
	"errors"
	"strconv"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

const invalidIntegerExpression = "godj: integer expression did not produce an int64"

// SQLite promotes overflowing integer arithmetic to REAL. Validate each
// native integer operand/result inside the statement, before affinity could
// round it back to INTEGER. An error aborts the entire UPDATE. This stateless
// function uses no application-side row reads or mutable global query state.
var scalarExpressionRegistrationError = errors.Join(
	modernsqlite.RegisterDeterministicScalarFunction("godj_int64", 1, sqliteIntegerExpression),
	modernsqlite.RegisterDeterministicScalarFunction("godj_decimal_value", 3, sqliteDecimalExpression),
)

func sqliteIntegerExpression(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, errors.New(invalidIntegerExpression)
	}
	if arguments[0] == nil {
		return nil, nil
	}
	if value, ok := arguments[0].(int64); ok {
		return value, nil
	}
	return nil, errors.New(invalidIntegerExpression)
}

func wrapSQLiteScalar(expression query.ScalarExpression, statement string) (string, error) {
	if expression.ResultKind() == query.FieldInteger && expression.Kind() != query.ScalarLiteral {
		return "godj_int64(" + statement + ")", nil
	}
	return statement, nil
}

func compileReadScalar(expression query.ScalarExpression, alias string, arguments *[]any) (string, error) {
	return compileReadScalarFields(expression, func(field query.FieldRef) (string, error) {
		if alias == "" {
			return quoteIdentifier(field.Column())
		}
		return quoteQualified(alias, field.Column())
	}, arguments)
}

func compileReadScalarFields(expression query.ScalarExpression, resolve func(query.FieldRef) (string, error), arguments *[]any) (string, error) {
	return queryplan.CompileScalar(expression, expression.ResultKind(), arguments, queryplan.ScalarDialect{
		ParameterLimit: sqliteBulkParameters, Value: sqliteValue, Placeholder: func(int) string { return "?" },
		Predicate: func(value query.Expression, arguments *[]any) (string, error) {
			return compileScalarPredicate(value, resolve, arguments)
		},
		Field: func(field query.FieldRef) (string, error) {
			column, err := resolve(field)
			if err != nil {
				return "", err
			}
			if digits, places, decimal := field.DecimalPrecision(); decimal {
				column = "godj_decimal_value(" + column + ", " + strconv.Itoa(digits) + ", " + strconv.Itoa(places) + ")"
			}
			return column, nil
		},
		Expression: func(expression query.ScalarExpression, statement string) (string, error) {
			switch expression.ResultKind() {
			case query.FieldFloat:
				return "godj_float_operand(" + statement + ")", nil
			case query.FieldDuration:
				return "godj_int64(" + statement + ")", nil
			default:
				return wrapSQLiteScalar(expression, statement)
			}
		},
	})
}

func sqliteDecimalExpression(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 3 {
		return nil, errDecimalAggregateInput
	}
	digits, digitsOK := arguments[1].(int64)
	places, placesOK := arguments[2].(int64)
	if !digitsOK || !placesOK || digits < 1 || digits > decimal.MaxDigits || places < 0 || places > digits {
		return nil, errDecimalAggregateInput
	}
	if arguments[0] == nil {
		return nil, nil
	}
	key, ok := arguments[0].([]byte)
	if !ok {
		return nil, errDecimalAggregateInput
	}
	value, err := decimalstorage.Decode(key)
	if err != nil || !value.Fits(int(digits), int(places)) {
		return nil, errDecimalAggregateInput
	}
	return decimalstorage.Encode(value)
}
