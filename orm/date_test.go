package orm_test

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"testing"
	"time"
)

func TestCalendarDateScannersPreserveDayAndRejectClocksOrInvalidReads(t *testing.T) {
	expected := calendar.Date{Year: 2000, Month: 2, Day: 29}
	for _, raw := range []any{"2000-02-29", []byte("2000-02-29"), time.Date(2000, 2, 29, 0, 0, 0, 0, time.FixedZone("+14", 14*3600)), time.Date(2000, 2, 29, 0, 0, 0, 0, time.FixedZone("-12", -12*3600))} {
		var required orm.DateScanner
		var optional orm.NullableDateScanner
		if err := required.Scan(raw); err != nil || required.Date != expected {
			t.Fatalf("driver date shifted: %v %v", required.Date, err)
		}
		if err := optional.Scan(raw); err != nil || !optional.Valid || optional.Date != expected {
			t.Fatal("nullable driver date shifted")
		}
	}
	for _, raw := range []any{nil, "0000-01-01", "1900-02-29", "2000-2-29", "2000-02-29 00:00:00", "2000-02-29T00:00:00Z", time.Date(2000, 2, 29, 1, 0, 0, 0, time.UTC), time.Date(2000, 2, 29, 0, 0, 0, 1, time.UTC), 0, false, expected} {
		required := orm.DateScanner{Date: expected}
		optional := orm.NullableDateScanner{Date: expected, Valid: true}
		if err := required.Scan(raw); err == nil || required.Date != (calendar.Date{}) {
			t.Fatalf("bad date read retained/published value: %v", raw)
		}
		err := optional.Scan(raw)
		if (err == nil) != (raw == nil) || optional.Valid || optional.Date != (calendar.Date{}) {
			t.Fatalf("nullable bad read lost presence: %v", raw)
		}
	}
	var nilRequired *orm.DateScanner
	var nilOptional *orm.NullableDateScanner
	if nilRequired.Scan("2000-02-29") == nil || nilOptional.Scan(nil) == nil {
		t.Fatal("nil date scanner accepted")
	}
	value := query.Date(expected)
	expected.Day = 1
	date, ok := value.Date()
	dbValue, err := value.DatabaseValue()
	if !ok || date.Day != 29 || err != nil || dbValue != "2000-02-29" {
		t.Fatal("query date lost canonical snapshot")
	}
	if invalid := query.Date(calendar.Date{}); invalid.IsNull() || invalid.Kind() == query.ValueDate {
		t.Fatal("invalid date acquired valid kind")
	}
}
