package openapi_test

import (
	"testing"

	"github.com/progresshans/godj/api/openapi"
)

func TestQuerySchemaDeclarations(t *testing.T) {
	for _, declaration := range []struct {
		min, max int64
		grammar  openapi.IntegerTextGrammar
		fallback *int64
	}{
		{2, 1, openapi.CanonicalDecimal, nil}, {0, 1, "unrecognized", nil}, {-1, 1, openapi.UnsignedDigits, nil},
		{1, 2, openapi.CanonicalDecimal, new(int64(0))},
	} {
		if _, err := openapi.QueryInteger(declaration.min, declaration.max, declaration.grammar, declaration.fallback); err == nil {
			t.Fatal("invalid query integer schema accepted", declaration)
		}
	}
	for _, declaration := range []struct {
		max      int
		empty    bool
		fallback *string
	}{
		{0, true, nil}, {2, true, new("éa")}, {2, false, new("")}, {2, true, new("\xff")}, {2, true, new("\x00")},
	} {
		if _, err := openapi.QueryString(declaration.max, declaration.empty, declaration.fallback); err == nil {
			t.Fatal("invalid query string schema accepted", declaration)
		}
	}
	fallback := int64(1)
	schema, err := openapi.QueryInteger(0, 2, openapi.CanonicalDecimal, &fallback)
	if err != nil {
		t.Fatal(err)
	}
	fallback = 2
	object, _ := schema.Value().AsObject()
	value, _ := object.Get("default")
	got, _ := value.AsInteger()
	if got != 1 {
		t.Fatal("schema retains mutable default")
	}
	for _, parameter := range []openapi.Parameter{
		{Name: "n", In: "path", Schema: schema}, {Name: "", In: "query", Schema: schema},
		{Name: "no?", In: "query", Schema: schema}, {Name: "n", In: "query", Schema: schema, Description: "\x00"},
		{Name: "n", In: "header", Schema: schema, AllowEmptyValue: true}, {Name: "n", In: "query"},
	} {
		if err := openapi.ValidateParameter(parameter); err == nil {
			t.Fatal("invalid parameter metadata accepted", parameter)
		}
	}
	if err := openapi.ValidateParameter(openapi.Parameter{Name: "n", In: "query", Schema: schema}); err != nil {
		t.Fatal(err)
	}
}
