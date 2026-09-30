package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestFileSchemaKeepsReferencesDistinctFromTextInput(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "files", Models: []schema.Model{{Name: "document", GoName: "Document", Fields: []schema.Field{field}}}})
	}
	s, err := build(schema.FileField("document", "Document", schema.Blank(), schema.Nullable(), schema.Unique(), schema.Default("seed.txt")))
	if err != nil {
		t.Fatal(err)
	}
	file := s.Models[0].Fields[1]
	if file.Kind != ir.FieldFile || file.MaxLength != 100 || !file.Blank || !file.Nullable || !file.Unique || file.Default.String != "seed.txt" {
		t.Fatal("file metadata lost")
	}
	for _, kind := range []ir.FieldKind{ir.FieldChar, ir.FieldEmail} {
		before := file.Clone()
		before.Kind = kind
		for _, pair := range [][2]ir.Field{{before, file}, {file, before}} {
			change, err := ir.ClassifyFieldChange(pair[0], pair[1])
			if err != nil || change != ir.ChangeStringSemantics {
				t.Fatal("same-storage transition rejected", err)
			}
		}
		other := s.Clone()
		other.Models[0].Fields[1] = before
		left, _ := ir.Hash(s)
		right, _ := ir.Hash(other)
		if left == right {
			t.Fatal("file semantics absent from schema identity")
		}
		for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.MaxLength++ }, func(f *ir.Field) { f.Nullable = false }, func(f *ir.Field) { f.Column = "other" }, func(f *ir.Field) { f.Default.String = "other" }} {
			changed := file.Clone()
			mutate(&changed)
			if _, err := ir.ClassifyFieldChange(before, changed); err == nil {
				t.Fatal("kind change hid storage change")
			}
		}
	}
	for _, invalid := range []schema.Field{
		schema.FileField("document", "Document", schema.MaxLength(0)),
		schema.FileField("document", "Document", schema.MaxLength(-1)),
		schema.FileField("document", "Document", schema.Default(true)),
		schema.FileField("document", "Document", schema.MaxLength(2), schema.Default("abc")),
		schema.FileField("document", "Document", schema.Choices(schema.Choice("x", "X"))),
	} {
		if _, err := build(invalid); err == nil {
			t.Fatal("invalid or unsupported file metadata accepted")
		}
	}
}
