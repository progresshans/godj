package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateRelationProjectionIsDeterministicAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	tests := []struct {
		name         string
		packageName  string
		schema       ir.Schema
		golden       string
		fragments    [][]byte
		forbidden    [][]byte
		wantExported []string
	}{
		{
			name:        "current target",
			packageName: "authors",
			schema:      authors,
			golden:      "authors.golden",
			fragments: [][]byte{
				[]byte(`const GoDjRelationProjectionGeneratorVersion = "godj-codegen-rel-projection-v1"`),
				[]byte("var _ orm.ProjectionDescriptor[Author] = AuthorDescriptor{}"),
				[]byte("func (AuthorDescriptor) NewProjectionScan() orm.ProjectionScan[Author]"),
				[]byte("scanID   sql.NullInt64"),
				[]byte("scanName sql.NullString"),
				[]byte("return Author{}, query.Null(), orm.ProjectionAbsent"),
				[]byte("_value.godjPrimaryKeyPresent = true"),
				[]byte("query.Integer(_scan.scanID.Int64), orm.ProjectionPresent"),
			},
			forbidden: [][]byte{
				[]byte("func (AuthorDescriptor) Scan("),
				[]byte("reflect."),
				[]byte("panic("),
				[]byte("func init("),
			},
			wantExported: []string{"GoDjRelationProjectionGeneratorVersion", "GoDjRelationProjectionSchemaSHA256"},
		},
		{
			name:        "current source",
			packageName: "blog",
			schema:      blog,
			golden:      "blog.golden",
			fragments: [][]byte{
				[]byte(`const GoDjRelationProjectionGeneratorVersion = "godj-codegen-rel-projection-v1"`),
				[]byte("var _ orm.ProjectionDescriptor[Post] = PostDescriptor{}"),
				[]byte("func (PostDescriptor) NewProjectionScan() orm.ProjectionScan[Post]"),
				[]byte("scanReviewerID sql.NullInt64"),
				[]byte("if _scan.scanReviewerID.Valid"),
				[]byte("_value.ReviewerID = &_scanned"),
				[]byte("_value.godjPrimaryKeyPresent = true"),
				[]byte("query.Integer(_scan.scanID.Int64), orm.ProjectionPresent"),
			},
			forbidden: [][]byte{
				[]byte("func (PostDescriptor) Scan("),
				[]byte("authors.Author"),
				[]byte("panic("),
				[]byte("func init("),
			},
			wantExported: []string{"GoDjRelationProjectionGeneratorVersion", "GoDjRelationProjectionSchemaSHA256"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := codegen.GenerateRelationProjection(test.packageName, test.schema)
			if err != nil {
				t.Fatalf("GenerateRelationProjection() error = %v", err)
			}
			second, err := codegen.GenerateRelationProjection(test.packageName, test.schema)
			if err != nil {
				t.Fatalf("GenerateRelationProjection() second error = %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Fatal("relation projection generation is not byte deterministic")
			}
			want, err := os.ReadFile(filepath.Join("testdata", "relation_projection", test.golden))
			if err != nil {
				t.Fatalf("read relation projection golden: %v\ngenerated:\n%s", err, first)
			}
			if !bytes.Equal(first, want) {
				t.Fatalf("relation projection bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
			}
			normalized, err := ir.Normalize(test.schema)
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			hash, err := ir.Hash(normalized)
			if err != nil {
				t.Fatalf("Hash() error = %v", err)
			}
			if !bytes.Contains(first, []byte(hash)) {
				t.Fatalf("relation projection source does not contain normalized hash %q", hash)
			}
			for _, fragment := range test.fragments {
				if !bytes.Contains(first, fragment) {
					t.Fatalf("relation projection source does not contain %q:\n%s", fragment, first)
				}
			}
			for _, fragment := range test.forbidden {
				if bytes.Contains(first, fragment) {
					t.Fatalf("relation projection source contains forbidden %q:\n%s", fragment, first)
				}
			}
			if got := exportedDeclarations(t, test.packageName+"_relation_projection.go", first); !slices.Equal(got, test.wantExported) {
				t.Fatalf("relation projection exported declarations = %v, want %v", got, test.wantExported)
			}
		})
	}
}

func TestGenerateRelationProjectionRejectsInvalidInputsAndOwnNamespace(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	collision := authors.Clone()
	collision.Models[0].GoName = "GoDjRelationProjectionGeneratorVersion"
	for _, test := range []struct {
		name        string
		packageName string
		schema      ir.Schema
	}{
		{name: "invalid package", packageName: "bad-package", schema: blog},
		{name: "provenance collision", packageName: "authors", schema: collision},
		{name: "invalid schema", packageName: "blog", schema: ir.Schema{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := codegen.GenerateRelationProjection(test.packageName, test.schema)
			if err == nil {
				t.Fatal("GenerateRelationProjection() accepted invalid input")
			}
			if len(generated) != 0 {
				t.Fatalf("invalid input returned %d partial bytes", len(generated))
			}
		})
	}
}

func TestGenerateRelationProjectionSnapshotsInputPreservesPrerequisiteBytesAndLastGood(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	authorsMainBefore := testfixture.Generate(t, "authors main before", func() ([]byte, error) {
		return codegen.Generate("authors", authors)
	})
	authorsMetadataBefore := testfixture.Generate(t, "authors metadata before", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("authors", authors)
	})
	authorsObjectBefore := testfixture.Generate(t, "authors object before", func() ([]byte, error) {
		return codegen.GenerateRelationObject("authors", authors)
	})
	blogMainBefore := testfixture.Generate(t, "blog main before", func() ([]byte, error) {
		return codegen.Generate("blog", blog)
	})
	blogMetadataBefore := testfixture.Generate(t, "blog metadata before", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("blog", blog)
	})
	blogObjectBefore := testfixture.Generate(t, "blog object before", func() ([]byte, error) {
		return codegen.GenerateRelationObject("blog", blog)
	})

	generated, err := codegen.GenerateRelationProjection("blog", blog)
	if err != nil {
		t.Fatalf("GenerateRelationProjection() error = %v", err)
	}
	blog.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(generated, []byte("mutated")) {
		t.Fatal("post-generation input mutation changed relation projection bytes")
	}
	_, freshBlog := testschema.QueryRelation()
	authorsMainAfter := testfixture.Generate(t, "authors main after", func() ([]byte, error) {
		return codegen.Generate("authors", authors)
	})
	authorsMetadataAfter := testfixture.Generate(t, "authors metadata after", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("authors", authors)
	})
	authorsObjectAfter := testfixture.Generate(t, "authors object after", func() ([]byte, error) {
		return codegen.GenerateRelationObject("authors", authors)
	})
	blogMainAfter := testfixture.Generate(t, "blog main after", func() ([]byte, error) {
		return codegen.Generate("blog", freshBlog)
	})
	blogMetadataAfter := testfixture.Generate(t, "blog metadata after", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("blog", freshBlog)
	})
	blogObjectAfter := testfixture.Generate(t, "blog object after", func() ([]byte, error) {
		return codegen.GenerateRelationObject("blog", freshBlog)
	})
	before := [][]byte{authorsMainBefore, authorsMetadataBefore, authorsObjectBefore, blogMainBefore, blogMetadataBefore, blogObjectBefore}
	after := [][]byte{authorsMainAfter, authorsMetadataAfter, authorsObjectAfter, blogMainAfter, blogMetadataAfter, blogObjectAfter}
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("relation projection generation changed current prerequisite byte stream %d", index)
		}
	}

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if _, err := codegen.GenerateRelationProjection("bad-package", freshBlog); err == nil {
		t.Fatal("GenerateRelationProjection() accepted invalid package")
	}
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}
