package orm_test

import (
	"math"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestFloatScannersCanonicalBitsAndFailure(t *testing.T) {
	for _, number := range []float64{0, math.Copysign(0, -1), 0.1, math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64, math.Inf(1), math.Inf(-1), math.Float64frombits(0xfff8000000000007)} {
		expected := math.Float64bits(number)
		if math.IsNaN(number) {
			expected = 0x7ff8000000000000
		}
		var required orm.FloatScanner
		var nullable orm.NullableFloatScanner
		if err := required.Scan(number); err != nil || math.Float64bits(required.Float) != expected {
			t.Fatal("required Float scan changed bits")
		}
		if err := nullable.Scan(number); err != nil || !nullable.Valid || math.Float64bits(nullable.Float) != expected {
			t.Fatal("nullable Float scan changed presence/bits")
		}
		value := query.Float(number)
		snapshot, ok := value.Float()
		if !ok || value.IsNull() || math.Float64bits(snapshot) != expected {
			t.Fatal("query Float bits differ")
		}
	}
	for _, raw := range []any{nil, "1.5", []byte("1.5"), float32(1), true, int(1)} {
		required := orm.FloatScanner{Float: 1.5}
		if err := required.Scan(raw); err == nil || required.Float != 0 {
			t.Fatal("invalid required scan retained value")
		}
		nullable := orm.NullableFloatScanner{Float: 1.5, Valid: true}
		err := nullable.Scan(raw)
		if (err == nil) != (raw == nil) || nullable.Valid || nullable.Float != 0 {
			t.Fatal("nullable failed scan retained presence")
		}
	}
	var numeric orm.FloatScanner
	if err := numeric.Scan(int64(9007199254740993)); err != nil || numeric.Float != 9007199254740992 {
		t.Fatal("native integer-to-float storage conversion differs")
	}
	if query.Float(0).Equal(query.Float(math.Copysign(0, -1))) {
		t.Fatal("AST identity discarded zero sign")
	}
	if !query.Float(math.NaN()).Equal(query.Float(math.Float64frombits(0xfff8000000000001))) {
		t.Fatal("AST NaN identity is unstable")
	}
	field := query.NewFieldRef("amount", "amount", query.FieldFloat, true)
	for _, lookup := range []query.Lookup{query.LookupExact, query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual} {
		if _, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, lookup, query.Float(0.1))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := query.NewFieldCondition(field, query.LookupExact, query.NewFieldRef("other", "other", query.FieldFloat, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewInCondition(field, []query.Value{query.Float(0), query.Null()}); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewInCondition(field, []query.Value{query.Integer(0)}); err == nil {
		t.Fatal("IN coerced integer")
	}
}
