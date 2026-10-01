package schema_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestImageSchemaOwnsDimensionsAndCanonicalIdentity(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "photos", Models: []schema.Model{{Name: "photo", GoName: "Photo", Fields: []schema.Field{
		schema.ImageField("image", "Image", schema.Nullable(), schema.Blank(), schema.ImageDimensions("width", "height")),
		schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	field := model.Fields[1]
	if field.Kind != ir.FieldImage || field.MaxLength != 100 || field.WidthField != "width" || field.HeightField != "height" {
		t.Fatal("image declaration lost")
	}
	owners, err := ir.ImageDimensionOwners(model)
	if err != nil || !reflect.DeepEqual(owners, map[string]string{"width": "image", "height": "image"}) {
		t.Fatal("dimension ownership", owners, err)
	}
	owners["width"] = "changed"
	again, _ := ir.ImageDimensionOwners(model)
	if again["width"] != "image" {
		t.Fatal("shared owner map")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ir.Schema
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	normalized, err := ir.Normalize(decoded)
	if err != nil || !reflect.DeepEqual(normalized, s) {
		t.Fatal("image roundtrip", err)
	}
	for _, after := range []ir.Field{func() ir.Field { f := field; f.WidthField = ""; return f }(), func() ir.Field { f := field; f.WidthField, f.HeightField = f.HeightField, f.WidthField; return f }(), func() ir.Field { f := field; f.Kind = ir.FieldFile; f.WidthField, f.HeightField = "", ""; return f }()} {
		for _, pair := range [][2]ir.Field{{field, after}, {after, field}} {
			kind, err := ir.ClassifyFieldChange(pair[0], pair[1])
			if err != nil || kind != ir.ChangeStringSemantics {
				t.Fatal("same-storage image change", err)
			}
		}
		changed := s.Clone()
		changed.Models[0].Fields[1] = after
		left, _ := ir.Hash(s)
		right, err := ir.Hash(changed)
		if err != nil || left == right {
			t.Fatal("dimensions absent from identity", err)
		}
		after.MaxLength++
		if _, err := ir.ClassifyFieldChange(field, after); err == nil {
			t.Fatal("image semantics hid storage change")
		}
	}
	for _, check := range []struct {
		name   string
		mutate func(*ir.Model)
	}{
		{"missing", func(m *ir.Model) { m.Fields[1].WidthField = "missing" }},
		{"self", func(m *ir.Model) { m.Fields[1].WidthField = "image" }},
		{"primary", func(m *ir.Model) { m.Fields[1].WidthField = "id" }},
		{"invalid_identifier", func(m *ir.Model) { m.Fields[1].HeightField = "bad.name" }},
		{"noninteger", func(m *ir.Model) { m.Fields[2].Kind = ir.FieldBoolean }},
		{"same_target", func(m *ir.Model) { m.Fields[1].HeightField = "width" }},
		{"not_image", func(m *ir.Model) { m.Fields[1].Kind = ir.FieldFile }},
		{"shared", func(m *ir.Model) {
			f := m.Fields[1]
			f.Name, f.GoName, f.Column = "other", "Other", "other"
			m.Fields = append(m.Fields, f)
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			candidate := s.Clone()
			check.mutate(&candidate.Models[0])
			if _, err := ir.Normalize(candidate); err == nil {
				t.Fatal("invalid dimension ownership accepted")
			}
		})
	}
	// Standalone migration fields validate local syntax; only their actual model
	// can prove the existence/type/ownership of the referenced fields.
	lone, err := ir.NormalizeField(field)
	if err != nil || !lone.Equal(field) {
		t.Fatal("standalone image field requires invented model", err)
	}
}
