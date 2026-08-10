package codegen_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateRelationMetadataSupportsV2AndV3FreshSchemaCompanions(t *testing.T) {
	t.Parallel()

	authors, blog := relationGenerationSchemas()
	tests := []struct {
		name      string
		input     ir.Schema
		fragments [][]byte
		forbidden [][]byte
	}{
		{
			name:  "v2 target app",
			input: authors,
			fragments: [][]byte{
				[]byte(`const GoDjRelationMetadataGeneratorVersion = "godj-codegen-rel-v1"`),
				[]byte("const GoDjRelationSchemaSHA256 ="),
				[]byte("func GoDjRelationSchema() ir.Schema"),
				[]byte("FormatVersion: ir.FormatVersion"),
				[]byte(`"authors"`),
				[]byte(`"author"`),
			},
			forbidden: [][]byte{[]byte("ForeignKeyRelation"), []byte("blog")},
		},
		{
			name:  "v3 source app",
			input: blog,
			fragments: [][]byte{
				[]byte(`const GoDjRelationMetadataGeneratorVersion = "godj-codegen-rel-v1"`),
				[]byte("const GoDjRelationSchemaSHA256 ="),
				[]byte("func GoDjRelationSchema() ir.Schema"),
				[]byte("FormatVersion: ir.RelationFormatVersion"),
				[]byte("ir.FieldForeignKey"),
				[]byte(`Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}`),
				[]byte("Cardinality: ir.RelationManyToOne"),
				[]byte(`Reverse:     ir.ReverseRelation{Name: "reviewed_posts"}`),
				[]byte("OnDelete:    ir.DeleteSetNull"),
			},
			forbidden: [][]byte{[]byte("github.com/progresshans/godj/orm"), []byte("github.com/progresshans/godj/query")},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := codegen.GenerateRelationMetadata("models", test.input)
			if err != nil {
				t.Fatalf("GenerateRelationMetadata() error = %v", err)
			}
			second, err := codegen.GenerateRelationMetadata("models", test.input)
			if err != nil {
				t.Fatalf("GenerateRelationMetadata() second error = %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Fatal("relation metadata generation is not byte deterministic")
			}
			for _, fragment := range test.fragments {
				if !bytes.Contains(first, fragment) {
					t.Fatalf("relation metadata source does not contain %q:\n%s", fragment, first)
				}
			}
			for _, fragment := range test.forbidden {
				if bytes.Contains(first, fragment) {
					t.Fatalf("relation metadata source contains forbidden %q:\n%s", fragment, first)
				}
			}
			normalized, err := ir.Normalize(test.input)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			hash, err := ir.Hash(normalized)
			if err != nil {
				t.Fatalf("Hash() error = %v", err)
			}
			if !bytes.Contains(first, []byte(hash)) {
				t.Fatalf("relation metadata source does not contain normalized hash %q", hash)
			}
		})
	}
}

func TestGenerateRelationMetadataSnapshotsInputAndRendersDisabledReverse(t *testing.T) {
	t.Parallel()

	_, blog := relationGenerationSchemas()
	blog.Models[0].Fields[1].Relation.Reverse = ir.ReverseRelation{Disabled: true}
	before, err := codegen.GenerateRelationMetadata("models", blog)
	if err != nil {
		t.Fatalf("GenerateRelationMetadata() error = %v", err)
	}
	if !bytes.Contains(before, []byte("ir.ReverseRelation{Disabled: true}")) {
		t.Fatalf("disabled reverse is not explicit:\n%s", before)
	}
	blog.Models[0].Fields[1].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(before, []byte("mutated")) {
		t.Fatal("post-generation input mutation changed candidate bytes")
	}
}

func TestGenerateRelationMetadataRejectsInvalidPackageAndFixedSymbolCollision(t *testing.T) {
	t.Parallel()

	authors, _ := relationGenerationSchemas()
	if _, err := codegen.GenerateRelationMetadata("bad-package", authors); err == nil {
		t.Fatal("GenerateRelationMetadata() accepted invalid package name")
	}
	if _, err := codegen.GenerateRelationMetadata("_", authors); err == nil {
		t.Fatal("GenerateRelationMetadata() accepted blank package identifier")
	}
	authors.Models[0].GoName = "GoDjRelationSchema"
	if _, err := codegen.GenerateRelationMetadata("models", authors); err == nil {
		t.Fatal("GenerateRelationMetadata() accepted accessor symbol collision")
	}
}
