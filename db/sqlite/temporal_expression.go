package sqlite

import (
	"database/sql/driver"
	"errors"
	"math"
	"math/big"
	"time"

	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

const invalidTemporalExpression = "godj: temporal expression requires canonical operands and a result in the supported domain"

func temporalExpressionError(cause error) error {
	return errors.Join(errors.New(invalidTemporalExpression), cause)
}

// SQL functions receive SQLite's physical value, before database/sql converts
// a DATETIME column to time.Time. Accept only our canonical UTC text encoding.
func sqliteInstant(raw driver.Value) (time.Time, error) {
	text, ok := raw.(string)
	if !ok || len(text) != len(temporal.SQLiteLayout) {
		return time.Time{}, temporalExpressionError(nil)
	}
	value, err := time.Parse(temporal.SQLiteLayout, text)
	if err != nil || value.Year() < 1 || value.Year() > 9999 || value.Format(temporal.SQLiteLayout) != text {
		return time.Time{}, temporalExpressionError(err)
	}
	return value, nil
}

func sqliteDateTimeExpression(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, temporalExpressionError(nil)
	}
	if arguments[0] == nil {
		return nil, nil
	}
	_, err := sqliteInstant(arguments[0])
	if err != nil {
		return nil, err
	}
	return arguments[0], nil
}

// Only AVG-derived expressions can carry native fractional microseconds.
// These bounds are the full normalized model domain, not SQLite's narrower
// int64 storage domain. Every allocation is local to this stateless call.
func sqliteDurationNative(raw driver.Value, mean bool) (driver.Value, error) {
	switch value := raw.(type) {
	case nil, int64:
		return value, nil
	case float64:
		if !mean || math.IsInf(value, 0) || math.IsNaN(value) {
			break
		}
		number := new(big.Rat).SetFloat64(value)
		lower := new(big.Int).Mul(big.NewInt(int64(duration.MinDays)), big.NewInt(duration.MicrosecondsPerDay))
		upper := new(big.Int).Mul(big.NewInt(int64(duration.MaxDays)+1), big.NewInt(duration.MicrosecondsPerDay))
		upper.Sub(upper, big.NewInt(1))
		if number.Cmp(new(big.Rat).SetInt(lower)) >= 0 && number.Cmp(new(big.Rat).SetInt(upper)) <= 0 {
			return value, nil
		}
	}
	return nil, temporalExpressionError(nil)
}

func sqliteDurationMean(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, temporalExpressionError(nil)
	}
	return sqliteDurationNative(arguments[0], true)
}

func sqliteTemporalKind(expression query.ScalarExpression) string {
	if expression.ResultKind() == query.FieldDuration && expression.RequiresDurationRounding() {
		return "duration_mean"
	}
	return string(expression.ResultKind())
}

func sqliteTemporalOperand(kind string, raw driver.Value) (driver.Value, error) {
	switch kind {
	case "datetime":
		if raw == nil {
			return nil, nil
		}
		value, err := sqliteInstant(raw)
		if err != nil {
			return nil, err
		}
		return value.UnixMicro(), nil
	case "duration", "duration_mean":
		return sqliteDurationNative(raw, kind == "duration_mean")
	default:
		return nil, temporalExpressionError(nil)
	}
}

func nativeMicrosecondsRatio(raw driver.Value) *big.Rat {
	if value, ok := raw.(int64); ok {
		return new(big.Rat).SetInt64(value)
	}
	return new(big.Rat).SetFloat64(raw.(float64)) // Validated by sqliteTemporalOperand.
}

// Round the exact sum, not the Duration operand or a floating-point epoch.
// In particular, years far from 1970 must retain odd microseconds beyond 2^53.
func roundTemporalMicroseconds(value *big.Rat) *big.Int {
	var whole, remainder big.Int
	whole.QuoRem(value.Num(), value.Denom(), &remainder)
	remainder.Abs(&remainder)
	comparison := remainder.Lsh(&remainder, 1).Cmp(value.Denom())
	if comparison > 0 || comparison == 0 && whole.Bit(0) != 0 {
		whole.Add(&whole, big.NewInt(int64(value.Sign())))
	}
	return &whole
}

func sqliteTemporalBinary(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 5 {
		return nil, temporalExpressionError(nil)
	}
	operator, operatorOK := arguments[0].(string)
	leftKind, leftOK := arguments[1].(string)
	rightKind, rightOK := arguments[2].(string)
	if !operatorOK || !leftOK || !rightOK || operator != "+" && operator != "-" {
		return nil, temporalExpressionError(nil)
	}
	isDuration := func(kind string) bool { return kind == "duration" || kind == "duration_mean" }
	durations := isDuration(leftKind) && isDuration(rightKind)
	difference := leftKind == "datetime" && rightKind == "datetime" && operator == "-"
	shift := leftKind == "datetime" && isDuration(rightKind) || isDuration(leftKind) && rightKind == "datetime" && operator == "+"
	if !durations && !difference && !shift {
		return nil, temporalExpressionError(nil)
	}
	// Validate both physical operands before NULL propagation. Otherwise a
	// missing value could hide corrupt data or an invalid inner expression.
	left, err := sqliteTemporalOperand(leftKind, arguments[3])
	if err != nil {
		return nil, err
	}
	right, err := sqliteTemporalOperand(rightKind, arguments[4])
	if err != nil {
		return nil, err
	}
	if left == nil || right == nil {
		return nil, nil
	}
	if durations {
		leftInteger, leftExact := left.(int64)
		rightInteger, rightExact := right.(int64)
		if leftExact && rightExact {
			result := new(big.Int).SetInt64(leftInteger)
			if operator == "+" {
				result.Add(result, big.NewInt(rightInteger))
			} else {
				result.Sub(result, big.NewInt(rightInteger))
			}
			if !result.IsInt64() {
				return nil, temporalExpressionError(duration.ErrMicrosecondRange)
			}
			return result.Int64(), nil
		}
		asFloat := func(value driver.Value) float64 {
			if integer, ok := value.(int64); ok {
				return float64(integer)
			}
			return value.(float64)
		}
		result := asFloat(left) + asFloat(right)
		if operator == "-" {
			result = asFloat(left) - asFloat(right)
		}
		return sqliteDurationNative(result, true)
	}
	if difference {
		// The entire year 1..9999 difference fits int64 microseconds, unlike
		// time.Time.Sub's roughly 290-year nanosecond range.
		return left.(int64) - right.(int64), nil
	}
	result := nativeMicrosecondsRatio(left)
	if operator == "+" {
		result.Add(result, nativeMicrosecondsRatio(right))
	} else {
		result.Sub(result, nativeMicrosecondsRatio(right))
	}
	whole := roundTemporalMicroseconds(result)
	if !whole.IsInt64() {
		return nil, temporalExpressionError(temporal.ErrInvalid)
	}
	instant, err := temporal.Canonical(time.UnixMicro(whole.Int64()))
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	return instant.Format(temporal.SQLiteLayout), nil
}

func sqliteTemporalNegate(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 2 {
		return nil, temporalExpressionError(nil)
	}
	kind, ok := arguments[0].(string)
	if !ok || kind != "duration" && kind != "duration_mean" {
		return nil, temporalExpressionError(nil)
	}
	value, err := sqliteTemporalOperand(kind, arguments[1])
	if err != nil || value == nil {
		return nil, err
	}
	if integer, ok := value.(int64); ok {
		if integer == math.MinInt64 {
			return nil, temporalExpressionError(duration.ErrMicrosecondRange)
		}
		return -integer, nil
	}
	return sqliteDurationNative(-value.(float64), true)
}
