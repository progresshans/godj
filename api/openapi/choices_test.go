package openapi_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func TestChoiceRequestEnumIncludesNullAndBlankAndResponseKeepsStoredDomain(t *testing.T) {
	status, err := serializers.StringField("status", serializers.WithNullable(), serializers.WithAllowEmpty(), serializers.WithChoices(
		serializers.Choice{Value: serializers.String("open"), Label: "<Open>"}, serializers.Choice{Value: serializers.String("closed"), Label: "Closed"}))
	if err != nil {
		t.Fatal(err)
	}
	priority, err := serializers.IntegerField("priority", serializers.WithDefault(serializers.Integer(99)), serializers.WithNullable(), serializers.WithChoices(
		serializers.Choice{Value: serializers.Integer(0), Label: "Normal"}, serializers.Choice{Value: serializers.Integer(-1), Label: "Low"}))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{status, priority})
	if err != nil {
		t.Fatal(err)
	}
	request, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, enum string }{{"status", `["open","closed",""]`}, {"priority", `[0,-1]`}} {
		property := schemaTestProperty(t, request, test.name)
		branches := schemaTestList(t, property, "anyOf")
		if len(branches) != 2 || schemaTestString(t, schemaTestValueObject(t, branches[1]), "type") != "null" {
			t.Fatal("choice enum lost independent null branch")
		}
		value, found := schemaTestValueObject(t, branches[0]).Get("enum")
		if !found || !bytes.Equal(schemaTestEncodeValue(t, value), []byte(test.enum)) {
			t.Fatalf("%s enum does not describe accepted input", test.name)
		}
		if _, found := property.Get("x-godj-choices"); !found {
			t.Fatal("choice labels absent")
		}
	}
	if _, found := schemaTestProperty(t, request, "status").Get("minLength"); found {
		t.Fatal("blank choice policy contradicted by minLength")
	}
	if value := schemaTestInteger(t, schemaTestProperty(t, request, "priority"), "x-godj-omission-default"); value != 99 {
		t.Fatal("server omission default disappeared")
	}
	if _, found := schemaTestProperty(t, request, "priority").Get("default"); found {
		t.Fatal("client was instructed to submit an invalid enum default")
	}
	partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := schemaTestProperty(t, partial, "priority").Get("default"); found {
		t.Fatal("partial input acquired default")
	}
	if _, found := schemaTestProperty(t, partial, "priority").Get("x-godj-omission-default"); found {
		t.Fatal("partial input acquired omission default")
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"status", "priority"} {
		property := schemaTestProperty(t, response, name)
		if _, found := property.Get("enum"); found {
			t.Fatal("response excludes existing out-of-choice rows")
		}
		branches := schemaTestList(t, property, "anyOf")
		if _, found := schemaTestValueObject(t, branches[0]).Get("enum"); found {
			t.Fatal("response branch excludes existing out-of-choice rows")
		}
		if _, found := property.Get("x-godj-choices"); !found {
			t.Fatal("response lost display metadata")
		}
	}
}
