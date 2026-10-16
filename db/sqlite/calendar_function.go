package sqlite

import (
	"database/sql/driver"
	"errors"
	"strconv"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

var errCalendarOperand = errors.New("godj: calendar extraction requires a canonical date, clock or UTC instant")

func compileSQLiteFunction(expression query.ScalarExpression, operands []string) (string, error) {
	function, inputs, ok := expression.Function()
	if !ok || len(inputs) != 1 || len(operands) != 1 {
		return "", invalidPlan("calendar compiler requires one validated function operand")
	}
	return queryplan.ScalarSQL("godj_calendar_function(", strconv.Itoa(int(function)), ", '",
		string(inputs[0].ResultKind()), "', ", operands[0], ")")
}

// Extraction sees physical SQLite values before database/sql's converters.
// Validate the complete operand before returning a smaller component, so an
// invalid date or clock cannot become an apparently valid year or second.
// This deterministic function owns no shared state and consults no local zone.
func sqliteCalendarFunction(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 3 {
		return nil, errCalendarOperand
	}
	code, codeOK := arguments[0].(int64)
	kind, kindOK := arguments[1].(string)
	if !codeOK || !kindOK || code < int64(query.FunctionDate) || code > int64(query.FunctionSecond) {
		return nil, errCalendarOperand
	}
	function := query.FunctionKind(code)
	switch function {
	case query.FunctionDate, query.FunctionTime:
		if kind != string(query.FieldDateTime) {
			return nil, errCalendarOperand
		}
	case query.FunctionHour, query.FunctionMinute, query.FunctionSecond:
		if kind != string(query.FieldDateTime) && kind != string(query.FieldTime) {
			return nil, errCalendarOperand
		}
	default:
		if kind != string(query.FieldDateTime) && kind != string(query.FieldDate) {
			return nil, errCalendarOperand
		}
	}
	if arguments[2] == nil {
		return nil, nil
	}
	var date calendar.Date
	var wall clock.Time
	switch query.FieldKind(kind) {
	case query.FieldDateTime:
		instant, err := sqliteInstant(arguments[2])
		if err != nil {
			return nil, errors.Join(errCalendarOperand, err)
		}
		date = calendar.Date{Year: instant.Year(), Month: instant.Month(), Day: instant.Day()}
		wall = clock.Time{Hour: instant.Hour(), Minute: instant.Minute(), Second: instant.Second(), Microsecond: instant.Nanosecond() / 1000}
	case query.FieldDate:
		raw, ok := arguments[2].(string)
		if !ok {
			return nil, errCalendarOperand
		}
		value, err := calendar.Parse(raw)
		if err != nil {
			return nil, errors.Join(errCalendarOperand, err)
		}
		date = value
	case query.FieldTime:
		raw, ok := arguments[2].(string)
		if !ok {
			return nil, errCalendarOperand
		}
		value, err := clock.Parse(raw)
		if err != nil {
			return nil, errors.Join(errCalendarOperand, err)
		}
		wall = value
	}
	switch function {
	case query.FunctionDate:
		return date.String(), nil
	case query.FunctionTime:
		return wall.String(), nil
	case query.FunctionYear:
		return int64(date.Year), nil
	case query.FunctionQuarter:
		return int64((date.Month-1)/3 + 1), nil
	case query.FunctionMonth:
		return int64(date.Month), nil
	case query.FunctionDay:
		return int64(date.Day), nil
	case query.FunctionHour:
		return int64(wall.Hour), nil
	case query.FunctionMinute:
		return int64(wall.Minute), nil
	case query.FunctionSecond:
		return int64(wall.Second), nil
	}
	// Only the ISO/weekday operations need a Gregorian calendar conversion.
	// The input was fully validated; time.Date cannot normalize a bad day.
	day := time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC)
	switch function {
	case query.FunctionISOYear:
		year, _ := day.ISOWeek()
		return int64(year), nil
	case query.FunctionWeek:
		_, week := day.ISOWeek()
		return int64(week), nil
	case query.FunctionWeekDay:
		return int64(day.Weekday()) + 1, nil
	case query.FunctionISOWeekDay:
		return int64((day.Weekday()+6)%7) + 1, nil
	default:
		return nil, errCalendarOperand
	}
}
