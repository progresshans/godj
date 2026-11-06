package orm

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/query"
)

// DateExpression is a Gregorian calendar day without a clock or time zone.
// DateTimeExpression.Date extracts the UTC day of an instant explicitly.
type DateExpression[M any] struct {
	ScalarExpression[M, calendar.Date]
}

// TimeExpression is a wall clock without a calendar day or time zone.
type TimeExpression[M any] struct {
	ScalarExpression[M, clock.Time]
}

func Date[M any](operand ScalarOperand[M, calendar.Date]) DateExpression[M] {
	return DateExpression[M]{Scalar(operand)}
}

func Time[M any](operand ScalarOperand[M, clock.Time]) TimeExpression[M] {
	return TimeExpression[M]{Scalar(operand)}
}

func functionOperand[M, Result, Input any](function query.FunctionKind, operand ScalarOperand[M, Input]) ScalarExpression[M, Result] {
	argument, err := operandExpression(operand)
	if err != nil {
		return ScalarExpression[M, Result]{err: err}
	}
	return typedFunctionResult[M, Result](query.FunctionExpression(function, argument))
}

func typedFunctionResult[M, Result any](expression query.ScalarExpression, err error) ScalarExpression[M, Result] {
	if err == nil && expression.ResultKind() != scalarKind[Result]() {
		err = invalidResultBuilder("function result differs from its typed domain")
	}
	return ScalarExpression[M, Result]{expression: expression, err: err}
}

// Date returns the UTC Gregorian day; Time retains the UTC microsecond clock.
// Neither operation uses the connection's zone or the caller's time.Location.
func (value DateTimeExpression[M]) Date() DateExpression[M] {
	return DateExpression[M]{functionOperand[M, calendar.Date](query.FunctionDate, value)}
}
func (value DateTimeExpression[M]) Time() TimeExpression[M] {
	return TimeExpression[M]{functionOperand[M, clock.Time](query.FunctionTime, value)}
}

func calendarPart[M, Input any](function query.FunctionKind, operand ScalarOperand[M, Input]) NumericExpression[M, int64] {
	return NumericExpression[M, int64]{functionOperand[M, int64](function, operand)}
}

func (value DateTimeExpression[M]) Year() NumericExpression[M, int64] {
	return calendarPart(query.FunctionYear, value)
}
func (value DateTimeExpression[M]) ISOYear() NumericExpression[M, int64] {
	return calendarPart(query.FunctionISOYear, value)
}
func (value DateTimeExpression[M]) Quarter() NumericExpression[M, int64] {
	return calendarPart(query.FunctionQuarter, value)
}
func (value DateTimeExpression[M]) Month() NumericExpression[M, int64] {
	return calendarPart(query.FunctionMonth, value)
}
func (value DateTimeExpression[M]) Week() NumericExpression[M, int64] {
	return calendarPart(query.FunctionWeek, value)
}
func (value DateTimeExpression[M]) WeekDay() NumericExpression[M, int64] {
	return calendarPart(query.FunctionWeekDay, value)
}
func (value DateTimeExpression[M]) ISOWeekDay() NumericExpression[M, int64] {
	return calendarPart(query.FunctionISOWeekDay, value)
}
func (value DateTimeExpression[M]) Day() NumericExpression[M, int64] {
	return calendarPart(query.FunctionDay, value)
}
func (value DateTimeExpression[M]) Hour() NumericExpression[M, int64] {
	return calendarPart(query.FunctionHour, value)
}
func (value DateTimeExpression[M]) Minute() NumericExpression[M, int64] {
	return calendarPart(query.FunctionMinute, value)
}
func (value DateTimeExpression[M]) Second() NumericExpression[M, int64] {
	return calendarPart(query.FunctionSecond, value)
}

func (value DateExpression[M]) Year() NumericExpression[M, int64] {
	return calendarPart(query.FunctionYear, value)
}
func (value DateExpression[M]) ISOYear() NumericExpression[M, int64] {
	return calendarPart(query.FunctionISOYear, value)
}
func (value DateExpression[M]) Quarter() NumericExpression[M, int64] {
	return calendarPart(query.FunctionQuarter, value)
}
func (value DateExpression[M]) Month() NumericExpression[M, int64] {
	return calendarPart(query.FunctionMonth, value)
}
func (value DateExpression[M]) Week() NumericExpression[M, int64] {
	return calendarPart(query.FunctionWeek, value)
}
func (value DateExpression[M]) WeekDay() NumericExpression[M, int64] {
	return calendarPart(query.FunctionWeekDay, value)
}
func (value DateExpression[M]) ISOWeekDay() NumericExpression[M, int64] {
	return calendarPart(query.FunctionISOWeekDay, value)
}
func (value DateExpression[M]) Day() NumericExpression[M, int64] {
	return calendarPart(query.FunctionDay, value)
}

func (value TimeExpression[M]) Hour() NumericExpression[M, int64] {
	return calendarPart(query.FunctionHour, value)
}
func (value TimeExpression[M]) Minute() NumericExpression[M, int64] {
	return calendarPart(query.FunctionMinute, value)
}
func (value TimeExpression[M]) Second() NumericExpression[M, int64] {
	return calendarPart(query.FunctionSecond, value)
}

func calendarAggregate[M, Input any](value ScalarOperand[M, Input], kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[Input]], error) {
	argument, err := operandExpression(value)
	if err != nil {
		return query.ResultExpression{}, nil, err
	}
	expression, err := query.ScalarAggregateResult(kind, argument)
	if err != nil {
		return query.ResultExpression{}, nil, err
	}
	cell, err := computedCell[Input](expression, true)
	return expression, cell, err
}

func (value DateExpression[M]) scalarOrderedField(_ M, _ calendar.Date, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[calendar.Date]], error) {
	return calendarAggregate(value, kind)
}
func (value TimeExpression[M]) scalarOrderedField(_ M, _ clock.Time, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[clock.Time]], error) {
	return calendarAggregate(value, kind)
}
