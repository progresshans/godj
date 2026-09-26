package clock_test

import (
	"encoding/json"
	"fmt"
	"github.com/progresshans/godj/clock"
	"testing"
)

func TestClockTimeCanonicalBoundariesAndMidnightPresence(t *testing.T) {
	for second := 0; second < 24*60*60; second++ {
		value, err := clock.New(second/3600, second/60%60, second%60, 0)
		if err != nil || !value.Valid() || value.String() != fmt.Sprintf("%02d:%02d:%02d", second/3600, second/60%60, second%60) {
			t.Fatal("clock second domain differs")
		}
		decoded, err := clock.Parse(value.String())
		if err != nil || decoded != value {
			t.Fatal("clock round trip differs")
		}
	}
	for _, value := range []clock.Time{{}, {Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}, {Microsecond: 1}} {
		wire, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded clock.Time
		if err := json.Unmarshal(wire, &decoded); err != nil || decoded != value {
			t.Fatal("clock JSON lost value")
		}
	}
	for _, value := range []clock.Time{{Hour: -1}, {Hour: 24}, {Minute: 60}, {Minute: -1}, {Second: 60}, {Second: -1}, {Microsecond: 1000000}, {Microsecond: -1}} {
		if value.Valid() {
			t.Fatal("invalid clock components accepted")
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("invalid clock encoded")
		}
	}
	value := clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456}
	for _, raw := range []string{`null`, `"24:00:00"`, `"12:34"`, `"12:34:56Z"`, `"12:34:56+09:00"`, `"12:34:56.000000"`, `"12:34:56.1"`, `" 12:34:56 "`, `0`, `false`, `"2000-02-29T12:34:56"`} {
		before := value
		if err := json.Unmarshal([]byte(raw), &value); err == nil || value != before {
			t.Fatal("failed clock decode published value")
		}
	}
	var optional struct {
		Time *clock.Time `json:"time"`
	}
	if err := json.Unmarshal([]byte(`{"time":null}`), &optional); err != nil || optional.Time != nil {
		t.Fatal("null acquired midnight")
	}
	if err := json.Unmarshal([]byte(`{"time":"00:00:00"}`), &optional); err != nil || optional.Time == nil || *optional.Time != (clock.Time{}) {
		t.Fatal("midnight lost presence")
	}
}
