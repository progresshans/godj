package serializers_test

import (
	"errors"
	"github.com/progresshans/godj/serializers"
	"strings"
	"testing"
)

func TestExactNumbersPreserveTokensAndIntegerFieldBoundary(t *testing.T) {
	field, err := serializers.IntegerField("number")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "-1", "9223372036854775807", "-9223372036854775808", "0.5", "-0", "1e2", "1.0000000000000000001", "9223372036854775808", "1E+999999999999999999", "1e-999999999999999999"} {
		value, err := serializers.Number(raw)
		if err != nil {
			t.Fatal(err)
		}
		token, ok := value.AsNumber()
		if !ok || token != raw {
			t.Fatal("number precision lost")
		}
		object, err := serializers.DecodeObject([]byte(`{"number":`+raw+`}`), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := serializers.EncodeObject(object, serializers.Limits{})
		if err != nil || string(encoded) != `{"number":`+raw+`}` {
			t.Fatalf("number round trip: %s %v", encoded, err)
		}
		decoded, _ := object.Get("number")
		if got, _ := decoded.AsNumber(); got != raw {
			t.Fatal("decode changed number")
		}
		result, err := spec.Bind(object, serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		_, integer := value.AsInteger()
		if result.Valid() != integer {
			t.Fatal("integer field coerced a number")
		}
	}
}

func TestNumberGrammarAndResourceLimits(t *testing.T) {
	for _, raw := range []string{"", "+1", "01", "-01", ".1", "1.", "1e", "1e+", "1 2", "NaN", "Infinity", "-Infinity", " 1", "1\n", "١", "1,2", "1\x00"} {
		if _, err := serializers.Number(raw); err == nil {
			t.Fatalf("accepted malformed token %q", raw)
		}
	}
	for _, raw := range []string{"1.5", "123", "1e9"} {
		value, _ := serializers.Number(raw)
		encoded, err := serializers.Encode(value, serializers.Limits{MaxNumberBytes: 2})
		if len(encoded) != 0 || !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
			t.Fatal("partial numeric encoding escaped limit")
		}
	}
	for _, size := range []int{-1, 4097} {
		if _, err := serializers.DecodeObject([]byte(`{}`), serializers.Limits{MaxNumberBytes: size}); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
			t.Fatal("invalid number limit accepted")
		}
	}
	if _, err := serializers.Number(strings.Repeat("1", 4097)); !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatal("number hard limit bypassed")
	}
	for _, document := range []string{`{"x":1.5,"x":2}`, `{"x":1.5} {}`, `{"x":1.5,"bad":"\u0000"}`, `{"x":1.5,"bad":"\ud800"}`} {
		if _, err := serializers.DecodeObject([]byte(document), serializers.Limits{}); err == nil {
			t.Fatal("exact numbers bypassed JSON security boundary")
		}
	}
}
