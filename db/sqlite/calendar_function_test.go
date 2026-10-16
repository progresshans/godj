package sqlite

import (
	"database/sql/driver"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestCalendarNativeExtractionValues(t *testing.T) {
	for _, test := range []struct {
		name     string
		function query.FunctionKind
		kind     query.FieldKind
		input    driver.Value
		want     driver.Value
	}{
		{"date_year_one", query.FunctionDate, query.FieldDateTime, "0001-01-01 00:00:00.000000", "0001-01-01"},
		{"date_year_last", query.FunctionDate, query.FieldDateTime, "9999-12-31 23:59:59.999999", "9999-12-31"},
		{"time_midnight", query.FunctionTime, query.FieldDateTime, "2024-02-29 00:00:00.000000", "00:00:00"},
		{"time_microsecond", query.FunctionTime, query.FieldDateTime, "2024-02-29 23:59:59.000001", "23:59:59.000001"},
		{"year", query.FunctionYear, query.FieldDate, "2021-01-01", int64(2021)},
		{"iso_year_previous", query.FunctionISOYear, query.FieldDate, "2021-01-01", int64(2020)},
		{"iso_year_next", query.FunctionISOYear, query.FieldDateTime, "2019-12-30 23:59:59.999999", int64(2020)},
		{"quarter_one", query.FunctionQuarter, query.FieldDate, "2024-03-31", int64(1)},
		{"quarter_two", query.FunctionQuarter, query.FieldDateTime, "2024-04-01 00:00:00.000000", int64(2)},
		{"quarter_four", query.FunctionQuarter, query.FieldDate, "9999-12-31", int64(4)},
		{"month", query.FunctionMonth, query.FieldDateTime, "2400-02-29 01:02:03.000000", int64(2)},
		{"week_53", query.FunctionWeek, query.FieldDate, "2021-01-01", int64(53)},
		{"week_1", query.FunctionWeek, query.FieldDateTime, "2019-12-30 00:00:00.000000", int64(1)},
		{"weekday_sunday", query.FunctionWeekDay, query.FieldDate, "2024-03-03", int64(1)},
		{"weekday_monday", query.FunctionWeekDay, query.FieldDateTime, "0001-01-01 00:00:00.000000", int64(2)},
		{"iso_weekday_sunday", query.FunctionISOWeekDay, query.FieldDateTime, "2024-03-03 00:00:00.000000", int64(7)},
		{"iso_weekday_monday", query.FunctionISOWeekDay, query.FieldDate, "0001-01-01", int64(1)},
		{"leap_day", query.FunctionDay, query.FieldDate, "2400-02-29", int64(29)},
		{"hour", query.FunctionHour, query.FieldTime, "23:59:59.999999", int64(23)},
		{"minute", query.FunctionMinute, query.FieldDateTime, "2026-10-16 00:34:59.999999", int64(34)},
		{"second_datetime_truncates", query.FunctionSecond, query.FieldDateTime, "2026-10-16 00:34:59.999999", int64(59)},
		{"second_time_truncates", query.FunctionSecond, query.FieldTime, "00:00:00.999999", int64(0)},
		{"date_null", query.FunctionDate, query.FieldDateTime, nil, nil},
		{"time_null", query.FunctionTime, query.FieldDateTime, nil, nil},
		{"year_null", query.FunctionYear, query.FieldDate, nil, nil},
		{"hour_null", query.FunctionHour, query.FieldTime, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := sqliteCalendarFunction(nil, []driver.Value{int64(test.function), string(test.kind), test.input})
			if err != nil || got != test.want {
				t.Fatalf("got %T(%v), want %T(%v): %v", got, got, test.want, test.want, err)
			}
		})
	}
}

func TestCalendarNativeExtractionRejectsInvalidPhysicalInputs(t *testing.T) {
	for _, test := range []struct {
		kind     query.FieldKind
		function query.FunctionKind
		values   []driver.Value
	}{
		{query.FieldDateTime, query.FunctionYear, []driver.Value{
			"2026-02-30 00:00:00.000000", "0000-01-01 00:00:00.000000", "10000-01-01 00:00:00.000000",
			"2026-10-16 00:00:00", "2026-10-16T00:00:00.000000Z", "2026-10-16 00:00:00.000000+00",
			"2026-10-16 24:00:00.000000", []byte("2026-10-16 00:00:00.000000"), int64(1), float64(1),
		}},
		{query.FieldDate, query.FunctionMonth, []driver.Value{
			"0000-01-01", "10000-01-01", "1900-02-29", "2026-02-30", "2026-1-01", "20260101",
			"2026-01-01\n", "2026-01-01 00:00:00", []byte("2026-01-01"), int64(1), float64(1),
		}},
		{query.FieldTime, query.FunctionSecond, []driver.Value{
			"24:00:00", "23:59:60", "00:60:00", "-01:00:00", "0:00:00", "00:00:00.000000",
			"00:00:00.1", "00:00:00.0000001", "00:00:00+00:00", "00:00:00\n", []byte("00:00:00"), int64(0), float64(0),
		}},
	} {
		for _, input := range test.values {
			if _, err := sqliteCalendarFunction(nil, []driver.Value{int64(test.function), string(test.kind), input}); err == nil {
				t.Fatal("extraction hid an invalid temporal operand", test.kind, input)
			}
		}
	}
	for _, arguments := range [][]driver.Value{
		nil, {int64(query.FunctionYear), "date"}, {int64(query.FunctionYear), "date", nil, nil},
		{int64(0), "date", nil}, {int64(256) + int64(query.FunctionYear), "date", nil},
		{"3", "date", nil}, {int64(query.FunctionYear), "time", nil}, {int64(query.FunctionHour), "date", nil},
		{int64(query.FunctionDate), "date", nil}, {int64(query.FunctionTime), "time", nil},
		{int64(query.FunctionYear), "duration", nil}, {int64(query.FunctionMonth), "json", nil},
	} {
		if _, err := sqliteCalendarFunction(nil, arguments); err == nil {
			t.Fatal("invalid function signature reached NULL propagation", arguments)
		}
	}
}
