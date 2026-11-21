package orm_test

import (
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"math"
	"testing"
)

func TestDurationScannersAndQueryPreserveRangePresenceAndFailure(t *testing.T) {
	for _, elapsed := range []duration.Duration{{}, duration.FromMicroseconds(-1), duration.FromMicroseconds(math.MinInt64), duration.FromMicroseconds(math.MaxInt64), {Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}} {
		sources := []any{elapsed.String(), []byte(elapsed.String())}
		if micros, err := elapsed.TotalMicroseconds(); err == nil {
			sources = append(sources, micros)
		}
		for _, raw := range sources {
			var required orm.DurationScanner
			var optional orm.NullableDurationScanner
			if err := required.Scan(raw); err != nil || required.Duration != elapsed {
				t.Fatalf("required duration: %v", err)
			}
			if err := optional.Scan(raw); err != nil || !optional.Valid || optional.Duration != elapsed {
				t.Fatal("optional duration lost range/presence")
			}
		}
		value := query.Duration(elapsed)
		snapshot, ok := value.Duration()
		if !ok || snapshot != elapsed || value.IsNull() {
			t.Fatal("duration query snapshot lost")
		}
	}
	before := duration.FromMicroseconds(1)
	for _, raw := range []any{nil, "0 00:00:00", "24:00:00", "PT1S", "1000000000 00:00:00", float64(1), int(1), false, before} {
		required := orm.DurationScanner{Duration: before}
		if err := required.Scan(raw); err == nil || required.Duration != (duration.Duration{}) {
			t.Fatal("failed read published duration")
		}
		optional := orm.NullableDurationScanner{Duration: before, Valid: true}
		err := optional.Scan(raw)
		if (err == nil) != (raw == nil) || optional.Valid || optional.Duration != (duration.Duration{}) {
			t.Fatal("failed read retained presence")
		}
	}
	field := query.NewFieldRef("elapsed", "elapsed", query.FieldDuration, false)
	if _, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupExact, query.Duration(before))); err != nil {
		t.Fatal(err)
	}
	if value := query.Duration(duration.Duration{Microseconds: -1}); value.Kind() == query.ValueDuration || value.IsNull() {
		t.Fatal("invalid duration became present or null")
	}
}
