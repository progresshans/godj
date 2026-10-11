package sqlite

import (
	"database/sql/driver"
	"errors"
	"math"

	modernsqlite "modernc.org/sqlite"
)

var numericAggregateRegistrationError = registerNumericAggregates()

func registerNumericAggregates() error {
	return errors.Join(
		modernsqlite.RegisterDeterministicScalarFunction("godj_decimal_aggregate_input", 3, sqliteDecimalAggregateInput),
		modernsqlite.RegisterFunction("godj_decimal_sum", &modernsqlite.FunctionImpl{NArgs: 1, Deterministic: true,
			MakeAggregate: func(modernsqlite.FunctionContext) (modernsqlite.AggregateFunction, error) {
				return &decimalAggregate{}, nil
			}}),
		modernsqlite.RegisterFunction("godj_decimal_avg", &modernsqlite.FunctionImpl{NArgs: 1, Deterministic: true,
			MakeAggregate: func(modernsqlite.FunctionContext) (modernsqlite.AggregateFunction, error) {
				return &decimalAggregate{average: true}, nil
			}}),
		modernsqlite.RegisterDeterministicScalarFunction("godj_float_operand", 1, sqliteFloatOperand),
		modernsqlite.RegisterDeterministicScalarFunction("godj_float_aggregate", 2, sqliteFloatAggregate),
	)
}

var errFloatAggregate = errors.New("godj: float aggregate requires native numeric values and a non-NaN result")

func sqliteFloatOperand(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, errFloatAggregate
	}
	switch value := arguments[0].(type) {
	case nil:
		return nil, nil
	case int64:
		return float64(value), nil
	case float64:
		if !math.IsNaN(value) {
			return value, nil
		}
	}
	return nil, errFloatAggregate
}

func sqliteFloatAggregate(ctx *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 2 {
		return nil, errFloatAggregate
	}
	count, ok := arguments[1].(int64)
	if !ok || count < 0 || count == 0 && arguments[0] != nil || count > 0 && arguments[0] == nil {
		return nil, errFloatAggregate
	}
	return sqliteFloatOperand(ctx, arguments[:1])
}
