package openapi_test

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/serializers"
)

func TestExtendObjectKeepsPartialModelPolicyAndRequiredIdentity(t *testing.T) {
	title, err := serializers.StringField("title", serializers.WithMaxLength(32))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := serializers.BooleanField("closed", serializers.WithDefault(serializers.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{title, closed})
	if err != nil {
		t.Fatal(err)
	}
	partial, err := openapi.RequestSchema(spec, serializers.ModePartial)
	if err != nil {
		t.Fatal(err)
	}
	before := schemaTestEncode(t, partial)
	identity, err := openapi.IntegerRange(1, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	properties := []openapi.Property{{Name: "id", Schema: identity, Required: true}}
	extended, err := openapi.ExtendObject(partial, properties...)
	if err != nil {
		t.Fatal(err)
	}
	properties[0].Name = "caller change"
	if !bytes.Equal(before, schemaTestEncode(t, partial)) || !reflect.DeepEqual(schemaTestRequired(t, extended), []string{"id"}) || !reflect.DeepEqual(schemaTestMemberNames(schemaTestProperties(t, extended)), []string{"title", "closed", "id"}) {
		t.Fatal("extension changed input or field ownership")
	}
	for _, field := range []string{"title", "closed"} {
		if !bytes.Equal(schemaTestEncodeValue(t, schemaTestProperty(t, partial, field).Value()), schemaTestEncodeValue(t, schemaTestProperty(t, extended, field).Value())) {
			t.Fatal("partial field policy changed", field)
		}
	}
	if schemaTestBoolean(t, schemaTestObject(t, extended), "additionalProperties") || schemaTestInteger(t, schemaTestProperty(t, extended, "id"), "minimum") != 1 {
		t.Fatal("extension widened input shape")
	}
	full, err := openapi.RequestSchema(spec, serializers.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	extended, err = openapi.ExtendObject(full, openapi.Property{Name: "id", Schema: identity, Required: true})
	if err != nil || !reflect.DeepEqual(schemaTestRequired(t, extended), []string{"title", "id"}) {
		t.Fatal("existing required field lost", err)
	}
}

func TestExtendObjectRejectsAmbiguousOrInvalidComposition(t *testing.T) {
	base, err := openapi.Object(openapi.Property{Name: "id", Schema: openapi.Integer(), Required: true})
	if err != nil {
		t.Fatal(err)
	}
	array, err := openapi.Array(base)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := openapi.Ref("Ticket")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		base   openapi.Schema
		fields []openapi.Property
	}{
		{"zero", openapi.Schema{}, nil}, {"array", array, nil}, {"reference", ref, nil}, {"scalar", openapi.String(), nil},
		{"shadow", base, []openapi.Property{{Name: "id", Schema: openapi.String()}}},
		{"duplicate", base, []openapi.Property{{Name: "new", Schema: openapi.String()}, {Name: "new", Schema: openapi.String()}}},
		{"invalid", base, []openapi.Property{{Name: "bad\x00", Schema: openapi.String()}}},
		{"zero_property", base, []openapi.Property{{Name: "new"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := openapi.ExtendObject(test.base, test.fields...)
			if !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) || result.Value().Kind() != 0 {
				t.Fatal("invalid extension published", err)
			}
		})
	}
}
