package orm

import (
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

// ZonedDateTimeExpression selects the civil frame for a calendar operation.
// It is not itself a scalar or a replacement for the underlying UTC instant.
// Extraction returns a calendar/clock/number; truncation returns a UTC instant.
// A further calendar operation on that instant uses UTC unless InZone is repeated.
type ZonedDateTimeExpression[M any] struct {
	instant DateTimeExpression[M]
	zone    timezone.Zone
}

func (value DateTimeExpression[M]) InZone(zone timezone.Zone) ZonedDateTimeExpression[M] {
	return ZonedDateTimeExpression[M]{instant: value, zone: zone}
}

func zonedFunctionOperand[M, Result any](function query.FunctionKind, value ZonedDateTimeExpression[M]) ScalarExpression[M, Result] {
	argument, err := operandExpression[M, time.Time](value.instant)
	if err != nil {
		return ScalarExpression[M, Result]{err: err}
	}
	return typedFunctionResult[M, Result](query.FunctionExpressionInZone(function, value.zone, argument))
}

// DynamicZonedDateTimeExpression defers field binding, while keeping the zone
// explicit. Binding rejects Date, Time, Duration and untyped NULL operands.
type DynamicZonedDateTimeExpression struct {
	instant DynamicExpression
	zone    timezone.Zone
}

func (value DynamicExpression) InZone(zone timezone.Zone) DynamicZonedDateTimeExpression {
	return DynamicZonedDateTimeExpression{instant: value, zone: zone}
}

func (value ZonedDateTimeExpression[M]) Date() DateExpression[M] {
	return DateExpression[M]{zonedFunctionOperand[M, calendar.Date](query.FunctionDate, value)}
}
func (value DynamicZonedDateTimeExpression) Date() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionDate, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Time() TimeExpression[M] {
	return TimeExpression[M]{zonedFunctionOperand[M, clock.Time](query.FunctionTime, value)}
}
func (value DynamicZonedDateTimeExpression) Time() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTime, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Year() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionYear, value)}
}
func (value DynamicZonedDateTimeExpression) Year() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionYear, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) ISOYear() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionISOYear, value)}
}
func (value DynamicZonedDateTimeExpression) ISOYear() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionISOYear, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Quarter() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionQuarter, value)}
}
func (value DynamicZonedDateTimeExpression) Quarter() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionQuarter, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Month() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionMonth, value)}
}
func (value DynamicZonedDateTimeExpression) Month() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionMonth, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Week() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionWeek, value)}
}
func (value DynamicZonedDateTimeExpression) Week() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionWeek, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) WeekDay() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionWeekDay, value)}
}
func (value DynamicZonedDateTimeExpression) WeekDay() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionWeekDay, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) ISOWeekDay() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionISOWeekDay, value)}
}
func (value DynamicZonedDateTimeExpression) ISOWeekDay() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionISOWeekDay, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Day() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionDay, value)}
}
func (value DynamicZonedDateTimeExpression) Day() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionDay, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Hour() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionHour, value)}
}
func (value DynamicZonedDateTimeExpression) Hour() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionHour, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Minute() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionMinute, value)}
}
func (value DynamicZonedDateTimeExpression) Minute() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionMinute, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) Second() NumericExpression[M, int64] {
	return NumericExpression[M, int64]{zonedFunctionOperand[M, int64](query.FunctionSecond, value)}
}
func (value DynamicZonedDateTimeExpression) Second() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionSecond, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncYear() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncYear, value)}
}
func (value DynamicZonedDateTimeExpression) TruncYear() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncYear, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncQuarter() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncQuarter, value)}
}
func (value DynamicZonedDateTimeExpression) TruncQuarter() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncQuarter, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncMonth() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncMonth, value)}
}
func (value DynamicZonedDateTimeExpression) TruncMonth() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncMonth, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncWeek() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncWeek, value)}
}
func (value DynamicZonedDateTimeExpression) TruncWeek() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncWeek, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncDay() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncDay, value)}
}
func (value DynamicZonedDateTimeExpression) TruncDay() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncDay, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncHour() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncHour, value)}
}
func (value DynamicZonedDateTimeExpression) TruncHour() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncHour, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncMinute() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncMinute, value)}
}
func (value DynamicZonedDateTimeExpression) TruncMinute() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncMinute, value.zone, true)
}

func (value ZonedDateTimeExpression[M]) TruncSecond() DateTimeExpression[M] {
	return DateTimeExpression[M]{zonedFunctionOperand[M, time.Time](query.FunctionTruncSecond, value)}
}
func (value DynamicZonedDateTimeExpression) TruncSecond() DynamicExpression {
	return value.instant.calendarInZone(query.FunctionTruncSecond, value.zone, true)
}
