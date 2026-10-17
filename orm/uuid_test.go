package orm_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDScannersKeepZeroPresentAndRejectForeignStorage(t *testing.T) {
	for _, text := range []string{"00000000-0000-0000-0000-000000000000", "12345678-9abc-4def-8123-456789abcdef", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		value, err := uuid.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []any{value, value.Hex()} {
			var required orm.UUIDScanner
			var optional orm.NullableUUIDScanner
			if err := required.Scan(raw); err != nil || required.UUID != value {
				t.Fatal("UUID scanner lost bytes", err)
			}
			if err := optional.Scan(raw); err != nil || !optional.Valid || optional.UUID != value {
				t.Fatal("nullable UUID scanner lost presence or bytes", err)
			}
			if err := optional.Scan(nil); err != nil || optional.Valid || optional.UUID != (uuid.UUID{}) {
				t.Fatal("NULL scan retained UUID", err)
			}
		}
	}
	prior := uuid.UUID{15: 1}
	for _, raw := range []any{nil, "", strings.Repeat("0", 31), strings.Repeat("f", 33), strings.Repeat("F", 32),
		"00000000-0000-0000-0000-000000000000", strings.Repeat("g", 32), []byte(strings.Repeat("0", 32)), make([]byte, 16), false, int64(0), float64(0)} {
		required := orm.UUIDScanner{UUID: prior}
		optional := orm.NullableUUIDScanner{UUID: prior, Valid: true}
		if err := required.Scan(raw); err == nil || required.UUID != (uuid.UUID{}) {
			t.Fatal("invalid UUID read retained or published a value")
		}
		err := optional.Scan(raw)
		if (err == nil) != (raw == nil) || optional.Valid || optional.UUID != (uuid.UUID{}) {
			t.Fatal("invalid nullable UUID retained value", err)
		}
	}
	var missingRequired *orm.UUIDScanner
	var missingOptional *orm.NullableUUIDScanner
	if missingRequired.Scan(prior) == nil || missingOptional.Scan(nil) == nil {
		t.Fatal("nil UUID scanner accepted input")
	}
}

func TestUUIDQueryValueOwnershipAndTypedConditions(t *testing.T) {
	original := uuid.UUID{0: 0x80, 15: 1}
	want := original
	value := query.UUID(original)
	original[0] = 0
	got, ok := value.UUID()
	database, err := value.DatabaseValue()
	if !ok || got != want || err != nil || database != want || value.IsNull() {
		t.Fatal("UUID query lost exact owned value", err)
	}
	if query.UUID(uuid.UUID{}).Equal(query.Null()) || query.UUID(want).Equal(query.String(want.String())) {
		t.Fatal("UUID identity collapsed into null or text")
	}
	left := query.NewFieldRef("reference", "reference", query.FieldUUID, true)
	right := query.NewFieldRef("mirror", "mirror", query.FieldUUID, false)
	if _, err := query.NewFieldCondition(left, query.LookupLessThan, right); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewInCondition(left, []query.Value{value, query.UUID(uuid.UUID{}), query.Null()}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []query.Value{query.String(want.String()), query.Integer(0), query.Float(0), query.Boolean(false), {}} {
		if _, err := query.NewInCondition(left, []query.Value{invalid}); err == nil {
			t.Fatal("UUID IN silently coerced another value kind")
		}
	}
	if _, err := query.NewFieldCondition(left, query.LookupExact, query.NewFieldRef("text", "text", query.FieldString, true)); err == nil {
		t.Fatal("UUID F accepted a text field")
	}
}
