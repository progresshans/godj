package orm

import (
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

// Date arithmetic uses UTC midnight and retains subday Duration values in a
// DateTime result. It does not truncate the period to whole calendar days.
func (value DateExpression[M]) Add(right duration.Duration) DateTimeExpression[M] {
	return value.AddExpression(Value[M](right))
}
func (value DateExpression[M]) AddExpression(right ScalarOperand[M, duration.Duration]) DateTimeExpression[M] {
	return DateTimeExpression[M]{temporalOperands[M, time.Time](query.ArithmeticAdd, value, right)}
}
func (value DateExpression[M]) Subtract(right duration.Duration) DateTimeExpression[M] {
	return value.SubtractExpression(Value[M](right))
}
func (value DateExpression[M]) SubtractExpression(right ScalarOperand[M, duration.Duration]) DateTimeExpression[M] {
	return DateTimeExpression[M]{temporalOperands[M, time.Time](query.ArithmeticSubtract, value, right)}
}
func (value DateExpression[M]) Difference(right calendar.Date) DurationExpression[M] {
	return value.DifferenceExpression(Value[M](right))
}
func (value DateExpression[M]) DifferenceExpression(right ScalarOperand[M, calendar.Date]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticSubtract, value, right)}
}
func (value DateExpression[M]) DifferenceDateTime(right time.Time) DurationExpression[M] {
	return value.DifferenceDateTimeExpression(Value[M](right))
}
func (value DateExpression[M]) DifferenceDateTimeExpression(right ScalarOperand[M, time.Time]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticSubtract, value, right)}
}
func (value DateTimeExpression[M]) DifferenceDate(right calendar.Date) DurationExpression[M] {
	return value.DifferenceDateExpression(Value[M](right))
}
func (value DateTimeExpression[M]) DifferenceDateExpression(right ScalarOperand[M, calendar.Date]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticSubtract, value, right)}
}

// A Time shift wraps at a day boundary; a Time difference is the signed
// same-day difference. Neither operation infers a missing calendar date.
func (value TimeExpression[M]) Add(right duration.Duration) TimeExpression[M] {
	return value.AddExpression(Value[M](right))
}
func (value TimeExpression[M]) AddExpression(right ScalarOperand[M, duration.Duration]) TimeExpression[M] {
	return TimeExpression[M]{temporalOperands[M, clock.Time](query.ArithmeticAdd, value, right)}
}
func (value TimeExpression[M]) Subtract(right duration.Duration) TimeExpression[M] {
	return value.SubtractExpression(Value[M](right))
}
func (value TimeExpression[M]) SubtractExpression(right ScalarOperand[M, duration.Duration]) TimeExpression[M] {
	return TimeExpression[M]{temporalOperands[M, clock.Time](query.ArithmeticSubtract, value, right)}
}
func (value TimeExpression[M]) Difference(right clock.Time) DurationExpression[M] {
	return value.DifferenceExpression(Value[M](right))
}
func (value TimeExpression[M]) DifferenceExpression(right ScalarOperand[M, clock.Time]) DurationExpression[M] {
	return DurationExpression[M]{temporalOperands[M, duration.Duration](query.ArithmeticSubtract, value, right)}
}
func (value DurationExpression[M]) AddToDate(right calendar.Date) DateTimeExpression[M] {
	return value.AddToDateExpression(Value[M](right))
}
func (value DurationExpression[M]) AddToDateExpression(right ScalarOperand[M, calendar.Date]) DateTimeExpression[M] {
	return DateTimeExpression[M]{temporalOperands[M, time.Time](query.ArithmeticAdd, value, right)}
}
func (value DurationExpression[M]) AddToTime(right clock.Time) TimeExpression[M] {
	return value.AddToTimeExpression(Value[M](right))
}
func (value DurationExpression[M]) AddToTimeExpression(right ScalarOperand[M, clock.Time]) TimeExpression[M] {
	return TimeExpression[M]{temporalOperands[M, clock.Time](query.ArithmeticAdd, value, right)}
}
