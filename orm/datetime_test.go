package orm_test

import (
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"testing"
	"time"
)

func TestDateTimeValueAndScannerPreservePresenceAndRejectBadReadState(t *testing.T) {
	instant := time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("caller", 9*3600))
	if !query.DateTime(instant).Equal(query.DateTime(instant.UTC().Truncate(time.Microsecond))) {
		t.Fatal("datetime query value retained location or unsupported precision")
	}
	var nullable orm.NullableDateTimeScanner
	if err := nullable.Scan(time.Time{}); err != nil || !nullable.Valid || !nullable.Time.IsZero() {
		t.Fatal("nullable zero time lost presence")
	}
	if err := nullable.Scan("invalid"); err == nil || nullable.Valid || !nullable.Time.IsZero() {
		t.Fatal("bad read reused previous nullable time")
	}
	if err := nullable.Scan(nil); err != nil || nullable.Valid {
		t.Fatal("database NULL became a datetime")
	}
	var required orm.DateTimeScanner
	if err := required.Scan(nil); err == nil {
		t.Fatal("nonnullable time silently accepted NULL")
	}
	var nilScanner *orm.NullableDateTimeScanner
	if err := nilScanner.Scan(instant); err == nil {
		t.Fatal("nil datetime scanner accepted")
	}
	if value := query.DateTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); value.IsNull() || value.Kind() == query.ValueDateTime {
		t.Fatal("invalid time acquired a valid value kind")
	}
}
