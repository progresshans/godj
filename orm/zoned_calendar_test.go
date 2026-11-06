package orm

import (
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

type zonedCalendarModel struct{}

func TestTypedDynamicZonedCalendarsBuildTheSameAST(t *testing.T) {
	zone, err := timezone.Parse("Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	for _, nullable := range []bool{false, true} {
		instant := time.Date(2024, 2, 29, 23, 59, 59, 999999000, time.UTC)
		typed := DateTime(Value[zonedCalendarModel](instant)).InZone(zone)
		dynamic := DynamicValue(instant).InZone(zone)
		if nullable {
			typed = DateTime(NullValue[zonedCalendarModel, time.Time]()).InZone(zone)
			dynamic = DynamicNull(query.FieldDateTime).InZone(zone)
		}
		cases := []struct {
			function query.FunctionKind
			typed    query.ScalarExpression
			dynamic  DynamicExpression
		}{
			{query.FunctionDate, typed.Date().expression, dynamic.Date()},
			{query.FunctionTime, typed.Time().expression, dynamic.Time()},
			{query.FunctionYear, typed.Year().expression, dynamic.Year()},
			{query.FunctionISOYear, typed.ISOYear().expression, dynamic.ISOYear()},
			{query.FunctionQuarter, typed.Quarter().expression, dynamic.Quarter()},
			{query.FunctionMonth, typed.Month().expression, dynamic.Month()},
			{query.FunctionWeek, typed.Week().expression, dynamic.Week()},
			{query.FunctionWeekDay, typed.WeekDay().expression, dynamic.WeekDay()},
			{query.FunctionISOWeekDay, typed.ISOWeekDay().expression, dynamic.ISOWeekDay()},
			{query.FunctionDay, typed.Day().expression, dynamic.Day()},
			{query.FunctionHour, typed.Hour().expression, dynamic.Hour()},
			{query.FunctionMinute, typed.Minute().expression, dynamic.Minute()},
			{query.FunctionSecond, typed.Second().expression, dynamic.Second()},
			{query.FunctionTruncYear, typed.TruncYear().expression, dynamic.TruncYear()},
			{query.FunctionTruncQuarter, typed.TruncQuarter().expression, dynamic.TruncQuarter()},
			{query.FunctionTruncMonth, typed.TruncMonth().expression, dynamic.TruncMonth()},
			{query.FunctionTruncWeek, typed.TruncWeek().expression, dynamic.TruncWeek()},
			{query.FunctionTruncDay, typed.TruncDay().expression, dynamic.TruncDay()},
			{query.FunctionTruncHour, typed.TruncHour().expression, dynamic.TruncHour()},
			{query.FunctionTruncMinute, typed.TruncMinute().expression, dynamic.TruncMinute()},
			{query.FunctionTruncSecond, typed.TruncSecond().expression, dynamic.TruncSecond()},
		}
		for _, test := range cases {
			bound, err := test.dynamic.bind(dynamicBinding{})
			if err != nil || test.typed.Validate() != nil || !bound.Equal(test.typed) || bound.Nullable() != nullable {
				t.Fatalf("function %d (NULL=%t) has different typed/dynamic AST: %v", test.function, nullable, err)
			}
			function, _, ok := bound.Function()
			got, explicit := bound.FunctionZone()
			if !ok || function != test.function || !explicit || got != zone {
				t.Fatal("bound function lost zone/operation identity")
			}
		}
		// A truncated instant is an instant again. Its subsequent calendar
		// frame is explicit, so a local zone cannot leak into other operations.
		repeated := typed.TruncHour().Year()
		if _, explicit := repeated.expression.FunctionZone(); explicit {
			t.Fatal("calendar frame leaked through a result")
		}
		if _, explicit := typed.TruncHour().InZone(zone).Year().expression.FunctionZone(); !explicit {
			t.Fatal("explicit frame was lost in a nested function")
		}
	}
}

func TestZonedCalendarRejectsInvalidZoneAndNonInstantBeforeBinding(t *testing.T) {
	zero := timezone.Zone{}
	typed := DateTime(Value[zonedCalendarModel](time.Time{})).InZone(zero).Date()
	if !errors.Is(typed.err, timezone.ErrInvalid) {
		t.Fatal("invalid typed zone was deferred or ignored", typed.err)
	}
	dynamic := DynamicF("unbound_name").InZone(zero).Date()
	if !errors.Is(dynamic.validate(), timezone.ErrInvalid) {
		t.Fatal("invalid dynamic zone reached model binding", dynamic.err)
	}
	sentinel := errors.New("original operand failed")
	broken := DateTime(ScalarExpression[zonedCalendarModel, time.Time]{err: sentinel}).InZone(timezone.UTC()).Year()
	if !errors.Is(broken.err, sentinel) {
		t.Fatal("zoned operation hid its operand error", broken.err)
	}
	zone, err := timezone.Parse("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{calendar.Date{Year: 2024, Month: time.January, Day: 1}, clock.Time{}, duration.Duration{}, int64(1), "2024-01-01", nil} {
		expression := DynamicValue(value).InZone(zone)
		for _, call := range []DynamicExpression{expression.Date(), expression.Time(), expression.Year(), expression.TruncHour()} {
			if _, err := call.bind(dynamicBinding{}); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				t.Fatalf("%T inferred a DateTime: %v", value, err)
			}
		}
	}
}
