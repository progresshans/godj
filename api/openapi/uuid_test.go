package openapi_test

import (
	"regexp"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDSchemaDescribesCanonicalClientStringAndInputPolicy(t *testing.T) {
	field, err := serializers.UUIDField("reference", serializers.WithNullable(), serializers.WithDefault(serializers.UUID(uuid.UUID{})))
	if err != nil {
		t.Fatal(err)
	}
	spec := schemaTestSpec(t, field)
	full, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, full, "reference")
	if schemaTestString(t, property, "default") != (uuid.UUID{}).String() || len(schemaTestRequired(t, full)) != 0 {
		t.Fatal("UUID omission default changed")
	}
	branches := schemaTestList(t, property, "anyOf")
	if len(branches) != 2 || schemaTestString(t, schemaTestValueObject(t, branches[1]), "type") != "null" {
		t.Fatal("UUID NULL branch absent")
	}
	value := schemaTestValueObject(t, branches[0])
	if schemaTestString(t, value, "type") != "string" || schemaTestString(t, value, "format") != "uuid" || schemaTestInteger(t, value, "minLength") != 36 || schemaTestInteger(t, value, "maxLength") != 36 {
		t.Fatal("UUID client representation changed")
	}
	pattern := regexp.MustCompile(schemaTestString(t, value, "pattern"))
	for _, text := range []string{(uuid.UUID{}).String(), "12345678-9abc-4def-8123-456789abcdef", "ffffffff-ffff-ffff-ffff-ffffffffffff", "80000000-0000-0000-0000-000000000000"} {
		if !pattern.MatchString(text) {
			t.Fatal("UUID output schema excluded a 128-bit pattern", text)
		}
	}
	for _, text := range []string{"00000000000000000000000000000000", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", "urn:uuid:12345678-9abc-4def-8123-456789abcdef", "00000000-0000-0000-0000-000000000000\n"} {
		if pattern.MatchString(text) {
			t.Fatal("UUID output pattern accepted input-only alias", text)
		}
	}
	policy, _ := value.Get("x-godj-uuid")
	object, ok := policy.AsObject()
	if !ok || schemaTestInteger(t, object, "bits") != 128 || schemaTestString(t, object, "integerMaximum") != "340282366920938463463374607431768211455" || schemaTestString(t, object, "floatingToken") != "reject" {
		t.Fatal("UUID runtime input policy lost exact range or lexical distinction")
	}
	partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := schemaTestProperty(t, partial, "reference").Get("default"); exists {
		t.Fatal("PATCH UUID defaults an omitted field")
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	if names := schemaTestRequired(t, response); len(names) != 1 || names[0] != "reference" {
		t.Fatal("nullable UUID output became optional")
	}
}
