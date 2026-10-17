package calendar_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
)

func TestGregorianDateCoversLeapCycleAndSupportedBoundaries(t *testing.T) {
	// A complete 400-year cycle compares validation to an independent standard
	// library normalization, including century exceptions and month overflow.
	for year := 1800; year < 2200; year++ {
		for month := time.January; month <= time.December; month++ {
			for day := 0; day <= 32; day++ {
				value := calendar.Date{Year: year, Month: month, Day: day}
				y, m, d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Date()
				if value.Valid() != (y == year && m == month && d == day) {
					t.Fatalf("Gregorian validity differs at %d-%d-%d", year, month, day)
				}
			}
		}
	}
	for _, text := range []string{"0001-01-01", "9999-12-31", "2000-02-29", "1900-03-01"} {
		value, err := calendar.Parse(text)
		if err != nil || !value.Valid() || value.String() != text {
			t.Fatalf("supported date boundary %s: %v", text, err)
		}
	}
	for _, value := range []calendar.Date{
		{}, {Year: math.MinInt, Month: 1, Day: 1}, {Year: math.MaxInt, Month: 1, Day: 1},
		{Year: 10000, Month: 1, Day: 1}, {Year: 2026, Month: 0, Day: 1}, {Year: 2026, Month: 13, Day: 1},
		{Year: 2026, Month: 1, Day: math.MinInt}, {Year: 2026, Month: 1, Day: math.MaxInt},
	} {
		if value.Valid() {
			t.Fatal("invalid date literal accepted")
		}
		if _, err := calendar.New(value.Year, value.Month, value.Day); !errors.Is(err, calendar.ErrInvalid) {
			t.Fatal("invalid components did not return the date error")
		}
		if _, err := json.Marshal(value); !errors.Is(err, calendar.ErrInvalid) {
			t.Fatal("invalid date literal serialized")
		}
	}
}

func TestDateCanonicalWireAndFailurePreserveReceiver(t *testing.T) {
	baseline, err := calendar.New(2026, time.September, 20)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(baseline)
	if err != nil || string(data) != `"2026-09-20"` {
		t.Fatal("date JSON invented a clock or time zone")
	}
	var roundtrip calendar.Date
	if err := json.Unmarshal(data, &roundtrip); err != nil || roundtrip != baseline {
		t.Fatal("date roundtrip changed its components")
	}
	for _, raw := range []string{"", " 2026-09-20", "2026-9-20", "20260920", "2026-W38-7", "2026-02-29", "2026-09-20T00:00:00Z", "2026-09-20\x00", "２０２６-09-20"} {
		value := baseline
		if err := value.UnmarshalText([]byte(raw)); !errors.Is(err, calendar.ErrInvalid) || value != baseline {
			t.Fatalf("invalid date text %q changed its receiver", raw)
		}
	}
	for _, raw := range []string{`null`, `true`, `0`, `{}`, `[]`, `""`, `"2026-09-20T00:00:00Z"`, `"2026-02-29"`, `"2026-09-20" "trailing"`} {
		value := baseline
		if err := value.UnmarshalJSON([]byte(raw)); !errors.Is(err, calendar.ErrInvalid) || value != baseline {
			t.Fatalf("invalid date JSON %s changed its receiver", raw)
		}
	}
	var pointer *calendar.Date
	if err := json.Unmarshal([]byte("null"), &pointer); err != nil || pointer != nil {
		t.Fatal("nullable pointer lost null")
	}
	if err := pointer.UnmarshalText([]byte("2026-09-20")); !errors.Is(err, calendar.ErrInvalid) {
		t.Fatal("nil receiver did not fail explicitly")
	}
}
