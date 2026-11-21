package openapi_test

import (
	"regexp"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/internal/binarytest"
	"github.com/progresshans/godj/serializers"
)

func TestBinarySchemaConstrainsEncodedStringsAndRetainsStoredOutput(t *testing.T) {
	field, err := serializers.BinaryField("payload", serializers.WithNullable(), serializers.WithMaxLength(4), serializers.WithDefault(serializers.Binary(binaryvalue.Value{})))
	if err != nil {
		t.Fatal(err)
	}
	spec := schemaTestSpec(t, field)
	request, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, request, "payload")
	if schemaTestString(t, property, "default") != "" || len(schemaTestRequired(t, request)) != 0 {
		t.Fatal("empty bytes are a present omission default")
	}
	branches := schemaTestList(t, property, "anyOf")
	if len(branches) != 2 || schemaTestString(t, schemaTestValueObject(t, branches[1]), "type") != "null" {
		t.Fatal("binary null branch missing")
	}
	encoded := schemaTestValueObject(t, branches[0])
	if schemaTestString(t, encoded, "type") != "string" || schemaTestString(t, encoded, "contentEncoding") != "base64" || schemaTestInteger(t, encoded, "maxLength") != 8 {
		t.Fatal("encoded string constraints lost")
	}
	for _, key := range []string{"maxLength", "minLength", "pattern", "contentEncoding"} {
		if _, exists := property.Get(key); exists {
			t.Fatal("binary assertion escaped the string branch", key)
		}
	}
	if _, exists := encoded.Get("format"); exists {
		t.Fatal("encoded constraints cannot be applied to a client byte slice")
	}
	pattern := regexp.MustCompile(schemaTestString(t, encoded, "pattern"))
	for _, text := range []string{"", "Zg==", "Zh==", "AP9hgA==", "YWJj", "YWI="} {
		if !pattern.MatchString(text) {
			t.Fatal("binary input excluded", text)
		}
	}
	for _, text := range []string{"Zg", "Zg===", " Zg==", "Zg==\n", "YQ==YQ==", "AP8_", "%%%%"} {
		if pattern.MatchString(text) {
			t.Fatal("malformed base64 matched", text)
		}
	}
	policy, _ := encoded.Get("x-godj-binary")
	if schemaTestInteger(t, schemaTestValueObject(t, policy), "maximumDecodedBytes") != 4 {
		t.Fatal("decoded length policy missing")
	}
	partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := schemaTestProperty(t, partial, "payload").Get("default"); exists {
		t.Fatal("partial omission invented empty bytes")
	}
	output, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	response := schemaTestValueObject(t, schemaTestList(t, schemaTestProperty(t, output, "payload"), "anyOf")[0])
	if schemaTestInteger(t, response, "maxLength") != int64(binaryvalue.MaxEncodedBytes) || len(schemaTestRequired(t, output)) != 1 {
		t.Fatal("output applied current input length or made nullable field optional")
	}
	if _, err := serializers.BinaryField("payload", serializers.WithMaxLength(1), serializers.WithDefault(serializers.Binary(binaryvalue.Value{Data: "legacy"}))); err == nil {
		t.Fatal("invalid declared binary default accepted")
	}
}

func TestNonEditableModelFieldsAreReadOnlyInOpenAPI(t *testing.T) {
	model := binarytest.Model(t, "hidden")
	spec, err := serializers.FromModel(model, serializers.ModelField{Name: "title"}, serializers.ModelField{Name: "payload"}, serializers.ModelField{Name: "internal_note"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	properties, _ := input.Value().AsObject()
	value, _ := properties.Get("properties")
	object, _ := value.AsObject()
	for _, name := range []string{"payload", "internal_note"} {
		if _, exists := object.Get(name); exists {
			t.Fatal("non-editable field entered request", name)
		}
	}
	output, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"payload", "internal_note"} {
		flag, exists := schemaTestProperty(t, output, name).Get("readOnly")
		readonly, valid := flag.AsBoolean()
		if !exists || !valid || !readonly {
			t.Fatal("model policy lost in output", name)
		}
	}
}
