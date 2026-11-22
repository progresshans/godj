package ir

import "testing"

func TestRenameFieldPreservesOrdinalDimensionsAndNamedKeys(t *testing.T) {
	s, err := Normalize(Schema{FormatVersion: CurrentFormatVersion, AppLabel: "gallery", Models: []Model{{
		Name: "photo", GoName: "Photo", Fields: []Field{
			{Name: "width", GoName: "Width", Kind: FieldInteger},
			{Name: "image", GoName: "Image", Kind: FieldImage, MaxLength: 100, WidthField: "width"},
		}, UniqueConstraints: []UniqueConstraint{{Name: "asset_shape", Fields: []string{"image", "width"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before := model.Fields[1]
	next := before
	next.Name, next.GoName = "pixel_width", "PixelWidth"
	changed, err := RenameModelField(s.AppLabel, model, before, next)
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Fields[1].Equal(next) || changed.Fields[2].WidthField != "pixel_width" || changed.UniqueConstraints[0].Fields[1] != "pixel_width" || changed.Fields[1].Column != "width" {
		t.Fatal("lost ordinal, fixed column or field reference", changed)
	}
	back, err := RenameModelField(s.AppLabel, changed, next, before)
	if err != nil || !back.Equal(model) {
		t.Fatal("field rename did not invert", err)
	}
	changed.UniqueConstraints[0].Fields[0] = "caller_owned"
	if model.UniqueConstraints[0].Fields[0] != "image" || model.Fields[2].WidthField != "width" {
		t.Fatal("rename mutated input model")
	}
	for _, patch := range []func(*Field){
		func(f *Field) { f.Nullable = true }, func(f *Field) { f.GoName = "Image" }, func(f *Field) { f.Column = "image" },
		func(f *Field) { f.Name = "image" }, func(f *Field) { f.Name = before.Name }, func(f *Field) { f.Column = "bad column" },
	} {
		invalid := next
		patch(&invalid)
		if _, err := RenameModelField(s.AppLabel, model, before, invalid); err == nil {
			t.Fatal("accepted invalid field transition", invalid)
		}
	}
}
