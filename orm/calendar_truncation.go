package orm

import (
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/query"
)

// TruncYear and the other Trunc methods return the beginning of the containing
// calendar or clock unit. They keep the operand's type and nullable domain.
// Instants use UTC, weeks begin on Monday, and seconds discard microseconds.
func (value DateTimeExpression[M]) TruncYear() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncYear, value)}
}
func (value DateTimeExpression[M]) TruncQuarter() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncQuarter, value)}
}
func (value DateTimeExpression[M]) TruncMonth() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncMonth, value)}
}
func (value DateTimeExpression[M]) TruncWeek() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncWeek, value)}
}
func (value DateTimeExpression[M]) TruncDay() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncDay, value)}
}
func (value DateTimeExpression[M]) TruncHour() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncHour, value)}
}
func (value DateTimeExpression[M]) TruncMinute() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncMinute, value)}
}
func (value DateTimeExpression[M]) TruncSecond() DateTimeExpression[M] {
	return DateTimeExpression[M]{functionOperand[M, time.Time](query.FunctionTruncSecond, value)}
}

func (value DateExpression[M]) TruncYear() DateExpression[M] {
	return DateExpression[M]{functionOperand[M, calendar.Date](query.FunctionTruncYear, value)}
}
func (value DateExpression[M]) TruncQuarter() DateExpression[M] {
	return DateExpression[M]{functionOperand[M, calendar.Date](query.FunctionTruncQuarter, value)}
}
func (value DateExpression[M]) TruncMonth() DateExpression[M] {
	return DateExpression[M]{functionOperand[M, calendar.Date](query.FunctionTruncMonth, value)}
}
func (value DateExpression[M]) TruncWeek() DateExpression[M] {
	return DateExpression[M]{functionOperand[M, calendar.Date](query.FunctionTruncWeek, value)}
}
func (value DateExpression[M]) TruncDay() DateExpression[M] {
	return DateExpression[M]{functionOperand[M, calendar.Date](query.FunctionTruncDay, value)}
}

func (value TimeExpression[M]) TruncHour() TimeExpression[M] {
	return TimeExpression[M]{functionOperand[M, clock.Time](query.FunctionTruncHour, value)}
}
func (value TimeExpression[M]) TruncMinute() TimeExpression[M] {
	return TimeExpression[M]{functionOperand[M, clock.Time](query.FunctionTruncMinute, value)}
}
func (value TimeExpression[M]) TruncSecond() TimeExpression[M] {
	return TimeExpression[M]{functionOperand[M, clock.Time](query.FunctionTruncSecond, value)}
}
