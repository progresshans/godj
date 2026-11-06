package serializers_test

import (
	"testing"

	"github.com/progresshans/godj/serializers"
)

func TestChoiceDefaultsAreAbsenceValuesAndInputRemainsStrict(t *testing.T) {
	choices := []serializers.Choice{{Value: serializers.Integer(0), Label: "Normal"}}
	option := serializers.WithChoices(choices...)
	choices[0].Value = serializers.Integer(99)
	field, err := serializers.IntegerField("value", option, serializers.WithDefault(serializers.Integer(99)), serializers.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	field.Choices()[0].Value = serializers.Integer(99)
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input, code string
		present     bool
		value       int64
		null        bool
	}{
		{`{}`, "", true, 99, false}, {`{"value":0}`, "", true, 0, false},
		{`{"value":99}`, "invalid_choice", false, 0, false},
		{`{"value":"0"}`, "type", false, 0, false}, {`{"value":false}`, "type", false, 0, false},
		{`{"value":null}`, "", true, 0, true},
	} {
		result, err := spec.Bind(decodeObject(t, test.input), serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		failures := result.Errors().All()
		if test.code == "" {
			if !result.Valid() {
				t.Fatal(failures)
			}
		} else if len(failures) != 1 || string(failures[0].Code()) != test.code {
			t.Fatalf("%s: %v", test.input, failures)
		}
		value, found := result.Values().Get("value")
		if found != test.present {
			t.Fatal("input presence changed")
		}
		if found {
			if value.IsNull() != test.null {
				t.Fatal("null changed")
			}
			if !test.null {
				if n, _ := value.AsInteger(); n != test.value {
					t.Fatal("integer changed")
				}
			}
		}
	}
	partial, err := spec.Bind(decodeObject(t, `{}`), serializers.ModePartial)
	if err != nil || !partial.Valid() || len(partial.Values().All()) != 0 {
		t.Fatal("partial omission applied a default")
	}
}

func TestChoiceStringPreservesWhitespaceAndExplicitEmptyPolicy(t *testing.T) {
	for _, allowBlank := range []bool{false, true} {
		options := []serializers.FieldOption{serializers.WithChoices(serializers.Choice{Value: serializers.String(" spaced "), Label: "Keep spaces"})}
		if allowBlank {
			options = append(options, serializers.WithAllowEmpty())
		}
		field, err := serializers.StringField("value", options...)
		if err != nil {
			t.Fatal(err)
		}
		if field.TrimWhitespace() {
			t.Fatal("choices enabled trimming")
		}
		spec, err := serializers.NewSpec([]serializers.Field{field})
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			json  string
			valid bool
		}{{`{"value":" spaced "}`, true}, {`{"value":"spaced"}`, false}, {`{"value":""}`, allowBlank}} {
			result, err := spec.Bind(decodeObject(t, test.json), serializers.ModeFull)
			if err != nil || result.Valid() != test.valid {
				t.Fatalf("%s: %v %v", test.json, result.Errors().All(), err)
			}
		}
	}
	empty, err := serializers.StringField("value", serializers.WithChoices(serializers.Choice{Value: serializers.String(""), Label: "Empty"}))
	if err != nil || !empty.AllowEmpty() {
		t.Fatalf("explicit empty choice rejected: %v", err)
	}
	for _, options := range [][]serializers.FieldOption{
		{serializers.WithChoices()},
		{serializers.WithChoices(serializers.Choice{Value: serializers.String("a")}), serializers.WithTrimWhitespace(true)},
		{serializers.WithChoices(serializers.Choice{Value: serializers.Integer(1)})},
		{serializers.WithChoices(serializers.Choice{Value: serializers.String("a")}, serializers.Choice{Value: serializers.String("a")})},
		{serializers.WithChoices(serializers.Choice{Value: serializers.String("long")}), serializers.WithMaxLength(2)},
		{serializers.WithChoices(serializers.Choice{Value: serializers.String("a"), Label: "bad\x00"})},
	} {
		if _, err := serializers.StringField("value", options...); err == nil {
			t.Fatal("invalid choices accepted")
		}
	}
}
