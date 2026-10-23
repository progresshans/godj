package serializers_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/floattest"
	"github.com/progresshans/godj/serializers"
)

func floatSpec(t *testing.T, defaulted bool) serializers.Spec {
	t.Helper()
	options := []serializers.FieldOption{serializers.WithNullable(), serializers.WithRequired(false)}
	if defaulted {
		options = append(options, serializers.WithDefault(serializers.Float(1.5)))
	}
	field, err := serializers.FloatField("effort", options...)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
func checkFloatResult(t *testing.T, result serializers.Result, valid bool, expected map[string]*floattest.Number, errors_ map[string][]string, nonfinite bool) {
	t.Helper()
	if nonfinite {
		valid = false
		expected = map[string]*floattest.Number{}
		errors_ = map[string][]string{"effort": {"invalid"}}
	}
	codes := map[string][]string{}
	for _, v := range result.Errors().All() {
		codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
	}
	if result.Valid() != valid || !reflect.DeepEqual(codes, errors_) {
		t.Fatalf("errors=%v want=%v", codes, errors_)
	}
	got, present := result.Values().Get("effort")
	want, exists := expected["effort"]
	if present != exists {
		t.Fatal("validated presence or default differs")
	}
	if !exists {
		return
	}
	if want == nil {
		if !got.IsNull() {
			t.Fatal("NULL lost")
		}
		return
	}
	value, ok := got.AsFloat()
	if !ok || math.Float64bits(value) != math.Float64bits(want.Float(t)) {
		t.Fatalf("binary64 bits differ: %016x want %s", math.Float64bits(value), want.Bits)
	}
	encoded, err := serializers.Encode(got, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var restored float64
	if err = json.Unmarshal(encoded, &restored); err != nil || math.Float64bits(restored) != math.Float64bits(value) {
		t.Fatalf("JSON output changed bits: %s %v", encoded, err)
	}
}
func TestFloatSerializersAndTypedBoundariesAgainstPinnedDRF(t *testing.T) {
	direct, decimalProjection, typedNonfinite, decimalNonJSON, nul, strict := 0, 0, 0, 0, 0, 0
	for index, observation := range floattest.Load(t).Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec := floatSpec(t, observation.Serializer == "default")
			var members []serializers.Member
			for name, input := range observation.Input {
				var value serializers.Value
				switch input.Type {
				case "float":
					var number floattest.Number
					if err := json.Unmarshal(input.Value, &number); err != nil {
						t.Fatal(err)
					}
					native := number.Float(t)
					value = serializers.Float(native)
					if math.IsNaN(native) || math.IsInf(native, 0) {
						typedNonfinite++
						if !observation.Valid || observation.RenderException != "ValueError" {
							t.Fatal("typed nonfinite reference changed")
						}
						if _, err := serializers.NewObject(serializers.MemberOf(name, value)); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
							t.Fatal("typed nonfinite escaped constructor boundary")
						}
						return
					}
				case "Decimal":
					var number struct{ Decimal string }
					if err := json.Unmarshal(input.Value, &number); err != nil {
						t.Fatal(err)
					}
					projected, err := serializers.Number(number.Decimal)
					if number.Decimal == "NaN" || number.Decimal == "Infinity" {
						decimalNonJSON++
						if !observation.Valid || observation.RenderException != "ValueError" || !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
							t.Fatal("non-JSON Decimal boundary changed")
						}
						return
					}
					// Go has no Python Decimal object. Compare this explicitly named transport
					// projection, preserving the finite decimal token before Float conversion.
					decimalProjection++
					if number.Decimal != "0.1" || err != nil {
						t.Fatal("Decimal projection selector changed")
					}
					value = projected
				default:
					wire := []byte(`{"effort":` + string(input.Value) + `}`)
					object, err := serializers.DecodeObject(wire, serializers.Limits{})
					var raw string
					_ = json.Unmarshal(input.Value, &raw)
					if input.Type == "str" && strings.ContainsRune(raw, 0) {
						nul++
						if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidDocument}) {
							t.Fatal("global NUL boundary changed")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					value, _ = object.Get(name)
				}
				members = append(members, serializers.MemberOf(name, value))
			}
			if input, ok := observation.Input["effort"]; !ok || input.Type != "Decimal" {
				direct++
			}
			object, err := serializers.NewObject(members...)
			if err != nil {
				t.Fatal(err)
			}
			mode := serializers.ModeFull
			if observation.Partial {
				mode = serializers.ModePartial
			}
			result, err := spec.Bind(object, mode)
			if err != nil {
				t.Fatal(err)
			}
			nonfinite := observation.RenderException != ""
			if nonfinite {
				strict++
				if !observation.Valid || observation.RenderException != "ValueError" {
					t.Fatal("finite-admission selector changed")
				}
			}
			checkFloatResult(t, result, observation.Valid, observation.Validated, observation.Errors, nonfinite)
		})
	}
	if direct != 332 || decimalProjection != 4 || typedNonfinite != 12 || decimalNonJSON != 8 || nul != 4 || strict != 52 {
		t.Fatalf("coverage buckets changed: direct=%d decimal=%d typed=%d nonJSON=%d NUL=%d finite-policy=%d", direct, decimalProjection, typedNonfinite, decimalNonJSON, nul, strict)
	}
}
func TestFloatJSONNumbersAgainstPinnedDRF(t *testing.T) {
	spec := floatSpec(t, false)
	strict := 0
	for _, observation := range floattest.Load(t).JSONNumbers {
		object, err := serializers.DecodeObject([]byte(`{"effort":`+observation.Raw+`}`), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := spec.Bind(object, serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		nonfinite := observation.RenderException != ""
		if nonfinite {
			strict++
		}
		checkFloatResult(t, result, observation.Valid, observation.Validated, observation.Errors, nonfinite)
	}
	if strict != 2 {
		t.Fatal("numeric non-finite selector changed")
	}
	for _, number := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
		if _, err := serializers.Encode(serializers.Float(number), serializers.Limits{}); err == nil {
			t.Fatal("nonfinite model output rendered")
		}
		if _, err := serializers.FloatField("effort", serializers.WithDefault(serializers.Float(number))); err == nil {
			t.Fatal("nonfinite default accepted")
		}
	}
	if _, err := serializers.Encode(serializers.Float(math.MaxFloat64), serializers.Limits{MaxNumberBytes: 4}); err == nil {
		t.Fatal("Float output bypassed number byte budget")
	}
}
