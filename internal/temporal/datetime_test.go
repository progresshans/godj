package temporal_test

import (
	"github.com/progresshans/godj/internal/temporal"
	"testing"
	"time"
)

func TestInstantsHaveOneUTCIdentityAndMicrosecondPrecision(t *testing.T) {
	canonical := "2026-09-19T03:34:56.123456Z"
	for _, raw := range []string{"2026-09-19T12:34:56.123456789+09:00", "2026-09-18T22:34:56.123456-05:00", "2026-09-19t03:34:56.123456z"} {
		value, err := temporal.ParseRFC3339(raw)
		if err != nil || temporal.Format(value) != canonical || value.Location() != time.UTC {
			t.Fatalf("canonical %q: %v %v", raw, value, err)
		}
	}
	now := time.Now()
	actual, err := temporal.Canonical(now)
	if err != nil || actual != now.UTC().Truncate(time.Microsecond) || actual != actual.Round(0) {
		t.Fatal("location or monotonic clock escaped canonicalization")
	}
	for _, raw := range []string{"0001-01-01T00:00:00.000000Z", "9999-12-31T23:59:59.999999Z", "1969-12-31T23:59:59.999999Z"} {
		value, err := temporal.ParseCanonical(raw)
		if err != nil || temporal.Format(value) != raw {
			t.Fatalf("boundary %q: %v", raw, err)
		}
	}
}

func TestInvalidInstantsNeverNormalizeCalendarErrorsOrRangeOverflow(t *testing.T) {
	for _, raw := range []string{"", "2026-02-30T00:00:00Z", "1900-02-29T00:00:00Z", "2026-09-19T24:00:00Z", "2026-09-19T03:34:60Z", "2026-09-19T03:34:56+24:00", "2026-09-19T03:34:56+00:60", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00+01:00", "9999-12-31T23:59:59-01:00", "2026-09-19", "2026-09-19 03:34:56", "2026-09-19T03:34:56Z\x00"} {
		if _, err := temporal.ParseRFC3339(raw); err == nil {
			t.Fatalf("invalid wire instant accepted: %q", raw)
		}
	}
	for _, raw := range []string{"2026-09-19T03:34:56Z", "2026-09-19T03:34:56.123456789Z", "2026-09-19T12:34:56.123456+09:00"} {
		if _, err := temporal.ParseCanonical(raw); err == nil {
			t.Fatalf("noncanonical historical value accepted: %q", raw)
		}
	}
	for _, year := range []int{0, 10000} {
		if _, err := temporal.Canonical(time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
			t.Fatal("unsupported UTC year accepted")
		}
	}
}

func TestDatabaseScanAcceptsNativeAndAggregateValuesWithoutEpochFallback(t *testing.T) {
	want := time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC)
	for _, raw := range []any{want, "1969-12-31 23:59:59.999999", []byte("1969-12-31 23:59:59.999999")} {
		got, err := temporal.FromDatabase(raw)
		if err != nil || got != want {
			t.Fatalf("database value %T: %v", raw, err)
		}
	}
	for _, raw := range []any{nil, int64(0), true, "infinity", "invalid", "2026-02-30 00:00:00"} {
		if _, err := temporal.FromDatabase(raw); err == nil {
			t.Fatalf("unsupported database value %T accepted", raw)
		}
	}
}

func TestFormEndOfDayAndFullInputConsumption(t *testing.T) {
	for _, raw := range []string{"2026-09-19T24:00:00Z", "2026-09-20T00:00:00.000000Z", "2026-09-20T09:00:00+09:00", "2026-09-19T24:00:00.0000001Z"} {
		value, err := temporal.ParseFormUTC(raw)
		if err != nil || temporal.Format(value) != "2026-09-20T00:00:00.000000Z" {
			t.Fatalf("form end of day %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"  ", "2026-09-19T24:01:00Z", "2026-09-19T24:00:01Z", "2026-09-19T24:00:00.000001Z", "9999-12-31T24:00:00+09:00", "2026-09-19T03:34:56Z\x00", "2026-09-19T03:34:56Z\x00garbage"} {
		if _, err := temporal.ParseFormUTC(raw); err == nil {
			t.Fatalf("unsupported form input accepted: %q", raw)
		}
	}
}
