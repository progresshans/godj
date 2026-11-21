package openapi_test

import (
	"testing"

	"github.com/progresshans/godj/api/openapi"
)

func TestArrayRangeDescribesAndValidatesCardinality(t *testing.T) {
	schema, err := openapi.ArrayRange(openapi.String(), 1, 40)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := schema.Value().AsObject()
	if !ok {
		t.Fatal("array schema is not an object")
	}
	for name, want := range map[string]int64{"minItems": 1, "maxItems": 40} {
		field, _ := value.Get(name)
		got, valid := field.AsInteger()
		if !valid || got != want {
			t.Fatal("array cardinality", name)
		}
	}
	for _, bounds := range [][2]int{{-1, 40}, {2, 1}} {
		if _, err := openapi.ArrayRange(openapi.String(), bounds[0], bounds[1]); err == nil {
			t.Fatal("invalid bounds accepted")
		}
	}
	if _, err := openapi.ArrayRange(openapi.Schema{}, 0, 1); err == nil {
		t.Fatal("zero item schema accepted")
	}
}
