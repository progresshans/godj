package parameters_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/serializers"
)

func TestCanonicalCalendarParametersKeepPresenceAndRejectAliases(t *testing.T) {
	type input struct {
		Day   calendar.Date
		Clock *clock.Time
	}
	setters := 0
	query, err := parameters.New(128,
		parameters.Field("day", parameters.Required(parameters.CanonicalDate()), func(value *input, date calendar.Date) { setters++; value.Day = date }, "Canonical Gregorian day."),
		parameters.Field("clock", parameters.Optional(parameters.CanonicalTime()), func(value *input, time *clock.Time) { setters++; value.Clock = time }, "Optional clock."),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{"0001-01-01", "2000-02-29", "2400-02-29", "9999-12-31"} {
		for _, clock := range []string{"00:00:00", "00:00:00.000001", "23:59:59.999999"} {
			value, diagnostics, err := query.Parse("day=" + day + "&clock=" + url.QueryEscape(clock))
			if err != nil || !diagnostics.Empty() || value.Day.String() != day || value.Clock == nil || value.Clock.String() != clock {
				t.Fatal("calendar query changed domain, precision or presence", day, clock, value, diagnostics, err)
			}
		}
	}
	value, diagnostics, err := query.Parse("day=0001-01-01")
	if err != nil || !diagnostics.Empty() || value.Day != (calendar.Date{Year: 1, Month: 1, Day: 1}) || value.Clock != nil {
		t.Fatal("omitted clock became midnight", value, diagnostics, err)
	}
	for _, day := range []string{"", "0000-01-01", "10000-01-01", "1900-02-29", "2026-02-30", "2026-1-01", "20260101", "2026-W01-1", "2026-01-01T00:00:00Z", "2026-01-01\n", "2026-01-01\x00", " 2026-01-01", "２０２６-01-01"} {
		before := setters
		value, diagnostics, err := query.Parse("day=" + url.QueryEscape(day) + "&clock=00%3A00%3A00")
		if err != nil || diagnostics.Empty() || value != (input{}) || setters != before {
			t.Fatal("invalid calendar date reached a setter", day, value, diagnostics, err)
		}
	}
	for _, clock := range []string{"", "24:00:00", "23:59:60", "0:00:00", "00:00", "00:00:00Z", "00:00:00+09:00", "00:00:00.1", "00:00:00.000000", "00:00:00.0000001", "00:00:00\n", "00:00:00\x00"} {
		before := setters
		value, diagnostics, err := query.Parse("day=2026-01-01&clock=" + url.QueryEscape(clock))
		if err != nil || diagnostics.Empty() || value != (input{}) || setters != before {
			t.Fatal("invalid clock reached a setter or partially published the date", clock, value, diagnostics, err)
		}
	}
	for _, raw := range []string{"", "clock=00%3A00%3A00", "day=2026-01-01&day=2026-01-01", "day=%ZZ", "day=2026-01-01&unknown=1"} {
		before := setters
		if value, diagnostics, err := query.Parse(raw); err != nil || diagnostics.Empty() || value != (input{}) || setters != before {
			t.Fatal("missing, duplicate or malformed calendar input was accepted", raw, diagnostics, err)
		}
	}
	first, _, _ := query.Parse("day=2026-01-01&clock=00:00:00.000001")
	second, _, _ := query.Parse("day=2026-01-01&clock=00:00:00.000001")
	if first.Clock == nil || second.Clock == nil || first.Clock == second.Clock {
		t.Fatal("clock results share caller-visible pointer storage")
	}
	first.Clock.Microsecond = 2
	if second.Clock.Microsecond != 1 {
		t.Fatal("one query mutated another clock result")
	}
	metadata := query.Parameters()
	for index, parameter := range metadata {
		encoded, err := serializers.Encode(parameter.Schema.Value(), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		pattern, _ := schema["pattern"].(string)
		if schema["type"] != "string" || !strings.HasSuffix(pattern, `$(?![\s\S])`) || schema["default"] != nil || parameter.Required != (index == 0) {
			t.Fatal("calendar parameter schema lost spelling or presence", string(encoded))
		}
		if index == 0 && (schema["format"] != "date" || schema["maxLength"] != float64(10)) {
			t.Fatal("Date parameter wire changed", string(encoded))
		}
		if index == 1 {
			policy, _ := schema["x-godj-time"].(map[string]any)
			if schema["format"] != nil || schema["maxLength"] != float64(15) || policy["inputOffset"] != "reject" || policy["subMicrosecond"] != "reject" || !strings.Contains(pattern, "(?!000000)") {
				t.Fatal("canonical clock schema claimed model input coercion", string(encoded))
			}
		}
	}
}

func TestCalendarParameterDefaultsAndHeaderBudgets(t *testing.T) {
	day := calendar.Date{Year: 1, Month: 1, Day: 1}
	dateHeader, err := parameters.NewHeader("X-Date", parameters.Default(parameters.CanonicalDate(), day), 10, "Day.")
	if err != nil {
		t.Fatal(err)
	}
	if value, diagnostics, err := dateHeader.Parse(nil); err != nil || !diagnostics.Empty() || value != day {
		t.Fatal("default Date changed", value, diagnostics, err)
	}
	for _, header := range []http.Header{{"X-Date": {"9999-12-31"}}, {"x-date": {"2000-02-29"}}} {
		if _, diagnostics, err := dateHeader.Parse(header); err != nil || !diagnostics.Empty() {
			t.Fatal("valid Date header rejected", diagnostics, err)
		}
	}
	for _, header := range []http.Header{{"X-Date": {"2026-01-01\n"}}, {"X-Date": {"2026-01-01T00:00:00Z"}}, {"X-Date": {"2026-01-01", "2026-01-01"}}, {"X-Date": {"2026%2D01%2D01"}}} {
		if _, diagnostics, err := dateHeader.Parse(header); err != nil || diagnostics.Empty() {
			t.Fatal("Date header lost its transport or byte boundary", diagnostics, err)
		}
	}
	for _, maximum := range []int{0, 9, 1<<20 + 1} {
		if _, err := parameters.NewHeader("X-Date", parameters.Required(parameters.CanonicalDate()), maximum, "Invalid budget."); err == nil {
			t.Fatal("invalid Date header budget accepted", maximum)
		}
	}
	for _, invalid := range []calendar.Date{{}, {Year: 10000, Month: 1, Day: 1}, {Year: 1900, Month: 2, Day: 29}} {
		if _, err := parameters.NewHeader("X-Date", parameters.Default(parameters.CanonicalDate(), invalid), 10, "Invalid day."); err == nil {
			t.Fatal("invalid Date default accepted")
		}
	}
	timeHeader, err := parameters.NewHeader("X-Clock", parameters.Default(parameters.CanonicalTime(), clock.Time{}), 8, "Clock.")
	if err != nil {
		t.Fatal(err)
	}
	if value, diagnostics, err := timeHeader.Parse(nil); err != nil || !diagnostics.Empty() || value != (clock.Time{}) {
		t.Fatal("default midnight lost its present identity", value, diagnostics, err)
	}
	if value, diagnostics, err := timeHeader.Parse(http.Header{"X-Clock": {"23:59:59"}}); err != nil || !diagnostics.Empty() || value != (clock.Time{Hour: 23, Minute: 59, Second: 59}) {
		t.Fatal("valid bounded clock rejected", value, diagnostics, err)
	}
	if _, diagnostics, err := timeHeader.Parse(http.Header{"X-Clock": {"00:00:00.000001"}}); err != nil || diagnostics.Empty() {
		t.Fatal("clock ignored the header byte limit", diagnostics, err)
	}
	for _, invalid := range []clock.Time{{Hour: 24}, {Microsecond: 1}} {
		if _, err := parameters.NewHeader("X-Clock", parameters.Default(parameters.CanonicalTime(), invalid), 8, "Invalid or oversized default."); err == nil {
			t.Fatal("clock default bypassed its domain or budget")
		}
	}
	if _, err := parameters.NewHeader("X-Clock", parameters.Required(parameters.CanonicalTime()), 7, "Too small."); err == nil {
		t.Fatal("impossible clock header declared")
	}
}
