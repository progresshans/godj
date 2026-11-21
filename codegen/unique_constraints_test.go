package codegen_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGenerateNamedUniqueConstraintsPreservesLogicalMembers(t *testing.T) {
	input, err := schema.Build(schema.Definition{AppLabel: "scoped", Models: []schema.Model{{
		Name: "label", GoName: "Label", Fields: []schema.Field{schema.IntegerField("category", "Category", schema.Column("category_key")), schema.CharField("name", "Name", 64)},
		UniqueConstraints: []schema.UniqueConstraint{{Name: "category_name", Fields: []string{"category", "name"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := codegen.Generate("models", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", first, parser.AllErrors); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first, []byte("UniqueConstraints: []ir.UniqueConstraint{")) || !bytes.Contains(first, []byte("{Name: \"category_name\", Fields: []string{\"category\", \"name\"}}")) {
		t.Fatal("generated descriptor omitted constraint identity or substituted storage columns")
	}
	second, err := codegen.Generate("models", input.Clone())
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("equivalent snapshots generated different source", err)
	}
	input.Models[0].UniqueConstraints[0].Fields[0] = "missing"
	if invalid, err := codegen.Generate("models", input); err == nil || invalid != nil {
		t.Fatal("invalid constraint generated partial source")
	}
}
