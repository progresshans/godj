package orm

import (
	"time"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

// DateTimeExpression represents a UTC instant. Adding a model Duration keeps
// an instant; subtracting another instant produces a DurationExpression.
type DateTimeExpression[M any] struct{ ScalarExpression[M, time.Time] }

// DurationExpression keeps normalized model periods separate from numeric
// scalars and Go's nanosecond time.Duration. Its SUM and AVG are Duration.
type DurationExpression[M any] struct {
	ScalarExpression[M, duration.Duration]
}

// DurationFactor retains the factor's model domain rather than converting an
// integer or exact Decimal into binary64 before the backend sees it.
type DurationFactor interface {
	int64 | float64 | decimal.Decimal
}

func DateTime[M any](operand ScalarOperand[M, time.Time]) DateTimeExpression[M] {
	return DateTimeExpression[M]{Scalar(operand)}
}

func Duration[M any](operand ScalarOperand[M, duration.Duration]) DurationExpression[M] {
	return DurationExpression[M]{Scalar(operand)}
}

func ScaleDuration[M any, N DurationFactor](value ScalarOperand[M, duration.Duration], factor N) DurationExpression[M] {
	return ScaleDurationExpressions(value, Value[M](factor))
}

func ScaleDurationExpressions[M any, N DurationFactor](value ScalarOperand[M, duration.Duration], factor ScalarOperand[M, N]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticMultiply, value, factor)}
}

func DivideDuration[M any, N DurationFactor](value ScalarOperand[M, duration.Duration], divisor N) DurationExpression[M] {
	return DivideDurationExpressions(value, Value[M](divisor))
}

func DivideDurationExpressions[M any, N DurationFactor](value ScalarOperand[M, duration.Duration], divisor ScalarOperand[M, N]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticDivide, value, divisor)}
}

func temporalOperands[M, Result, Left, Right any](operator query.ArithmeticOperator, left ScalarOperand[M, Left], right ScalarOperand[M, Right]) ScalarExpression[M, Result] {
	first, err := operandExpression(left)
	if err != nil {
		return ScalarExpression[M, Result]{err: err}
	}
	second, err := operandExpression(right)
	if err != nil {
		return ScalarExpression[M, Result]{err: err}
	}
	expression, err := query.Arithmetic(operator, first, second)
	if err == nil && expression.ResultKind() != scalarKind[Result]() {
		err = invalidResultBuilder("temporal expression result differs from its typed domain")
	}
	return ScalarExpression[M, Result]{expression: expression, err: err}
}

func (value DateTimeExpression[M]) Add(right duration.Duration) DateTimeExpression[M] {
	return value.AddExpression(Value[M](right))
}
func (value DateTimeExpression[M]) Subtract(right duration.Duration) DateTimeExpression[M] {
	return value.SubtractExpression(Value[M](right))
}
func (value DateTimeExpression[M]) AddExpression(right ScalarOperand[M, duration.Duration]) DateTimeExpression[M] {
	return DateTimeExpression[M]{temporalOperands[M, time.Time](query.ArithmeticAdd, value, right)}
}
func (value DateTimeExpression[M]) SubtractExpression(right ScalarOperand[M, duration.Duration]) DateTimeExpression[M] {
	return DateTimeExpression[M]{temporalOperands[M, time.Time](query.ArithmeticSubtract, value, right)}
}
func (value DateTimeExpression[M]) Difference(right time.Time) DurationExpression[M] {
	return value.DifferenceExpression(Value[M](right))
}
func (value DateTimeExpression[M]) DifferenceExpression(right ScalarOperand[M, time.Time]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticSubtract, value, right)}
}

func (value DurationExpression[M]) Add(right duration.Duration) DurationExpression[M] {
	return value.AddExpression(Value[M](right))
}
func (value DurationExpression[M]) Subtract(right duration.Duration) DurationExpression[M] {
	return value.SubtractExpression(Value[M](right))
}
func (value DurationExpression[M]) AddExpression(right ScalarOperand[M, duration.Duration]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticAdd, value, right)}
}
func (value DurationExpression[M]) SubtractExpression(right ScalarOperand[M, duration.Duration]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticSubtract, value, right)}
}
func (value DurationExpression[M]) AddTo(right time.Time) DateTimeExpression[M] {
	return value.AddToExpression(Value[M](right))
}
func (value DurationExpression[M]) AddToExpression(right ScalarOperand[M, time.Time]) DateTimeExpression[M] {
	return DateTimeExpression[M]{temporalOperands[M, time.Time](query.ArithmeticAdd, value, right)}
}
func (value DurationExpression[M]) Negate() DurationExpression[M] {
	if value.err != nil {
		return value
	}
	expression, err := query.NegateScalar(value.expression)
	return DurationExpression[M]{ScalarExpression[M, duration.Duration]{expression: expression, err: err}}
}

func (value DurationExpression[M]) Ratio(right duration.Duration) NumericExpression[M, float64] {
	return value.RatioExpression(Value[M](right))
}

func (value DurationExpression[M]) RatioExpression(right ScalarOperand[M, duration.Duration]) NumericExpression[M, float64] {
	return NumericExpression[M, float64]{temporalOperands[M, float64](query.ArithmeticDivide, value, right)}
}

func (value DurationExpression[M]) aggregate(kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	if value.err != nil {
		return query.ResultExpression{}, nil, value.err
	}
	expression, err := query.ScalarAggregateResult(kind, value.expression)
	if err != nil {
		return query.ResultExpression{}, nil, err
	}
	cell, err := computedCell[duration.Duration](expression, true)
	return expression, cell, err
}
func (value DurationExpression[M]) scalarSumField(M, duration.Duration) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	return value.aggregate(query.ResultSum)
}
func (value DurationExpression[M]) scalarAvgField(M, duration.Duration) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	return value.aggregate(query.ResultAvg)
}
func (value DurationExpression[M]) scalarOrderedField(_ M, _ duration.Duration, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[duration.Duration]], error) {
	return value.aggregate(kind)
}
func (value DateTimeExpression[M]) scalarOrderedField(_ M, _ time.Time, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[time.Time]], error) {
	if value.err != nil {
		return query.ResultExpression{}, nil, value.err
	}
	expression, err := query.ScalarAggregateResult(kind, value.expression)
	if err != nil {
		return query.ResultExpression{}, nil, err
	}
	cell, err := computedCell[time.Time](expression, true)
	return expression, cell, err
}
