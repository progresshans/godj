package openapi_test

import (
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
)

func TestJSONSchemasSeparateInputNullabilityAndCompleteResponse(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		options := []serializers.FieldOption{}
		if nullable {
			options = append(options, serializers.WithNullable())
		}
		field, err := serializers.JSONField("payload", options...)
		if err != nil {
			t.Fatal(err)
		}
		spec := schemaTestSpec(t, field)
		request, err := openapi.RequestSchema(spec, serializers.ModeFull)
		if err != nil {
			t.Fatal(err)
		}
		input := schemaTestProperty(t, request, "payload")
		not, exists := input.Get("not")
		if exists == nullable {
			t.Fatal("JSON request nullability changed")
		}
		if exists && schemaTestString(t, schemaTestValueObject(t, not), "type") != "null" {
			t.Fatal("JSON request excludes the wrong type")
		}
		if _, exists := input.Get("type"); exists {
			t.Fatal("JSON field narrowed to a single JSON type")
		}
		if names := schemaTestRequired(t, request); len(names) != 1 || names[0] != "payload" {
			t.Fatal("JSON nullable field became optional")
		}
		partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
		if err != nil {
			t.Fatal(err)
		}
		if len(schemaTestRequired(t, partial)) != 0 {
			t.Fatal("PATCH JSON became required")
		}
		response, err := openapi.ModelResponseSchema(spec)
		if err != nil {
			t.Fatal(err)
		}
		output := schemaTestProperty(t, response, "payload")
		for _, name := range []string{"type", "not", "anyOf", "default"} {
			if _, exists := output.Get(name); exists {
				t.Fatal("JSON response excludes valid stored JSON or applies input default", name)
			}
		}
		if names := schemaTestRequired(t, response); len(names) != 1 || names[0] != "payload" {
			t.Fatal("complete JSON response became optional")
		}
		policy, _ := output.Get("x-godj-json")
		if schemaTestString(t, schemaTestValueObject(t, policy), "numbers") != "exact-token" {
			t.Fatal("JSON runtime policy lost exact numbers")
		}
	}
	field, err := serializers.JSONField("payload", serializers.WithDefault(serializers.JSON(jsonvalue.Null())))
	if err != nil {
		t.Fatal(err)
	}
	full, err := openapi.RequestSchema(schemaTestSpec(t, field), serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, full, "payload")
	if _, exists := property.Get("default"); exists {
		t.Fatal("nonnullable input advertised null as an accepted request default")
	}
	value, exists := property.Get("x-godj-default")
	document, ok := value.AsJSON()
	if !exists || !ok || document != jsonvalue.Null() {
		t.Fatal("model JSON null omission default disappeared")
	}
	partial, err := openapi.RequestSchema(schemaTestSpec(t, field), serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := schemaTestProperty(t, partial, "payload").Get("x-godj-default"); exists {
		t.Fatal("PATCH carries an omission default")
	}
}
