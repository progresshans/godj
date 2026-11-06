package sqlite

import (
	"database/sql/driver"
	"errors"
	"math"
	"math/big"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationcalc"
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
	if expression.RequiresDurationFraction() {
		return "duration_fraction"
	}
	if expression.ResultKind() == query.FieldDuration && expression.RequiresDurationRounding() {
		return "duration_mean"
	}
	return string(expression.ResultKind())
}

func sqliteTemporalOperand(kind string, raw driver.Value) (driver.Value, error) {
	switch kind {
	case "date":
		if raw == nil {
			return nil, nil
		}
		text, ok := raw.(string)
		if !ok {
			return nil, temporalExpressionError(nil)
		}
		value, err := calendar.Parse(text)
		if err != nil {
			return nil, temporalExpressionError(err)
		}
		return time.Date(value.Year, value.Month, value.Day, 0, 0, 0, 0, time.UTC).UnixMicro(), nil
	case "time":
		if raw == nil {
			return nil, nil
		}
		text, ok := raw.(string)
		if !ok {
			return nil, temporalExpressionError(nil)
		}
		value, err := clock.Parse(text)
		if err != nil {
			return nil, temporalExpressionError(err)
		}
		return int64(value.Hour*3600+value.Minute*60+value.Second)*1000000 + int64(value.Microsecond), nil
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
	case "duration_fraction":
		if raw == nil {
			return nil, nil
		}
		key, ok := raw.([]byte)
		if !ok {
			return nil, temporalExpressionError(nil)
		}
		if _, err := durationcalc.FromKey(key); err != nil {
			return nil, temporalExpressionError(err)
		}
		return key, nil
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

func sqliteTemporalShiftResult(kind string, whole *big.Int) (driver.Value, error) {
	if kind == "time" {
		// Full model periods may exceed int64 microseconds. Reduce only the
		// checked, rounded clock result; never narrow its Duration operand.
		microseconds := new(big.Int).Mod(whole, big.NewInt(duration.MicrosecondsPerDay)).Int64()
		value := clock.Time{
			Hour: int(microseconds / 3600000000), Minute: int(microseconds / 60000000 % 60),
			Second: int(microseconds / 1000000 % 60), Microsecond: int(microseconds % 1000000),
		}
		return value.String(), nil
	}
	if kind != "date" && kind != "datetime" || !whole.IsInt64() {
		return nil, temporalExpressionError(temporal.ErrInvalid)
	}
	value, err := temporal.Canonical(time.UnixMicro(whole.Int64()))
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	return value.Format(temporal.SQLiteLayout), nil
}

func sqliteTemporalBinary(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 5 {
		return nil, temporalExpressionError(nil)
	}
	operator, operatorOK := arguments[0].(string)
	leftKind, leftOK := arguments[1].(string)
	rightKind, rightOK := arguments[2].(string)
	if !operatorOK || !leftOK || !rightOK || operator != "+" && operator != "-" && operator != "/" && operator != "*" {
		return nil, temporalExpressionError(nil)
	}
	isDuration := func(kind string) bool {
		return kind == "duration" || kind == "duration_mean" || kind == "duration_fraction"
	}
	isNumber := func(kind string) bool { return kind == "integer" || kind == "float" || kind == "decimal" }
	isCalendar := func(kind string) bool { return kind == "date" || kind == "datetime" }
	isTemporal := func(kind string) bool { return isCalendar(kind) || kind == "time" }
	if isDuration(leftKind) && isNumber(rightKind) && (operator == "*" || operator == "/") || isNumber(leftKind) && isDuration(rightKind) && operator == "*" {
		return sqliteDurationScale(operator, leftKind, rightKind, arguments[3], arguments[4])
	}
	durations := isDuration(leftKind) && isDuration(rightKind) && (operator == "+" || operator == "-")
	ratio := isDuration(leftKind) && isDuration(rightKind) && operator == "/"
	difference := (isCalendar(leftKind) && isCalendar(rightKind) || leftKind == "time" && rightKind == "time") && operator == "-"
	shift := isTemporal(leftKind) && isDuration(rightKind) && (operator == "+" || operator == "-") ||
		isDuration(leftKind) && isTemporal(rightKind) && operator == "+"
	if !durations && !ratio && !difference && !shift {
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
	shiftKind := leftKind
	if isDuration(leftKind) {
		shiftKind = rightKind
	}
	if leftKind == "duration_fraction" || rightKind == "duration_fraction" {
		if shift {
			instant, period := left, right
			if isTemporal(rightKind) {
				instant, period = right, left
			}
			value, err := decodeDurationFraction(period)
			if err != nil {
				return nil, temporalExpressionError(err)
			}
			if operator == "-" {
				return sqliteTemporalShiftResult(shiftKind, value.RoundSubtractFrom(instant.(int64)))
			}
			return sqliteTemporalShiftResult(shiftKind, value.RoundOffset(instant.(int64)))
		}
		first, err := decodeDurationFraction(left)
		if err != nil {
			return nil, temporalExpressionError(err)
		}
		second, err := decodeDurationFraction(right)
		if err != nil {
			return nil, temporalExpressionError(err)
		}
		if ratio {
			value, err := first.Ratio(second)
			if err != nil {
				return nil, temporalExpressionError(err)
			}
			return value, nil
		}
		var value durationcalc.Value
		if operator == "+" {
			value, err = first.Add(second)
		} else {
			value, err = first.Subtract(second)
		}
		if err != nil {
			return nil, temporalExpressionError(err)
		}
		return value.Key(), nil
	}
	if ratio {
		denominator := nativeMicrosecondsRatio(right)
		if denominator.Sign() == 0 {
			return nil, temporalExpressionError(errors.New("duration division by zero"))
		}
		// Convert once at the Float result boundary. Converting each period
		// first would discard odd microseconds above 2^53 before division.
		value := nativeMicrosecondsRatio(left)
		value.Quo(value, denominator)
		result, _ := value.Float64()
		if math.IsInf(result, 0) {
			return nil, temporalExpressionError(errors.New("duration ratio exceeds finite Float"))
		}
		return result, nil
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
	return sqliteTemporalShiftResult(shiftKind, roundTemporalMicroseconds(result))
}

func sqliteTemporalNegate(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 2 {
		return nil, temporalExpressionError(nil)
	}
	kind, ok := arguments[0].(string)
	if !ok || kind != "duration" && kind != "duration_mean" && kind != "duration_fraction" {
		return nil, temporalExpressionError(nil)
	}
	value, err := sqliteTemporalOperand(kind, arguments[1])
	if err != nil || value == nil {
		return nil, err
	}
	if kind == "duration_fraction" {
		period, err := decodeDurationFraction(value)
		if err == nil {
			period, err = period.Negate()
		}
		if err != nil {
			return nil, temporalExpressionError(err)
		}
		return period.Key(), nil
	}
	if integer, ok := value.(int64); ok {
		if integer == math.MinInt64 {
			return nil, temporalExpressionError(duration.ErrMicrosecondRange)
		}
		return -integer, nil
	}
	return sqliteDurationNative(-value.(float64), true)
}
