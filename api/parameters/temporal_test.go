package parameters_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/serializers"
)

func TestCanonicalDurationParametersPreserveRangePresenceAndSchema(t *testing.T) {
	type input struct {
		Shift   duration.Duration
		Minimum *duration.Duration
	}
	setters := 0
	q, err := parameters.New(256,
		parameters.Field("shift", parameters.Default(parameters.CanonicalDuration(), duration.Duration{}), func(v *input, value duration.Duration) { setters++; v.Shift = value }, "Exact elapsed shift."),
		parameters.Field("minimum", parameters.Optional(parameters.CanonicalDuration()), func(v *input, value *duration.Duration) { setters++; v.Minimum = value }, "Optional minimum."),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"00:00:00", "00:00:00.000001", "-1 23:59:59.999999", "106751991 04:00:54.775808", "-999999999 00:00:00", "999999999 23:59:59.999999"} {
		value, diagnostics, err := q.Parse("shift=" + url.QueryEscape(raw) + "&minimum=00:00:00")
		if err != nil || !diagnostics.Empty() || value.Shift.String() != raw || value.Minimum == nil || *value.Minimum != (duration.Duration{}) {
			t.Fatal("canonical Duration input narrowed its model range or lost zero", raw, value, diagnostics, err)
		}
	}
	omitted, diagnostics, err := q.Parse("")
	if err != nil || !diagnostics.Empty() || omitted != (input{}) {
		t.Fatal("Duration omission differs from explicit zero", omitted, diagnostics, err)
	}
	for _, raw := range []string{"", "0", "PT1S", "1 day", "0 00:00:00", "-0 00:00:00", "01 00:00:00", "+1 00:00:00", "24:00:00", "00:60:00", "00:00:60", "00:00:00.000000", "00:00:00.1", "00:00:00.0000001", "1000000000 00:00:00", "-1000000000 00:00:00", "00:00:00\n", "00:00:00\x00", " 00:00:00"} {
		before := setters
		value, diagnostics, err := q.Parse("shift=" + url.QueryEscape(raw))
		if err != nil || diagnostics.Empty() || value != (input{}) || setters != before {
			t.Fatal("invalid Duration reached a setter", raw, value, diagnostics, err)
		}
	}
	metadata := q.Parameters()
	encoded, err := serializers.Encode(metadata[0].Schema.Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	pattern, _ := schema["pattern"].(string)
	if schema["type"] != "string" || schema["default"] != "00:00:00" || schema["maxLength"] != float64(26) || schema["x-godj-duration"] != "normalized-days-and-microseconds" || !strings.Contains(pattern, "(?!000000)") || !strings.HasSuffix(pattern, `$(?![\s\S])`) {
		t.Fatal("canonical Duration schema differs from its accepted text", string(encoded))
	}
	first, _, _ := q.Parse("minimum=00:00:00.000001")
	second, _, _ := q.Parse("minimum=00:00:00.000001")
	if first.Minimum == nil || second.Minimum == nil || first.Minimum == second.Minimum {
		t.Fatal("Duration option pointer is shared")
	}
	*first.Minimum = duration.Duration{}
	if *second.Minimum != duration.FromMicroseconds(1) {
		t.Fatal("caller changed another Duration result")
	}
}

func TestRFC3339DateTimeParametersRequireOffsetAndPreserveMicroseconds(t *testing.T) {
	type input struct {
		AsOf  time.Time
		Until *time.Time
	}
	setters := 0
	q, err := parameters.New(512,
		parameters.Field("as_of", parameters.Required(parameters.RFC3339DateTime(64)), func(v *input, value time.Time) { setters++; v.AsOf = value }, "Explicit reference instant."),
		parameters.Field("until", parameters.Optional(parameters.RFC3339DateTime(64)), func(v *input, value *time.Time) { setters++; v.Until = value }, "Optional endpoint."),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ raw, want string }{
		{"0001-01-01T00:00:00Z", "0001-01-01T00:00:00.000000Z"},
		{"9999-12-31T23:59:59.999999Z", "9999-12-31T23:59:59.999999Z"},
		{"2024-02-29T09:00:00.123456999+09:00", "2024-02-29T00:00:00.123456Z"},
		{"2024-02-28t19:00:00.000001z", "2024-02-28T19:00:00.000001Z"},
		{"2024-02-28T19:00:00-05:00", "2024-02-29T00:00:00.000000Z"},
		{"2026-10-15T00:00:00.0000009Z", "2026-10-15T00:00:00.000000Z"},
	} {
		value, diagnostics, err := q.Parse("as_of=" + url.QueryEscape(test.raw))
		if err != nil || !diagnostics.Empty() || value.AsOf.Location() != time.UTC || value.AsOf.Format("2006-01-02T15:04:05.000000Z") != test.want || value.Until != nil {
			t.Fatal("explicit-offset DateTime lost its instant or microseconds", test.raw, value, diagnostics, err)
		}
	}
	for _, raw := range []string{"", "2026-10-15", "2026-10-15T00:00:00", "2026-10-15 00:00:00Z", "2026-10-15T24:00:00Z", "2026-10-15T00:00:60Z", "2026-02-29T00:00:00Z", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00+00:01", "9999-12-31T23:59:59-00:01", "2026-10-15T00:00:00+24:00", "2026-10-15T00:00:00+09", "2026-10-15T00:00:00Z\n", "2026-10-15T00:00:00Z\x00", "2026-10-15T00:00:00." + strings.Repeat("1", 44) + "Z"} {
		before := setters
		value, diagnostics, err := q.Parse("as_of=" + url.QueryEscape(raw))
		if err != nil || diagnostics.Empty() || value != (input{}) || setters != before {
			t.Fatal("invalid or implicit-zone DateTime reached a setter", raw, value, diagnostics, err)
		}
	}
	for _, raw := range []string{"", "until=2026-10-15T00%3A00%3A00Z", "as_of=2026-10-15T00:00:00+09:00", "as_of=2026-10-15T00:00:00Z&as_of=2026-10-15T00:00:00Z", "as_of=%ZZ", "unknown=value"} {
		before := setters
		value, diagnostics, err := q.Parse(raw)
		if err != nil || diagnostics.Empty() || value != (input{}) || setters != before {
			t.Fatal("missing/duplicate/malformed DateTime query accepted", raw, diagnostics, err)
		}
	}
	metadata := q.Parameters()
	encoded, err := serializers.Encode(metadata[0].Schema.Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if !metadata[0].Required || metadata[1].Required || schema["format"] != "date-time" || schema["x-ogen-time-format"] != time.RFC3339Nano || schema["x-godj-input-offset"] != "required" || schema["maxLength"] != float64(64) || schema["default"] != nil {
		t.Fatal("DateTime parameter schema lost explicit offset or exact client format", string(encoded))
	}
}

func TestTemporalParameterDefaultsAndHeaderBudgets(t *testing.T) {
	type input struct {
		At   time.Time
		Span duration.Duration
	}
	canonical := time.Date(2026, 10, 15, 0, 0, 0, 123456000, time.UTC)
	for _, bad := range []time.Time{canonical.Add(time.Nanosecond), canonical.In(time.FixedZone("same-instant", 3600)), time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := parameters.New(128, parameters.Field("at", parameters.Default(parameters.RFC3339DateTime(64), bad), func(v *input, at time.Time) { v.At = at }, "Invalid default.")); err == nil {
			t.Fatal("noncanonical DateTime default accepted")
		}
	}
	for _, maximum := range []int{-1, 0, 19, 1<<20 + 1} {
		if _, err := parameters.New(128, parameters.Field("at", parameters.Required(parameters.RFC3339DateTime(maximum)), func(*input, time.Time) {}, "Invalid budget.")); err == nil {
			t.Fatal("invalid DateTime codec budget", maximum)
		}
	}
	if _, err := parameters.New(128, parameters.Field("span", parameters.Default(parameters.CanonicalDuration(), duration.Duration{Days: duration.MaxDays + 1}), func(*input, duration.Duration) {}, "Invalid period.")); err == nil {
		t.Fatal("invalid default Duration accepted")
	}
	header, err := parameters.NewHeader("X-As-Of", parameters.Default(parameters.RFC3339DateTime(64), canonical), 27, "Explicit instant.")
	if err != nil {
		t.Fatal(err)
	}
	value, diagnostics, err := header.Parse(nil)
	if err != nil || !diagnostics.Empty() || value != canonical {
		t.Fatal("DateTime default changed its exact identity", value, diagnostics, err)
	}
	for _, raw := range []string{"2026-10-15T00:00:00.123456Z", "2026-10-15T09:00:00+09:00"} {
		_, diagnostics, err := header.Parse(http.Header{"X-As-Of": {raw}})
		if err != nil || !diagnostics.Empty() {
			t.Fatal("DateTime header rejected offset", raw, diagnostics, err)
		}
	}
	for _, raw := range []string{"2026-10-15T00:00:00.123456+00:00", "2026-10-15T00:00:00Z\r\n", "2026-10-15T00%3A00%3A00Z"} {
		_, diagnostics, err := header.Parse(http.Header{"X-As-Of": {raw}})
		if err != nil || diagnostics.Empty() {
			t.Fatal("DateTime header lost its independent byte/transport boundary", raw, diagnostics, err)
		}
	}
	if _, err := parameters.NewHeader("X-As-Of", parameters.Default(parameters.RFC3339DateTime(64), canonical), 20, "Too small for this default."); err == nil {
		t.Fatal("header default bypassed byte limit")
	}
	period, err := parameters.NewHeader("X-Shift", parameters.Required(parameters.CanonicalDuration()), 8, "Whole subday period.")
	if err != nil {
		t.Fatal(err)
	}
	span, diagnostics, err := period.Parse(http.Header{"X-Shift": {"00:00:00"}})
	if err != nil || !diagnostics.Empty() || span != (duration.Duration{}) {
		t.Fatal("zero header Duration lost", span, diagnostics, err)
	}
	if _, diagnostics, err := period.Parse(http.Header{"X-Shift": {"00:00:00.000001"}}); err != nil || diagnostics.Empty() {
		t.Fatal("Duration header bypassed its byte limit")
	}
	if _, err := parameters.NewHeader("X-Shift", parameters.Required(parameters.CanonicalDuration()), 7, "Cannot contain a period."); err == nil {
		t.Fatal("impossible Duration header declared")
	}
}
