package schema_test

import (
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestSlugSchemaOwnsUnicodeAndColumnIndexSemantics(t *testing.T) {
	build := func(field schema.Field) (ir.Schema, error) {
		return schema.Build(schema.Definition{AppLabel: "articles", Models: []schema.Model{{Name: "article", GoName: "Article", Fields: []schema.Field{field}}}})
	}
	definition, err := build(schema.SlugField("slug", "Slug", schema.AllowUnicode(true), schema.Nullable(), schema.Default("Old Slug!")))
	if err != nil {
		t.Fatal(err)
	}
	field := definition.Models[0].Fields[1]
	if field.Kind != ir.FieldSlug || field.MaxLength != 50 || !field.DBIndex || !field.HasColumnIndex() || !field.AllowUnicode || field.Default.String != "Old Slug!" {
		t.Fatal("slug defaults or explicit metadata lost")
	}
	plain := field.Clone()
	plain.Kind = ir.FieldChar
	plain.AllowUnicode = false
	plain.DBIndex = false
	for _, pair := range [][2]ir.Field{{plain, field}, {field, plain}} {
		if kind, err := ir.ClassifyFieldChange(pair[0], pair[1]); err != nil || kind != ir.ChangeIndex {
			t.Fatal("indexed string transition lost physical ownership", kind, err)
		}
	}
	unindexed := field.Clone()
	unindexed.DBIndex = false
	if kind, err := ir.ClassifyFieldChange(plain, unindexed); err != nil || kind != ir.ChangeStringSemantics {
		t.Fatal("storage-equivalent slug transition", kind, err)
	}
	ascii := field.Clone()
	ascii.AllowUnicode = false
	if kind, err := ir.ClassifyFieldChange(field, ascii); err != nil || kind != ir.ChangeStringSemantics {
		t.Fatal("Unicode policy transition", kind, err)
	}
	for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.MaxLength++ }, func(f *ir.Field) { f.Column = "other" }, func(f *ir.Field) { f.Unique = true }, func(f *ir.Field) { f.Nullable = false }} {
		changed := field.Clone()
		mutate(&changed)
		if _, err := ir.ClassifyFieldChange(plain, changed); err == nil {
			t.Fatal("index/string change hid another storage facet")
		}
	}
	for _, option := range []schema.FieldOption{schema.DBIndex(false), schema.Unique()} {
		definition, err := build(schema.SlugField("slug", "Slug", option))
		if err != nil {
			t.Fatal(err)
		}
		if definition.Models[0].Fields[1].HasColumnIndex() {
			t.Fatal("redundant or disabled index was requested")
		}
	}
	for _, invalid := range []schema.Field{schema.CharField("slug", "Slug", 50, schema.AllowUnicode(true)), schema.SlugField("slug", "Slug", schema.MaxLength(0)), schema.SlugField("slug", "Slug", schema.Default(true))} {
		if _, err := build(invalid); err == nil {
			t.Fatal("invalid slug declaration accepted")
		}
	}
	for _, changed := range []ir.Field{unindexed, ascii} {
		other := definition.Clone()
		other.Models[0].Fields[1] = changed
		beforeHash, _ := ir.Hash(definition)
		afterHash, _ := ir.Hash(other)
		if field.Equal(changed) || beforeHash == afterHash {
			t.Fatal("canonical index/Unicode identity lost")
		}
	}
}
