package sqlite

import (
	"database/sql/driver"
	"errors"

	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

const invalidIntegerExpression = "godj: integer expression did not produce an int64"

// SQLite promotes overflowing integer arithmetic to REAL. Validate each
// native integer operand/result inside the statement, before affinity could
// round it back to INTEGER. An error aborts the entire UPDATE. This stateless
// function uses no application-side row reads or mutable global query state.
var scalarExpressionRegistrationError = modernsqlite.RegisterDeterministicScalarFunction("godj_int64", 1, sqliteIntegerExpression)

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
