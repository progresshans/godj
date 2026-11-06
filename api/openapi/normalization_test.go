package openapi_test

import (
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func TestRequestNormalizationDescribesPostTransformConstraintsAndRawBytes(t *testing.T) {
	field := schemaTestField(t, serializers.FieldString, "name", serializers.WithNullable(), serializers.WithStringNormalization(serializers.StringUnicode16NFKC), serializers.WithMaxInputBytes(12), serializers.WithMaxLength(4), serializers.WithDefault(serializers.String("old")), serializers.WithChoices(serializers.Choice{Value: serializers.String("Fred"), Label: "Canonical"}))
	spec := schemaTestSpec(t, field)
	for _, mode := range []serializers.Mode{serializers.ModeFull, serializers.ModePartial} {
		name := "full"
		if mode == serializers.ModePartial {
			name = "partial"
		}
		t.Run(name, func(t *testing.T) {
			schema, err := openapi.RequestSchema(spec, mode)
			if err != nil {
				t.Fatal(err)
			}
			property := schemaTestProperty(t, schema, "name")
			for _, key := range []string{"minLength", "maxLength", "enum", "default"} {
				if _, found := property.Get(key); found {
					t.Fatal("raw assertion misdescribes transformed input", key)
				}
			}
			branches := schemaTestList(t, property, "anyOf")
			if len(branches) != 2 {
				t.Fatal("nullable type lost")
			}
			if _, found := schemaTestValueObject(t, branches[0]).Get("enum"); found {
				t.Fatal("choice aliases excluded by raw enum")
			}
			policy := schemaTestValueObject(t, func() serializers.Value { value, _ := property.Get("x-godj-normalization"); return value }())
			if schemaTestString(t, policy, "transform") != "unicode16_nfkc" || schemaTestBoolean(t, policy, "trimWhitespace") || schemaTestBoolean(t, policy, "allowEmptyAfterNormalization") || schemaTestInteger(t, policy, "maxInputBytes") != 12 || schemaTestInteger(t, policy, "maxLengthAfterNormalization") != 4 {
				t.Fatal("normalization policy lost exact runtime semantics")
			}
			choices := schemaTestList(t, policy, "enumAfterNormalization")
			if len(choices) != 1 {
				t.Fatal("normalized enum lost")
			}
			if value, _ := choices[0].AsString(); value != "Fred" {
				t.Fatal("canonical enum changed")
			}
			_, defaultPresent := property.Get("x-godj-omission-default")
			if defaultPresent != (mode == serializers.ModeFull) {
				t.Fatal("default mode semantics changed")
			}
		})
	}
	for _, document := range []string{`{"name":"Ｆｒｅｄ"}`, `{"name":null}`, `{}`} {
		if !schemaTestBind(t, spec, document, serializers.ModeFull).Valid() {
			t.Fatal("documented normalized alias/default rejected")
		}
	}
	for _, document := range []string{`{"name":" Ｆｒｅｄ"}`, `{"name":"Other"}`} {
		if schemaTestBind(t, spec, document, serializers.ModeFull).Valid() {
			t.Fatal("raw byte/normalized choice constraint lost")
		}
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, response, "name")
	for _, key := range []string{"x-godj-normalization", "enum", "default", "x-godj-omission-default"} {
		if _, found := property.Get(key); found {
			t.Fatal("input policy imposed on stored output", key)
		}
	}
	branches := schemaTestList(t, property, "anyOf")
	if schemaTestInteger(t, schemaTestValueObject(t, branches[0]), "maxLength") != 4 {
		t.Fatal("stored output length omitted")
	}
}

func TestRequestStringPolicyKeepsExactBytesEmailGrammarAndEmptyChoices(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy serializers.StringNormalization
		trim   bool
	}{
		{"exact", serializers.StringExact, false}, {"nfkc", serializers.StringUnicode16NFKC, true}, {"domain", serializers.StringUnicode16EmailDomain, false}, {"combined", serializers.StringUnicode16NFKCEmailDomain, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			field, err := serializers.EmailField("email", serializers.WithStringNormalization(test.policy), serializers.WithMaxInputBytes(1024), serializers.WithTrimWhitespace(test.trim))
			if err != nil {
				t.Fatal(err)
			}
			schema, err := openapi.RequestSchema(schemaTestSpec(t, field), serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			property := schemaTestProperty(t, schema, "email")
			policy, _ := property.Get("x-godj-normalization")
			actual := schemaTestValueObject(t, policy)
			want := string(test.policy)
			if want == "" {
				want = "exact"
			}
			if schemaTestString(t, actual, "transform") != want || schemaTestBoolean(t, actual, "trimWhitespace") != test.trim || schemaTestInteger(t, actual, "maxInputBytes") != 1024 {
				t.Fatal("input byte/transform policy differs")
			}
			email, _ := property.Get("x-godj-email")
			if !schemaTestBoolean(t, schemaTestValueObject(t, email), "afterNormalization") {
				t.Fatal("email grammar evaluated before transform")
			}
		})
	}
	field := schemaTestField(t, serializers.FieldString, "value", serializers.WithStringNormalization(serializers.StringUnicode16NFKC), serializers.WithAllowEmpty(), serializers.WithChoices(serializers.Choice{Value: serializers.String("A")}))
	schema, err := openapi.RequestSchema(schemaTestSpec(t, field), serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := schemaTestProperty(t, schema, "value").Get("x-godj-normalization")
	policy := schemaTestValueObject(t, value)
	if values := schemaTestList(t, policy, "enumAfterNormalization"); len(values) != 2 || !schemaTestBoolean(t, policy, "allowEmptyAfterNormalization") {
		t.Fatal("explicit empty policy disappeared")
	}
}
