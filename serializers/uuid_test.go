package serializers_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/uuidtest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
)

func uuidSpec(t *testing.T, defaulted bool) serializers.Spec {
	t.Helper()
	options := []serializers.FieldOption{serializers.WithNullable(), serializers.WithRequired(false)}
	if defaulted {
		options = append(options, serializers.WithDefault(serializers.UUID(uuid.UUID{})))
	}
	field, err := serializers.UUIDField("reference", options...)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}
func checkUUIDResult(t *testing.T, result serializers.Result, want uuidtest.Result) {
	t.Helper()
	codes := map[string][]string{}
	for _, v := range result.Errors().All() {
		codes[string(v.Field())] = append(codes[string(v.Field())], string(v.Code()))
	}
	if result.Valid() != want.Valid || !reflect.DeepEqual(codes, want.Errors) {
		t.Fatalf("UUID errors=%v want %v", codes, want.Errors)
	}
	value, present := result.Values().Get("reference")
	expected, exists := want.Validated["reference"]
	if present != exists {
		t.Fatal("UUID omitted/null/default presence changed")
	}
	if !exists {
		// DRF .data can render an omitted optional field as null even though
		// validated_data has no member. Bind exposes input presence; complete
		// response fields belong to ModelEncoder, not this validation result.
		if want.Valid && want.Rendered != `{}` && want.Rendered != `{"reference":null}` {
			t.Fatal("unexpected independent omission representation")
		}
		return
	}
	if exists {
		if expected == nil {
			if !value.IsNull() {
				t.Fatal("explicit UUID NULL changed")
			}
		} else if got, ok := value.AsUUID(); !ok || got != expected.UUID(t) {
			t.Fatal("UUID validated bytes changed")
		}
	}
	if want.Valid {
		members := []serializers.Member{}
		if present {
			members = append(members, serializers.MemberOf("reference", value))
		}
		object, err := serializers.NewObject(members...)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := serializers.Encode(object.Value(), serializers.Limits{})
		if err != nil || string(wire) != want.Rendered {
			t.Fatalf("UUID response %s want %s: %v", wire, want.Rendered, err)
		}
	}
}
func TestUUIDSerializersAgainstPinnedDRF(t *testing.T) {
	direct, pythonBytes, nul := 0, 0, 0
	for index, observed := range uuidtest.Load(t).Serializer {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			members := []serializers.Member{}
			for name, input := range observed.Input {
				var value serializers.Value
				switch input.Type {
				case "UUID":
					var expected uuidtest.Value
					if err := json.Unmarshal(input.Value, &expected); err != nil {
						t.Fatal(err)
					}
					value = serializers.UUID(expected.UUID(t))
				case "bytes":
					// The closed Go value/JSON API has no Python bytes kind. These
					// observations are an explicit unsupported input selector.
					if observed.Valid || !reflect.DeepEqual(observed.Errors, map[string][]string{"reference": {"invalid"}}) {
						t.Fatal("Python bytes selector changed")
					}
					pythonBytes++
					return
				default:
					object, err := serializers.DecodeObject([]byte(`{"reference":`+string(input.Value)+`}`), serializers.Limits{})
					var raw string
					_ = json.Unmarshal(input.Value, &raw)
					if input.Type == "str" && strings.ContainsRune(raw, 0) {
						if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidDocument}) {
							t.Fatal("NUL document boundary changed", err)
						}
						nul++
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					value, _ = object.Get("reference")
				}
				members = append(members, serializers.MemberOf(name, value))
			}
			object, err := serializers.NewObject(members...)
			if err != nil {
				t.Fatal(err)
			}
			mode := serializers.ModeFull
			if observed.Partial {
				mode = serializers.ModePartial
			}
			result, err := uuidSpec(t, observed.Serializer == "default").Bind(object, mode)
			if err != nil {
				t.Fatal(err)
			}
			checkUUIDResult(t, result, observed.Result)
			direct++
		})
	}
	if direct != 240 || pythonBytes != 8 || nul != 4 {
		t.Fatal("UUID coverage changed", direct, pythonBytes, nul)
	}
}
func TestUUIDJSONIntegerTokensAndTypedFloatRejection(t *testing.T) {
	for _, row := range uuidtest.Load(t).JSONNumbers {
		object, err := serializers.DecodeObject([]byte(`{"reference":`+row.Raw+`}`), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		result, err := uuidSpec(t, false).Bind(object, serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		checkUUIDResult(t, result, row.Result)
	}
	for _, value := range []serializers.Value{serializers.Float(0), serializers.Float(1), serializers.Float(1.5)} {
		object, err := serializers.NewObject(serializers.MemberOf("reference", value))
		if err != nil {
			t.Fatal(err)
		}
		result, err := uuidSpec(t, false).Bind(object, serializers.ModeFull)
		if err != nil || result.Valid() {
			t.Fatal("typed float became integer UUID", err)
		}
	}
}
func TestUUIDModelEncoderDefaultsAndOwnedValues(t *testing.T) {
	built, err := schema.Build(schema.Definition{AppLabel: "refs", Models: []schema.Model{{Name: "record", GoName: "Record", Fields: []schema.Field{schema.UUIDField("reference", "Reference", schema.Nullable(), schema.Default(uuid.UUID{}))}}}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := built.Models[0]
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "reference"})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, func(value *uuid.UUID, field ir.Field) (query.Value, bool) {
		field.Default.UUID = "changed"
		if value == nil {
			return query.Null(), true
		}
		return query.UUID(*value), true
	})
	if err != nil {
		t.Fatal(err)
	}
	value := uuid.UUID{0: 0xff, 15: 1}
	want := value
	encoded, err := encoder.Encode(&value)
	if err != nil {
		t.Fatal(err)
	}
	value[0] = 0
	wire, err := serializers.Encode(encoded, serializers.Limits{})
	if err != nil || string(wire) != `{"reference":"`+want.String()+`"}` {
		t.Fatal("UUID encoded value aliases caller", err)
	}
	if metadata.Fields[1].Default.UUID != (uuid.UUID{}).String() {
		t.Fatal("encoder mutated UUID metadata")
	}
	null, err := encoder.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if wire, err := serializers.Encode(null, serializers.Limits{}); err != nil || string(wire) != `{"reference":null}` {
		t.Fatal("complete UUID model response omitted null", err)
	}
	for _, options := range [][]serializers.FieldOption{{serializers.WithMaxLength(36)}, {serializers.WithDefault(serializers.String(want.String()))}, {serializers.WithAllowEmpty()}} {
		if _, err := serializers.UUIDField("reference", options...); err == nil {
			t.Fatal("string-only UUID configuration accepted")
		}
	}
}
