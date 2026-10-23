package sqlite

import (
	"database/sql/driver"
	"errors"
	"strconv"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/internal/zonerules"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

var errCalendarOperand = errors.New("godj: calendar function requires a canonical date, clock or UTC instant")

func compileSQLiteFunction(expression query.ScalarExpression, operands []string, bind queryplan.ScalarParameterBinder) (string, error) {
	function, inputs, ok := expression.Function()
	if !ok || len(inputs) != 1 || len(operands) != 1 {
		return "", invalidPlan("calendar compiler requires one validated function operand")
	}
	zoneSQL := "''"
	if zone, explicit := expression.FunctionZone(); explicit {
		var err error
		zoneSQL, err = bind(query.String(zone.Name()))
		if err != nil {
			return "", err
		}
	}
	return queryplan.ScalarSQL("godj_calendar_function(", strconv.Itoa(int(function)), ", '",
		string(inputs[0].ResultKind()), "', ", operands[0], ", ", zoneSQL, ")")
}

// Extraction sees physical SQLite values before database/sql's converters.
// Validate the complete operand before returning a smaller component, so an
// invalid date or clock cannot become an apparently valid year or second.
// This deterministic function owns no shared state and consults no local zone.
func sqliteCalendarFunction(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 4 {
		return nil, errCalendarOperand
	}
	code, codeOK := arguments[0].(int64)
	kind, kindOK := arguments[1].(string)
	if !codeOK || !kindOK || code < int64(query.FunctionDate) || code > int64(query.FunctionTruncSecond) {
		return nil, errCalendarOperand
	}
	function := query.FunctionKind(code)
	switch function {
	case query.FunctionDate, query.FunctionTime:
		if kind != string(query.FieldDateTime) {
			return nil, errCalendarOperand
		}
	case query.FunctionHour, query.FunctionMinute, query.FunctionSecond,
		query.FunctionTruncHour, query.FunctionTruncMinute, query.FunctionTruncSecond:
		if kind != string(query.FieldDateTime) && kind != string(query.FieldTime) {
			return nil, errCalendarOperand
		}
	default:
		if kind != string(query.FieldDateTime) && kind != string(query.FieldDate) {
			return nil, errCalendarOperand
		}
	}
	zoneName, ok := arguments[3].(string)
	if !ok {
		return nil, errCalendarOperand
	}
	var zone *zonerules.Rules
	if zoneName != "" {
		if kind != string(query.FieldDateTime) {
			return nil, errCalendarOperand
		}
		rules, err := zonerules.Load(zoneName)
		if err != nil {
			return nil, errors.Join(errCalendarOperand, err)
		}
		zone = &rules
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
		if zone != nil {
			offset, err := zone.OffsetAt(instant.Unix())
			if err != nil {
				return nil, errors.Join(errCalendarOperand, err)
			}
			instant = instant.Add(time.Duration(offset) * time.Second)
		}
		date = calendar.Date{Year: instant.Year(), Month: instant.Month(), Day: instant.Day()}
		wall = clock.Time{Hour: instant.Hour(), Minute: instant.Minute(), Second: instant.Second(), Microsecond: instant.Nanosecond() / 1000}
		if !date.Valid() || !wall.Valid() {
			return nil, errCalendarOperand
		}
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
	if function >= query.FunctionTruncYear && function <= query.FunctionTruncSecond {
		return sqliteCalendarTruncation(function, query.FieldKind(kind), date, wall, zone)
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

// The complete physical operand has already been validated. Truncation
// changes calendar components; it never represents a Gregorian date as a
// floating-point epoch or normalizes an invalid input through time.Date.
func sqliteCalendarTruncation(function query.FunctionKind, kind query.FieldKind, date calendar.Date, wall clock.Time, zone *zonerules.Rules) (driver.Value, error) {
	switch function {
	case query.FunctionTruncYear:
		date.Month, date.Day = time.January, 1
		wall = clock.Time{}
	case query.FunctionTruncQuarter:
		date.Month, date.Day = (date.Month-1)/3*3+1, 1
		wall = clock.Time{}
	case query.FunctionTruncMonth:
		date.Day = 1
		wall = clock.Time{}
	case query.FunctionTruncWeek:
		day := time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC)
		day = day.AddDate(0, 0, -int((day.Weekday()+6)%7))
		date.Year, date.Month, date.Day = day.Date()
		wall = clock.Time{}
	case query.FunctionTruncDay:
		wall = clock.Time{}
	case query.FunctionTruncHour:
		wall.Minute, wall.Second, wall.Microsecond = 0, 0, 0
	case query.FunctionTruncMinute:
		wall.Second, wall.Microsecond = 0, 0
	case query.FunctionTruncSecond:
		wall.Microsecond = 0
	default:
		return nil, errCalendarOperand
	}
	switch kind {
	case query.FieldDateTime:
		if !date.Valid() || !wall.Valid() {
			return nil, errCalendarOperand
		}
		instant := time.Date(date.Year, date.Month, date.Day, wall.Hour, wall.Minute, wall.Second, wall.Microsecond*1000, time.UTC)
		if zone != nil {
			offset, err := zone.OffsetForCivil(instant.Unix())
			if err != nil {
				return nil, errors.Join(errCalendarOperand, err)
			}
			instant = instant.Add(-time.Duration(offset) * time.Second)
		}
		instant, err := temporal.Canonical(instant)
		if err != nil {
			return nil, errors.Join(errCalendarOperand, err)
		}
		return instant.Format(temporal.SQLiteLayout), nil
	case query.FieldDate:
		if !date.Valid() {
			return nil, errCalendarOperand
		}
		return date.String(), nil
	case query.FieldTime:
		if !wall.Valid() {
			return nil, errCalendarOperand
		}
		return wall.String(), nil
	default:
		return nil, errCalendarOperand
	}
}
