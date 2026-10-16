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
	modernsqlite.RegisterDeterministicScalarFunction("godj_decimal_result", 1, sqliteDecimalResult),
	modernsqlite.RegisterDeterministicScalarFunction("godj_decimal_binary", 3, sqliteDecimalBinary),
	modernsqlite.RegisterDeterministicScalarFunction("godj_decimal_negate", 1, sqliteDecimalNegate),
	modernsqlite.RegisterDeterministicScalarFunction("godj_duration_mean", 1, sqliteDurationMean),
	modernsqlite.RegisterDeterministicScalarFunction("godj_datetime_value", 1, sqliteDateTimeExpression),
	modernsqlite.RegisterDeterministicScalarFunction("godj_temporal_binary", 5, sqliteTemporalBinary),
	modernsqlite.RegisterDeterministicScalarFunction("godj_temporal_negate", 2, sqliteTemporalNegate),
	modernsqlite.RegisterDeterministicScalarFunction("godj_calendar_function", 3, sqliteCalendarFunction),
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
	switch expression.ResultKind() {
	case query.FieldDateTime:
		return queryplan.ScalarSQL("godj_datetime_value(", statement, ")")
	case query.FieldDuration:
		if expression.RequiresDurationRounding() {
			return queryplan.ScalarSQL("godj_duration_mean(", statement, ")")
		}
		return queryplan.ScalarSQL("godj_int64(", statement, ")")
	}
	if expression.ResultKind() == query.FieldInteger && expression.Kind() != query.ScalarLiteral {
		return "godj_int64(" + statement + ")", nil
	}
	return statement, nil
}

func compileReadScalar(expression query.ScalarExpression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	if err := source.Validate(expression); err != nil {
		return "", err
	}
	return queryplan.CompileScalar(expression, expression.ResultKind(), arguments, queryplan.ScalarDialect{
		ParameterLimit: sqliteBulkParameters, Value: sqliteValue, Placeholder: func(int) string { return "?" },
		Binary: compileSQLiteBinary, Negate: compileSQLiteNegate, Function: compileSQLiteFunction, Expression: wrapReadScalar,
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
		return queryplan.ScalarSQL("godj_decimal_value(", column, ", ", strconv.Itoa(digits), ", ", strconv.Itoa(places), ")")
	}
	switch field.Kind() {
	case query.FieldDateTime:
		return queryplan.ScalarSQL("godj_datetime_value(", column, ")")
	case query.FieldDuration:
		return queryplan.ScalarSQL("godj_int64(", column, ")")
	}
	return column, nil
}

func wrapReadScalar(expression query.ScalarExpression, statement string) (string, error) {
	switch expression.ResultKind() {
	case query.FieldDecimal:
		if reference, selected := expression.ResultReference(); selected {
			producer, _ := reference.Expression()
			if producer.Kind() == query.ResultField {
				field, _ := producer.Field()
				return readScalarField(field, statement)
			}
			return queryplan.ScalarSQL("godj_decimal_result(", statement, ")")
		}
		return statement, nil
	case query.FieldFloat:
		return "godj_float_operand(" + statement + ")", nil
	default:
		return wrapSQLiteScalar(expression, statement)
	}
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
