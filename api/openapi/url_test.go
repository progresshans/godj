package openapi_test

import (
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func TestURLSchemaDescribesNormalizedInputAndUnvalidatedStoredOutput(t *testing.T) {
	field, err := serializers.URLField("address", serializers.WithMaxLength(200), serializers.WithAllowEmpty())
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
		t.Fatal("url wire type changed")
	}
	for _, key := range []string{"format", "maxLength", "minLength"} {
		if _, found := property.Get(key); found {
			t.Fatal("raw input assertion contradicts url trimming or optional blank", key)
		}
	}
	policy, found := property.Get("x-godj-url")
	if !found {
		t.Fatal("url grammar policy is missing")
	}
	if schemaTestString(t, schemaTestValueObject(t, policy), "validator") != "django-6.1" {
		t.Fatal("url validator authority changed")
	}
	normalization, found := property.Get("x-godj-normalization")
	if !found {
		t.Fatal("url normalization is missing")
	}
	if schemaTestString(t, schemaTestValueObject(t, normalization), "whitespaceProfile") != "python-unicode-16" {
		t.Fatal("url whitespace profile is ambiguous")
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	output := schemaTestProperty(t, response, "address")
	for _, key := range []string{"format", "x-godj-url", "x-godj-normalization"} {
		if _, found := output.Get(key); found {
			t.Fatal("response schema re-runs url input policy", key)
		}
	}
	if schemaTestInteger(t, output, "maxLength") != 200 {
		t.Fatal("response dropped stored string length")
	}
}

func TestURLDefaultsDoNotInstructClientsToSubmitUnvalidatedValues(t *testing.T) {
	field, err := serializers.URLField("address", serializers.WithDefault(serializers.String("  legacy  ")))
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
