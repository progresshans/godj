package orm_test

import (
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestDecimalScannersPrecisionTypesAndFailureReset(t *testing.T) {
	for _, input := range []string{"0", "-0", "1.23", "-999.99", "999.99"} {
		value, err := decimal.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		key, err := decimalstorage.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []any{value, key} {
			required := orm.NewDecimalScanner(5, 2)
			nullable := orm.NewNullableDecimalScanner(5, 2)
			if err := required.Scan(raw); err != nil || !required.Decimal.Equal(value) {
				t.Fatalf("required %s: %v", input, err)
			}
			if err := nullable.Scan(raw); err != nil || !nullable.Valid || !nullable.Decimal.Equal(value) {
				t.Fatalf("nullable %s: %v", input, err)
			}
		}
	}
	overscale, _ := decimal.Parse("1.001")
	overflow, _ := decimal.Parse("1000")
	overscaleKey, _ := decimalstorage.Encode(overscale)
	overflowKey, _ := decimalstorage.Encode(overflow)
	for _, raw := range []any{nil, "1.5", []byte("1.5"), int64(1), float64(1), true, []byte{2}, decimal.Decimal{Coefficient: "NaN"}, overscale, overflow, overscaleKey, overflowKey} {
		required := orm.NewDecimalScanner(5, 2)
		nullable := orm.NewNullableDecimalScanner(5, 2)
		if err := required.Scan(decimal.Decimal{Coefficient: "1"}); err != nil {
			t.Fatal(err)
		}
		if err := nullable.Scan(decimal.Decimal{Coefficient: "1"}); err != nil {
			t.Fatal(err)
		}
		if err := required.Scan(raw); err == nil || required.Decimal != (decimal.Decimal{}) {
			t.Fatalf("invalid required value %T retained state", raw)
		}
		err := nullable.Scan(raw)
		if (err == nil) != (raw == nil) || nullable.Valid || nullable.Decimal != (decimal.Decimal{}) {
			t.Fatalf("invalid nullable value %T retained state: %v", raw, err)
		}
	}
	var missing orm.NullableDecimalScanner
	if err := missing.Scan(nil); err == nil {
		t.Fatal("unconfigured scanner accepted NULL")
	}
	for _, spec := range [][2]int{{0, 0}, {1001, 0}, {5, -1}, {5, 6}} {
		scanner := orm.NewDecimalScanner(spec[0], spec[1])
		if err := scanner.Scan(decimal.Decimal{}); err == nil {
			t.Fatal("invalid scanner precision accepted")
		}
	}
}

func TestDecimalQueryIdentityPrecisionAndNoCoercion(t *testing.T) {
	positive, negative := query.Decimal(decimal.Decimal{}), query.Decimal(decimal.Decimal{Coefficient: "-0"})
	if positive.Equal(negative) {
		t.Fatal("AST identity lost zero sign")
	}
	if !query.Decimal(decimal.Decimal{Coefficient: "01500", Exponent: -3}).Equal(query.Decimal(decimal.Decimal{Coefficient: "15", Exponent: -1})) {
		t.Fatal("AST retained noncanonical digits")
	}
	if query.Decimal(decimal.Decimal{Coefficient: "NaN"}).Kind() == query.ValueDecimal {
		t.Fatal("invalid decimal has a query value")
	}
	field := query.NewDecimalFieldRef("cost", "cost", true, 5, 2)
	wide := query.NewDecimalFieldRef("wide", "wide", false, 30, 12)
	if _, err := query.NewFieldCondition(field, query.LookupLessThan, wide); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewInCondition(field, []query.Value{positive, query.Null()}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []query.Value{query.Float(0), query.Integer(0), query.String("0"), query.Value{}} {
		if _, err := query.NewInCondition(field, []query.Value{value}); err == nil {
			t.Fatal("IN coerced invalid decimal")
		}
	}
	invalid := query.NewFieldRef("invalid", "invalid", query.FieldDecimal, true)
	if _, err := query.NewFieldCondition(field, query.LookupExact, invalid); err == nil {
		t.Fatal("F comparison accepted missing precision")
	}
	if _, err := query.NewInCondition(invalid, nil); err == nil {
		t.Fatal("empty IN bypassed precision validation")
	}
	if field.Equal(query.NewDecimalFieldRef("cost", "cost", true, 6, 2)) {
		t.Fatal("field identity ignored precision")
	}
}
