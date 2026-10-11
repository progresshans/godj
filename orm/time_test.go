package orm_test

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"testing"
	"time"
)

func TestClockTimeScannersPreserveMicrosecondsAndClearFailedReads(t *testing.T) {
	for _, test := range []struct {
		raw  any
		want clock.Time
	}{
		{"00:00:00", clock.Time{}}, {"00:00:00.000000", clock.Time{}}, {[]byte("23:59:59.999999"), clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}},
		{"12:34:56.1", clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 100000}},
		{"12:34:56.000001", clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 1}},
	} {
		required := orm.TimeScanner{}
		optional := orm.NullableTimeScanner{}
		if err := required.Scan(test.raw); err != nil || required.Time != test.want {
			t.Fatalf("clock driver scan changed value: %v %v", required, err)
		}
		if err := optional.Scan(test.raw); err != nil || !optional.Valid || optional.Time != test.want {
			t.Fatal("clock driver scan lost presence")
		}
	}
	before := clock.Time{Hour: 12}
	for _, raw := range []any{nil, "24:00:00", "12:60:00", "12:00:60", "12:00", "12:00:00.", "12:00:00.1234567", "12:00:00Z", "12:00:00+09:00", "2000-01-01T12:00:00", " 12:00:00", time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC), false, 0, before} {
		required := orm.TimeScanner{Time: before}
		optional := orm.NullableTimeScanner{Time: before, Valid: true}
		if err := required.Scan(raw); err == nil || required.Time != (clock.Time{}) {
			t.Fatalf("bad clock read retained/published a value: %v", raw)
		}
		err := optional.Scan(raw)
		if (err == nil) != (raw == nil) || optional.Valid || optional.Time != (clock.Time{}) {
			t.Fatalf("nullable failed clock retained presence: %v", raw)
		}
	}
	var required *orm.TimeScanner
	var optional *orm.NullableTimeScanner
	if required.Scan("00:00:00") == nil || optional.Scan(nil) == nil {
		t.Fatal("nil scanner accepted")
	}
	value := query.Time(before)
	before.Hour = 1
	got, ok := value.Time()
	raw, err := value.DatabaseValue()
	if !ok || got.Hour != 12 || err != nil || raw != "12:00:00" {
		t.Fatal("query clock lost snapshot")
	}
	if value := query.Time(clock.Time{}); value.IsNull() || value.Kind() != query.ValueTime {
		t.Fatal("midnight became null")
	}
	if value := query.Time(clock.Time{Hour: 24}); value.IsNull() || value.Kind() == query.ValueTime {
		t.Fatal("invalid clock acquired a valid scalar kind")
	}
}
