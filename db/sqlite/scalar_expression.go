package sqlite

import (
	"database/sql/driver"
	"errors"
	"math"
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
	modernsqlite.RegisterDeterministicScalarFunction("godj_duration_mean", 1, sqliteDurationMean),
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

func compileReadScalar(expression query.ScalarExpression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	if err := source.Plan.ValidateScalar(expression); err != nil {
		return "", err
	}
	return queryplan.CompileScalar(expression, expression.ResultKind(), arguments, queryplan.ScalarDialect{
		ParameterLimit: sqliteBulkParameters, Value: sqliteValue, Placeholder: func(int) string { return "?" },
		Expression: wrapReadScalar,
		Predicate: func(value query.Expression, arguments *[]any) (string, error) {
			return compileScalarPredicate(value, source, arguments)
		},
		Field: func(field query.FieldRef) (string, error) {
			column, err := source.ResolveField(field, quoteIdentifier, quoteQualified)
			if err != nil {
				return "", err
			}
			return readScalarField(field, column)
		},
		Related: func(path query.RelationPath) (string, error) {
			column, err := source.ResolveRelated(path, quoteQualified)
			if err != nil {
				return "", err
			}
			return readScalarField(path.Terminal(), column)
		},
	})
}

func readScalarField(field query.FieldRef, column string) (string, error) {
	if digits, places, decimal := field.DecimalPrecision(); decimal {
		column = "godj_decimal_value(" + column + ", " + strconv.Itoa(digits) + ", " + strconv.Itoa(places) + ")"
	}
	return column, nil
}

func wrapReadScalar(expression query.ScalarExpression, statement string) (string, error) {
	switch expression.ResultKind() {
	case query.FieldFloat:
		return "godj_float_operand(" + statement + ")", nil
	case query.FieldDuration:
		if expression.RequiresDurationRounding() {
			return "godj_duration_mean(" + statement + ")", nil
		}
		return "godj_int64(" + statement + ")", nil
	default:
		return wrapSQLiteScalar(expression, statement)
	}
}

func sqliteDurationMean(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) == 1 {
		switch value := arguments[0].(type) {
		case nil, int64:
			return value, nil
		case float64:
			if !math.IsInf(value, 0) && !math.IsNaN(value) {
				return value, nil
			}
		}
	}
	return nil, errors.New("godj: duration average result requires finite native microseconds")
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
