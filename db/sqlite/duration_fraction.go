package sqlite

import (
	"database/sql/driver"
	"errors"

	"github.com/progresshans/godj/internal/durationcalc"
	modernsqlite "modernc.org/sqlite"
)

// Fraction keys only occur in computed query values. Field readers still use
// godj_int64; accepting this carrier cannot legitimise a corrupt model BLOB.
func decodeDurationFraction(raw driver.Value) (durationcalc.Value, error) {
	switch value := raw.(type) {
	case int64:
		return durationcalc.FromMicroseconds(value), nil
	case float64:
		return durationcalc.FromNativeMean(value)
	case []byte:
		return durationcalc.FromKey(value)
	default:
		return durationcalc.Value{}, durationcalc.ErrInvalid
	}
}

func sqliteDurationFraction(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, temporalExpressionError(nil)
	}
	if arguments[0] == nil {
		return nil, nil
	}
	value, err := decodeDurationFraction(arguments[0])
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	return value.Key(), nil
}

func sqliteDurationStore(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 1 {
		return nil, temporalExpressionError(nil)
	}
	if arguments[0] == nil {
		return nil, nil
	}
	value, err := decodeDurationFraction(arguments[0])
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	period, err := value.Duration()
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	microseconds, err := period.TotalMicroseconds()
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	return microseconds, nil
}

func sqliteDurationFactor(kind string, raw driver.Value) (durationcalc.Factor, error) {
	if raw == nil {
		return durationcalc.Factor{}, nil
	}
	switch kind {
	case "integer":
		if value, ok := raw.(int64); ok {
			return durationcalc.IntegerFactor(value), nil
		}
	case "float":
		switch value := raw.(type) {
		case float64:
			return durationcalc.FloatFactor(value)
		case int64:
			// A declared Float field may use INTEGER storage. Its model value
			// is still binary64, including the corresponding input rounding.
			return durationcalc.FloatFactor(float64(value))
		}
	case "decimal":
		value, err := decodeDecimalExpression(raw)
		if err != nil {
			return durationcalc.Factor{}, err
		}
		return durationcalc.DecimalFactor(value)
	}
	return durationcalc.Factor{}, durationcalc.ErrInvalid
}

func sqliteDurationScale(operator, leftKind, rightKind string, left, right driver.Value) (driver.Value, error) {
	periodKind, factorKind, rawPeriod, rawFactor := leftKind, rightKind, left, right
	if rightKind == "duration" || rightKind == "duration_mean" || rightKind == "duration_fraction" {
		if operator != "*" {
			return nil, temporalExpressionError(nil)
		}
		periodKind, factorKind, rawPeriod, rawFactor = rightKind, leftKind, right, left
	}
	period, err := sqliteTemporalOperand(periodKind, rawPeriod)
	if err != nil {
		return nil, err
	}
	factor, err := sqliteDurationFactor(factorKind, rawFactor)
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	if period == nil || rawFactor == nil {
		return nil, nil // Both physical domains were consumed before NULL.
	}
	value, err := decodeDurationFraction(period)
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	var result durationcalc.Value
	if operator == "*" {
		result, err = value.Scale(factor)
	} else {
		result, err = value.Divide(factor)
	}
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	return result.Key(), nil
}

type durationFractionAggregate struct {
	value   durationcalc.Aggregate
	average bool
	err     error
}

func (aggregate *durationFractionAggregate) Step(_ *modernsqlite.FunctionContext, arguments []driver.Value) error {
	if aggregate.err != nil {
		return aggregate.err
	}
	if len(arguments) != 1 {
		aggregate.err = temporalExpressionError(nil)
		return aggregate.err
	}
	if arguments[0] == nil {
		return nil
	}
	value, err := decodeDurationFraction(arguments[0])
	if err == nil {
		err = aggregate.value.Add(value)
	}
	if err != nil {
		aggregate.err = temporalExpressionError(err)
	}
	return aggregate.err
}

func (aggregate *durationFractionAggregate) WindowValue(_ *modernsqlite.FunctionContext) (driver.Value, error) {
	if aggregate.err != nil {
		return nil, aggregate.err
	}
	value, present, err := aggregate.value.Result(aggregate.average)
	if err != nil {
		return nil, temporalExpressionError(err)
	}
	if !present {
		return nil, nil
	}
	return value.Key(), nil
}

func (aggregate *durationFractionAggregate) WindowInverse(_ *modernsqlite.FunctionContext, _ []driver.Value) error {
	aggregate.err = temporalExpressionError(errors.New("duration window aggregates are not supported"))
	return aggregate.err
}

func (aggregate *durationFractionAggregate) Final(_ *modernsqlite.FunctionContext) {
	*aggregate = durationFractionAggregate{err: temporalExpressionError(errors.New("duration aggregate has been finalized"))}
}
