package sqlite

import (
	"database/sql/driver"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestSQLiteZonedCalendarPreservesLocalComponentsAndFoldZero(t *testing.T) {
	for _, test := range []struct {
		zone, input string
		function    query.FunctionKind
		want        driver.Value
	}{
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionDate, "2025-01-01"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTime, "01:34:59.999999"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionYear, int64(2025)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionISOYear, int64(2025)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionQuarter, int64(1)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionMonth, int64(1)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionWeek, int64(1)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionWeekDay, int64(4)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionISOWeekDay, int64(3)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionDay, int64(1)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionHour, int64(1)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionMinute, int64(34)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionSecond, int64(59)},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncYear, "2024-12-31 15:00:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncQuarter, "2024-12-31 15:00:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncMonth, "2024-12-31 15:00:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncWeek, "2024-12-29 15:00:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncDay, "2024-12-31 15:00:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncHour, "2024-12-31 16:00:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncMinute, "2024-12-31 16:34:00.000000"},
		{"Asia/Seoul", "2024-12-31 16:34:59.999999", query.FunctionTruncSecond, "2024-12-31 16:34:59.000000"},
		{"America/New_York", "2024-11-03 06:30:59.999999", query.FunctionTruncHour, "2024-11-03 05:00:00.000000"},
		{"America/New_York", "2024-11-03 06:30:59.999999", query.FunctionTruncSecond, "2024-11-03 05:30:59.000000"},
		{"Australia/Lord_Howe", "2024-10-05 15:45:00.000000", query.FunctionTruncHour, "2024-10-05 15:30:00.000000"},
		{"Australia/Lord_Howe", "2024-04-06 15:15:00.000000", query.FunctionTruncMinute, "2024-04-06 14:45:00.000000"},
		{"America/Sao_Paulo", "2018-11-04 04:30:00.000000", query.FunctionTruncDay, "2018-11-04 03:00:00.000000"},
		{"Europe/Paris", "1900-01-01 00:00:00.000001", query.FunctionTime, "00:09:21.000001"},
		{"Europe/Paris", "1900-01-01 00:00:00.000001", query.FunctionTruncMinute, "1899-12-31 23:59:39.000000"},
		{"Pacific/Apia", "2011-12-30 10:30:00.000000", query.FunctionDate, "2011-12-31"},
	} {
		got, err := sqliteCalendarFunction(nil, []driver.Value{int64(test.function), string(query.FieldDateTime), test.input, test.zone})
		if err != nil || got != test.want {
			t.Fatalf("%s / %s / %d = %v, want %v: %v", test.zone, test.input, test.function, got, test.want, err)
		}
	}
}

func TestSQLiteZonedCalendarRejectsCompleteDomainBeforeExtracting(t *testing.T) {
	for _, test := range []struct {
		zone, input string
		function    query.FunctionKind
	}{
		{"Etc/GMT+1", "0001-01-01 00:00:00.000000", query.FunctionSecond},
		{"Asia/Seoul", "9999-12-31 23:59:59.999999", query.FunctionSecond},
		{"Asia/Seoul", "0001-01-01 12:00:00.000000", query.FunctionTruncDay},
		{"UTC", "0000-01-01 00:00:00.000000", query.FunctionYear},
		{"America/New_York", "10000-01-01 00:00:00.000000", query.FunctionYear},
		{"Asia/Seoul", "2024-02-30 00:00:00.000000", query.FunctionMonth},
	} {
		if _, err := sqliteCalendarFunction(nil, []driver.Value{int64(test.function), string(query.FieldDateTime), test.input, test.zone}); err == nil {
			t.Fatalf("invalid full value became a component: %+v", test)
		}
	}
	for _, raw := range []driver.Value{int64(1), float64(1), []byte("2024-01-01 00:00:00.000000")} {
		if _, err := sqliteCalendarFunction(nil, []driver.Value{int64(query.FunctionYear), string(query.FieldDateTime), raw, "UTC"}); err == nil {
			t.Fatal("physical input was coerced", raw)
		}
	}
	for _, test := range []struct{ kind, zone string }{{"datetime", "Local"}, {"datetime", "Missing/Zone"}, {"datetime", "../UTC"}, {"date", "UTC"}, {"time", "UTC"}, {"duration", "UTC"}} {
		if _, err := sqliteCalendarFunction(nil, []driver.Value{int64(query.FunctionYear), test.kind, nil, test.zone}); err == nil {
			t.Fatal("NULL hid invalid signature/zone", test)
		}
	}
	for _, zone := range []string{"UTC", "Asia/Seoul", "America/New_York"} {
		for function := query.FunctionDate; function <= query.FunctionTruncSecond; function++ {
			if value, err := sqliteCalendarFunction(nil, []driver.Value{int64(function), "datetime", nil, zone}); err != nil || value != nil {
				t.Fatal("valid zoned NULL changed its domain", value, err)
			}
		}
	}
}
