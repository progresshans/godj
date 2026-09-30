package openapi_test

import (
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func TestSlugSchemaDescribesNormalizedInputAndUnvalidatedStoredOutput(t *testing.T) {
	field, err := serializers.SlugField("address", serializers.WithMaxLength(50), serializers.WithAllowEmpty(), serializers.WithAllowUnicode(true))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	request, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, request, "address")
	if schemaTestString(t, property, "type") != "string" {
		t.Fatal("slug wire type changed")
	}
	for _, key := range []string{"format", "maxLength", "minLength"} {
		if _, found := property.Get(key); found {
			t.Fatal("raw input assertion contradicts slug trimming or optional blank", key)
		}
	}
	policy, found := property.Get("x-godj-slug")
	if !found {
		t.Fatal("slug grammar policy is missing")
	}
	if schemaTestString(t, schemaTestValueObject(t, policy), "validator") != "django-6.1" {
		t.Fatal("slug validator authority changed")
	}
	slugPolicy := schemaTestValueObject(t, policy)
	unicodeValue, exists := slugPolicy.Get("allowUnicode")
	unicode, valid := unicodeValue.AsBoolean()
	if !exists || !valid || !unicode || schemaTestString(t, slugPolicy, "characterProfile") != "python-unicode-16-alphanumeric" {
		t.Fatal("slug Unicode grammar was not declared")
	}
	normalization, found := property.Get("x-godj-normalization")
	if !found {
		t.Fatal("slug normalization is missing")
	}
	if schemaTestString(t, schemaTestValueObject(t, normalization), "whitespaceProfile") != "python-unicode-16" {
		t.Fatal("slug whitespace profile is ambiguous")
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	output := schemaTestProperty(t, response, "address")
	for _, key := range []string{"format", "x-godj-slug", "x-godj-normalization"} {
		if _, found := output.Get(key); found {
			t.Fatal("response schema re-runs slug input policy", key)
		}
	}
	if schemaTestInteger(t, output, "maxLength") != 50 {
		t.Fatal("response dropped stored string length")
	}
}

func TestSlugDefaultsDoNotInstructClientsToSubmitUnvalidatedValues(t *testing.T) {
	field, err := serializers.SlugField("address", serializers.WithDefault(serializers.String("  legacy  ")))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	request, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, request, "address")
	if _, exists := property.Get("default"); exists {
		t.Fatal("client was instructed to send an unvalidated server default")
	}
	if schemaTestString(t, property, "x-godj-omission-default") != "  legacy  " {
		t.Fatal("server omission value changed")
	}
}
