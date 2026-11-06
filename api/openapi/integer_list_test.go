package openapi_test

import (
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func TestIntegerListSchemasDescribePresenceAndEveryExactKey(t *testing.T) {
	field, err := serializers.IntegerListField("labels", serializers.WithRequired(false))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []bool{false, true} {
		var schema openapi.Schema
		if output {
			schema, err = openapi.ModelResponseSchema(spec)
		} else {
			schema, err = openapi.RequestSchema(spec, serializers.ModeFull)
		}
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := serializers.Encode(schema.Value(), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Required   []string
			Properties map[string]struct {
				Type        string
				UniqueItems bool
				Items       struct {
					Type, Format     string
					Minimum, Maximum json.Number
				}
			}
		}
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		labels := document.Properties["labels"]
		if labels.Type != "array" || labels.UniqueItems || labels.Items.Type != "integer" || labels.Items.Format != "int64" || labels.Items.Minimum.String() != "-9223372036854775808" || labels.Items.Maximum.String() != "9223372036854775807" || len(document.Required) != map[bool]int{false: 0, true: 1}[output] {
			t.Fatal(string(encoded))
		}
	}
}
