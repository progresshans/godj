package openapi_test

import (
	"regexp"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/serializers"
)

func TestDecimalSchemaDescribesExactFixedScaleNullAndDefault(t *testing.T) {
	field, err := serializers.DecimalField("cost", 12, 2, serializers.WithNullable(), serializers.WithDefault(serializers.Decimal(decimal.Decimal{Coefficient: "-0"})))
	if err != nil {
		t.Fatal(err)
	}
	spec := schemaTestSpec(t, field)
	full, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	property := schemaTestProperty(t, full, "cost")
	if schemaTestString(t, property, "default") != "-0.00" || len(schemaTestRequired(t, full)) != 0 {
		t.Fatal("decimal default or optional presence changed")
	}
	branches := schemaTestList(t, property, "anyOf")
	if len(branches) != 2 || schemaTestString(t, schemaTestValueObject(t, branches[1]), "type") != "null" {
		t.Fatal("decimal null alternative missing")
	}
	value := schemaTestValueObject(t, branches[0])
	if schemaTestString(t, value, "type") != "string" || schemaTestInteger(t, value, "minLength") != 4 || schemaTestInteger(t, value, "maxLength") != 14 {
		t.Fatal("decimal declared string bounds changed")
	}
	pattern := regexp.MustCompile(schemaTestString(t, value, "pattern"))
	for _, text := range []string{"0.00", "-0.00", "1.50", "9999999999.99", "-9999999999.99"} {
		if !pattern.MatchString(text) {
			t.Fatalf("valid decimal output %s rejected", text)
		}
	}
	for _, text := range []string{"1.5", "1.500", "01.50", "+1.50", "1e2", "NaN", "10000000000.00", "1.00\n"} {
		if pattern.MatchString(text) {
			t.Fatalf("noncanonical decimal output %q admitted", text)
		}
	}
	policy, _ := value.Get("x-godj-decimal")
	object, ok := policy.AsObject()
	if !ok || schemaTestInteger(t, object, "maxDigits") != 12 || schemaTestInteger(t, object, "decimalPlaces") != 2 || schemaTestString(t, object, "rounding") != "reject-excess-scale" {
		t.Fatal("decimal policy differs from metadata")
	}
	partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := schemaTestProperty(t, partial, "cost").Get("default"); exists {
		t.Fatal("PATCH defaulted omitted decimal")
	}
	response, err := openapi.ModelResponseSchema(spec)
	if err != nil {
		t.Fatal(err)
	}
	if names := schemaTestRequired(t, response); len(names) != 1 || names[0] != "cost" {
		t.Fatal("nullable decimal response became optional")
	}
	for _, precision := range [][2]int{{4, 4}, {4, 0}} {
		field, err := serializers.DecimalField("cost", precision[0], precision[1])
		if err != nil {
			t.Fatal(err)
		}
		schema, err := openapi.ModelResponseSchema(schemaTestSpec(t, field))
		if err != nil {
			t.Fatal(err)
		}
		property := schemaTestProperty(t, schema, "cost")
		pattern := regexp.MustCompile(schemaTestString(t, property, "pattern"))
		valid, invalid := "0.1234", "1.1234"
		if precision[1] == 0 {
			valid, invalid = "-9999", "1.0"
		}
		if !pattern.MatchString(valid) || pattern.MatchString(invalid) {
			t.Fatal("zero-whole or zero-scale boundary changed")
		}
	}
}
