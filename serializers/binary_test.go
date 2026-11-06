package serializers_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/internal/binarytest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/serializers"
)

func TestBinaryJSONInputsAgainstPinnedDRF(t *testing.T) {
	reference, inputs := binarytest.Load(t, "sqlite"), binarytest.Inputs(t)
	for _, name := range []string{"required", "short", "blank", "nullable", "nullable_required", "hidden", "default"} {
		t.Run(name, func(t *testing.T) {
			profile := reference.Profiles[name]
			options := []serializers.FieldOption{serializers.WithRequired(profile.SerializerRequired)}
			if name == "nullable" || name == "nullable_required" || name == "hidden" {
				options = append(options, serializers.WithNullable())
			}
			if profile.MaxLength != nil {
				options = append(options, serializers.WithMaxLength(*profile.MaxLength))
			}
			if profile.SerializerReadOnly {
				options = append(options, serializers.WithReadOnly())
			}
			field, err := serializers.BinaryField("payload", options...)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := serializers.NewSpec([]serializers.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range inputs {
				t.Run(input.Name, func(t *testing.T) {
					raw := []byte(`{}`)
					if input.Present {
						raw = append(append([]byte(`{"payload":`), input.Value...), '}')
					}
					object, err := spec.DecodeObject(raw, serializers.Limits{})
					wanted := profile.Cases[input.Name].Serializer
					if input.Name == "nul" {
						if !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidDocument}) {
							t.Fatal("binary bypassed the existing JSON NUL boundary", err)
						}
						if !profile.SerializerReadOnly && !slices.Equal(wanted.Errors["payload"], []string{"invalid"}) {
							t.Fatal("native NUL witness changed")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					result, err := spec.Bind(object, serializers.ModeFull)
					if err != nil {
						t.Fatal(err)
					}
					codes := []string{}
					for _, failure := range result.Errors().All() {
						codes = append(codes, string(failure.Code()))
					}
					wantCodes := wanted.Errors["payload"]
					var stringInput *string
					foreignType := input.Present && json.Unmarshal(input.Value, &stringInput) != nil
					if profile.SerializerReadOnly && input.Present {
						// GoDj rejects submitted server fields; DRF ignores them.
						if !wanted.Valid {
							t.Fatal("native read-only witness changed")
						}
						wantCodes = []string{string(serializers.CodeReadOnly)}
					} else if foreignType {
						// Python objects and TypeError are outside the closed binary
						// JSON input domain. They become an ordinary invalid error.
						wantCodes = []string{"invalid"}
					}
					if !slices.Equal(codes, wantCodes) || result.Valid() != (len(wantCodes) == 0) {
						t.Fatalf("codes=%v want=%v", codes, wantCodes)
					}
					got, present := result.Values().Get("payload")
					if len(wantCodes) > 0 || !input.Present || profile.SerializerReadOnly {
						if present {
							t.Fatal("omitted or rejected binary input published a value")
						}
						return
					}
					var values map[string]binarytest.Value
					if err := json.Unmarshal(wanted.Values.Value, &values); err != nil {
						t.Fatal(err)
					}
					want := values["payload"].Binary(t)
					if want == nil {
						if !present || !got.IsNull() {
							t.Fatal("binary null lost")
						}
						return
					}
					data, ok := got.AsBinary()
					if !present || !ok || data != *want {
						t.Fatal("base64 input differs from native")
					}
					encoded, err := serializers.Encode(got, serializers.Limits{})
					var output string
					if err != nil || json.Unmarshal(encoded, &output) != nil || output != want.Base64() {
						t.Fatal("binary output is not canonical base64", err)
					}
				})
			}
		})
	}
}

func TestBinaryModelJSONPolicyDefaultsAndLegacyOutput(t *testing.T) {
	model := binarytest.Model(t, "hidden")
	spec, err := serializers.FromModel(model, serializers.ModelField{Name: "title"}, serializers.ModelField{Name: "payload"}, serializers.ModelField{Name: "internal_note"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range spec.Fields() {
		if field.Name() != "title" && !field.ReadOnly() {
			t.Fatal("model non-editable policy did not reach JSON")
		}
	}
	object, err := spec.DecodeObject([]byte(`{"title":"valid","payload":"","internal_note":"attacker"}`), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := spec.Bind(object, serializers.ModeFull)
	if err != nil || bound.Valid() || len(bound.Errors().All()) != 2 {
		t.Fatal("read-only model fields were submitted", err)
	}
	for _, failure := range bound.Errors().All() {
		if failure.Code() != serializers.CodeReadOnly {
			t.Fatal("wrong server-field failure", failure.Code())
		}
	}
	// Server storage can contain values exceeding input max_length. Encoding
	// preserves them instead of imposing validation retroactively.
	encoder, err := serializers.NewModelEncoder(spec, model, func(_ int, field ir.Field) (query.Value, bool) {
		switch field.Name {
		case "title":
			return query.String("stored"), true
		case "payload":
			return query.Binary(binaryvalue.Value{Data: "longer than four"}), true
		case "internal_note":
			return query.String("server"), true
		}
		return query.Value{}, false
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := encoder.Encode(0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := serializers.Encode(value, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(encoded, &response); err != nil || response["payload"] != "bG9uZ2VyIHRoYW4gZm91cg==" {
		t.Fatal("legacy binary output changed", err)
	}
	unsafeField, err := serializers.BinaryField("payload", serializers.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	unsafe, err := serializers.NewSpec([]serializers.Field{unsafeField})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serializers.NewModelEncoder(unsafe, model, func(int, ir.Field) (query.Value, bool) { return query.Null(), true }); err == nil {
		t.Fatal("custom model encoder advertised writable server field")
	}
	defaulted := binarytest.Model(t, "default")
	defaults, err := serializers.FromModel(defaulted, serializers.ModelField{Name: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := serializers.NewObject()
	if err != nil {
		t.Fatal(err)
	}
	full, err := defaults.Bind(empty, serializers.ModeFull)
	if data, ok := full.Values().Binary("payload"); err != nil || !full.Valid() || !ok || data.Data != "def" {
		t.Fatal("model omission default changed", err)
	}
	partial, err := defaults.Bind(empty, serializers.ModePartial)
	if _, present := partial.Values().Get("payload"); err != nil || !partial.Valid() || present {
		t.Fatal("partial input invented binary default", err)
	}
}
