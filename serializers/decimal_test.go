package serializers_test

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimaltest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func decimalSpec(t *testing.T, digits, places int, defaulted bool) serializers.Spec {
	t.Helper()
	options := []serializers.FieldOption{serializers.WithNullable(), serializers.WithRequired(false)}
	if defaulted {
		options = append(options, serializers.WithDefault(serializers.Decimal(decimal.Decimal{Coefficient: "15", Exponent: -1})))
	}
	field, err := serializers.DecimalField("cost", digits, places, options...)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
func checkDecimalResult(t *testing.T, result serializers.Result, want decimaltest.Result) {
	t.Helper()
	codes := map[string][]string{}
	for _, v := range result.Errors().All() {
		codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
	}
	if result.Valid() != want.Valid || !reflect.DeepEqual(codes, want.Errors) {
		t.Fatalf("errors %v want %v", codes, want.Errors)
	}
	value, present := result.Values().Get("cost")
	number, exists := want.Validated["cost"]
	if present != exists {
		t.Fatal("validated presence differs")
	}
	if !exists {
		return
	}
	if number == nil {
		if !value.IsNull() {
			t.Fatal("null lost")
		}
		return
	}
	actual, ok := value.AsDecimal()
	if !ok || actual != number.Decimal(t) {
		t.Fatal("decimal coefficient/exponent or zero sign differs")
	}
	encoded, err := serializers.Encode(value, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err := json.Unmarshal(encoded, &text); err != nil || text != number.Text {
		t.Fatalf("fixed-scale string %s want %s", encoded, number.Text)
	}
}

func TestDecimalSerializerPinnedDRFProfilesAndTypedBoundaries(t *testing.T) {
	direct, decimalProjection, nonfinite, nul := 0, 0, 0, 0
	for index, observation := range decimaltest.Load(t).Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			spec := decimalSpec(t, 12, 2, observation.Serializer == "default")
			var members []serializers.Member
			for name, input := range observation.Input {
				var value serializers.Value
				switch input.Type {
				case "float":
					var source struct{ Bits string }
					if err := json.Unmarshal(input.Value, &source); err != nil {
						t.Fatal(err)
					}
					bits, err := strconv.ParseUint(source.Bits, 16, 64)
					if err != nil {
						t.Fatal(err)
					}
					number := math.Float64frombits(bits)
					value = serializers.Float(number)
					if math.IsNaN(number) || math.IsInf(number, 0) {
						nonfinite++
						if observation.Valid || !reflect.DeepEqual(observation.Errors, map[string][]string{"cost": {"invalid"}}) {
							t.Fatal("nonfinite reference selector changed")
						}
						if _, err := serializers.NewObject(serializers.MemberOf(name, value)); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
							t.Fatal("nonfinite constructor leaked value")
						}
						return
					}
				case "Decimal":
					// Python's mutable-scale object is an explicit lexical projection.
					// Go model Decimal intentionally owns a canonical numeric value.
					decimalProjection++
					var source decimaltest.Number
					if err := json.Unmarshal(input.Value, &source); err != nil {
						t.Fatal(err)
					}
					value = serializers.String(source.Text)
				default:
					object, err := serializers.DecodeObject([]byte(`{"cost":`+string(input.Value)+`}`), serializers.Limits{})
					var text string
					_ = json.Unmarshal(input.Value, &text)
					if input.Type == "str" && strings.ContainsRune(text, 0) {
						nul++
						if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidDocument}) || observation.Valid {
							t.Fatal("NUL rejection boundary changed")
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
			if input, ok := observation.Input["cost"]; !ok || input.Type != "Decimal" {
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
			checkDecimalResult(t, result, observation.Result)
		})
	}
	if direct != 324 || decimalProjection != 20 || nonfinite != 12 || nul != 4 {
		t.Fatalf("coverage direct=%d decimal-text=%d nonfinite=%d NUL=%d", direct, decimalProjection, nonfinite, nul)
	}
}

func TestDecimalExactJSONNumberAndPrecisionProfiles(t *testing.T) {
	ref := decimaltest.Load(t)
	for index, observation := range ref.JSONDecimalNumbers {
		t.Run("number_"+strconv.Itoa(index), func(t *testing.T) {
			object, err := serializers.DecodeObject([]byte(`{"cost":`+observation.Raw+`}`), serializers.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := decimalSpec(t, 12, 2, false).Bind(object, serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			checkDecimalResult(t, result, observation.Result)
		})
	}
	for index, observation := range ref.JSONPrecisionNumbers {
		t.Run("precise_"+strconv.Itoa(index), func(t *testing.T) {
			object, err := serializers.DecodeObject([]byte(`{"cost":`+observation.Raw+`}`), serializers.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := decimalSpec(t, 30, 12, false).Bind(object, serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			checkDecimalResult(t, result, observation.Lexical)
		})
	}
	bounded, unbounded := 0, 0
	for index, row := range ref.Precision {
		if row.MaxDigits == nil || row.DecimalPlaces == nil {
			if row.MaxDigits != nil || row.DecimalPlaces != nil {
				t.Fatal("unknown precision profile")
			}
			unbounded++
			continue
		}
		bounded++
		t.Run("precision_"+strconv.Itoa(index), func(t *testing.T) {
			object, err := serializers.NewObject(serializers.MemberOf("cost", serializers.String(row.Input)))
			if err != nil {
				t.Fatal(err)
			}
			result, err := decimalSpec(t, *row.MaxDigits, *row.DecimalPlaces, false).Bind(object, serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			codes := []string{}
			for _, v := range result.Errors().All() {
				codes = append(codes, string(v.Code()))
			}
			if !reflect.DeepEqual(codes, row.Serializer.Codes) {
				t.Fatalf("%q codes %v want %v", row.Input, codes, row.Serializer.Codes)
			}
			if row.Serializer.Value != nil {
				value, ok := result.Values().Get("cost")
				if !ok {
					t.Fatal("cleaned missing")
				}
				encoded, err := serializers.Encode(value, serializers.Limits{})
				if err != nil {
					t.Fatal(err)
				}
				var text string
				if json.Unmarshal(encoded, &text) != nil || text != row.Serializer.Output {
					t.Fatal("precision output changed")
				}
			}
		})
	}
	if bounded != 72 || unbounded != 18 {
		t.Fatal("precision profile coverage changed")
	}
}

func TestDecimalSerializerModelOutputDefaultsAndResourceLimits(t *testing.T) {
	model, err := schema.Build(schema.Definition{AppLabel: "costs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.DecimalField("cost", "Cost", 5, 2, schema.Nullable(), schema.Default(decimal.Decimal{Coefficient: "15", Exponent: -1}))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := model.Models[0]
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "cost"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(v decimal.Decimal, f ir.Field) (query.Value, bool) {
		f.Decimal.MaxDigits = 1000
		return query.Decimal(v), true
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []decimal.Decimal{{}, {Coefficient: "-0"}, {Coefficient: "15", Exponent: -1}} {
		output, err := encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := serializers.Encode(output, serializers.Limits{})
		fixed, _ := value.Fixed(2)
		expected, _ := json.Marshal(map[string]string{"cost": fixed})
		if err != nil || string(wire) != string(expected) {
			t.Fatalf("model output %s %v", wire, err)
		}
	}
	for _, value := range []decimal.Decimal{{Coefficient: "NaN"}, {Coefficient: "1", Exponent: 3}, {Coefficient: "1", Exponent: -3}} {
		if _, err := encoder.Encode(value); err == nil {
			t.Fatal("invalid model output accepted")
		}
	}
	if _, err := serializers.NewModelEncoder(decimalSpec(t, 6, 2, false), metadata, func(decimal.Decimal, ir.Field) (query.Value, bool) { return query.Null(), true }); err == nil {
		t.Fatal("encoder accepted precision drift")
	}
	empty, _ := serializers.NewObject()
	bound, err := spec.Bind(empty, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	value, present := bound.Values().Get("cost")
	encoded, err := serializers.Encode(value, serializers.Limits{})
	if !present || err != nil || string(encoded) != `"1.50"` {
		t.Fatal("default lost field scale")
	}
	if _, err := serializers.Encode(value, serializers.Limits{MaxStringBytes: 3}); !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatal("Decimal string escaped output budget")
	}
	for _, pair := range [][2]int{{0, 0}, {1001, 2}, {5, 6}, {5, -1}} {
		if _, err := serializers.DecimalField("cost", pair[0], pair[1]); err == nil {
			t.Fatal("invalid precision accepted")
		}
	}
	for _, value := range []serializers.Value{serializers.String("1.5"), serializers.Float(1.5), serializers.Decimal(decimal.Decimal{Coefficient: "NaN"}), serializers.Decimal(decimal.Decimal{Coefficient: "1", Exponent: -3})} {
		if _, err := serializers.DecimalField("cost", 5, 2, serializers.WithDefault(value)); err == nil {
			t.Fatal("invalid Decimal default accepted")
		}
	}
}
