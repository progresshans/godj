package codegen_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateRelationObjectIsDeterministicAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
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
			name:        "v2 target",
			packageName: "authors",
			schema:      authors,
			golden:      "authors.golden",
			fragments: [][]byte{
				[]byte(`const GoDjRelationObjectGeneratorVersion = "godj-codegen-rel-object-v1"`),
				[]byte("const GoDjRelationObjectSchemaSHA256 ="),
				[]byte("var _ orm.RelationObjectDescriptor[Author] = AuthorDescriptor{}"),
				[]byte("func (AuthorDescriptor) SnapshotRelationObjectDescriptor() orm.RelationObjectDescriptor[Author]"),
				[]byte("func (AuthorDescriptor) BindRelationStorage(field ir.Field) (orm.RelationStorage[Author], bool)"),
				[]byte("return nil, false"),
			},
			forbidden: [][]byte{
				[]byte(`"reflect"`),
				[]byte(`"github.com/progresshans/godj/query"`),
				[]byte("type AuthorDescriptor struct"),
				[]byte("example.com/"),
			},
			wantExported: []string{"GoDjRelationObjectGeneratorVersion", "GoDjRelationObjectSchemaSHA256"},
		},
		{
			name:        "v3 source",
			packageName: "blog",
			schema:      blog,
			golden:      "blog.golden",
			fragments: [][]byte{
				[]byte(`const GoDjRelationObjectGeneratorVersion = "godj-codegen-rel-object-v1"`),
				[]byte("var _ orm.RelationObjectDescriptor[Post] = PostDescriptor{}"),
				[]byte("type postAuthorIDRelationStorage struct{}"),
				[]byte("type postReviewerIDRelationStorage struct{}"),
				[]byte("case reflect.DeepEqual(field, (postAuthorIDRelationStorage{}).Field())"),
				[]byte("return schema.Models[0].Fields[2].Clone()"),
				[]byte("return query.Integer(value.AuthorID), true"),
				[]byte("if value.ReviewerID == nil"),
				[]byte("return query.Null(), true"),
				[]byte("return query.Integer(*value.ReviewerID), true"),
			},
			forbidden: [][]byte{
				[]byte("type PostDescriptor struct"),
				[]byte("authors.Author"),
				[]byte("reflect.ValueOf"),
				[]byte("panic("),
				[]byte("func init("),
				[]byte("example.com/"),
			},
			wantExported: []string{"GoDjRelationObjectGeneratorVersion", "GoDjRelationObjectSchemaSHA256"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, err := codegen.GenerateRelationObject(test.packageName, test.schema)
			if err != nil {
				t.Fatalf("GenerateRelationObject() error = %v", err)
			}
			second, err := codegen.GenerateRelationObject(test.packageName, test.schema)
			if err != nil {
				t.Fatalf("GenerateRelationObject() second error = %v", err)
			}
			if !bytes.Equal(first, second) {
				t.Fatal("relation object generation is not byte deterministic")
			}
			want, err := os.ReadFile(filepath.Join("testdata", "relation_object", test.golden))
			if err != nil {
				t.Fatalf("read relation object golden: %v\ngenerated:\n%s", err, first)
			}
			if !bytes.Equal(first, want) {
				t.Fatalf("relation object bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
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
				t.Fatalf("relation object source does not contain normalized hash %q", hash)
			}
			for _, fragment := range test.fragments {
				if !bytes.Contains(first, fragment) {
					t.Fatalf("relation object source does not contain %q:\n%s", fragment, first)
				}
			}
			for _, fragment := range test.forbidden {
				if bytes.Contains(first, fragment) {
					t.Fatalf("relation object source contains forbidden %q:\n%s", fragment, first)
				}
			}
			if got := exportedDeclarations(t, test.packageName+"_relation_object.go", first); !slices.Equal(got, test.wantExported) {
				t.Fatalf("relation object exported declarations = %v, want %v", got, test.wantExported)
			}
		})
	}
}

func TestGenerateRelationObjectSnapshotsInputPreservesPrerequisitesAndNeverWritesOnFailure(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	oldBlogBefore, err := codegen.GenerateRelationQuery("blog", blog)
	if err != nil {
		t.Fatalf("GenerateRelationQuery() before error = %v", err)
	}
	oldProjectBefore, err := codegen.GenerateProjectRelationQuery("project", relationQueryGenerationPackages(authors, blog))
	if err != nil {
		t.Fatalf("GenerateProjectRelationQuery() before error = %v", err)
	}
	generated, err := codegen.GenerateRelationObject("blog", blog)
	if err != nil {
		t.Fatalf("GenerateRelationObject() error = %v", err)
	}
	blog.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(generated, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed relation object bytes")
	}
	_, freshBlog := relationQueryGenerationSchemas()
	oldBlogAfter, err := codegen.GenerateRelationQuery("blog", freshBlog)
	if err != nil {
		t.Fatalf("GenerateRelationQuery() after error = %v", err)
	}
	oldProjectAfter, err := codegen.GenerateProjectRelationQuery("project", relationQueryGenerationPackages(authors, freshBlog))
	if err != nil {
		t.Fatalf("GenerateProjectRelationQuery() after error = %v", err)
	}
	if !bytes.Equal(oldBlogBefore, oldBlogAfter) || !bytes.Equal(oldProjectBefore, oldProjectAfter) {
		t.Fatal("new relation object generation changed an existing v1 generator byte stream")
	}

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if _, err := codegen.GenerateRelationObject("bad-package", freshBlog); err == nil {
		t.Fatal("GenerateRelationObject() accepted invalid package")
	}
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}

func TestGenerateRelationObjectRejectsOwnFixedNamespaceCollision(t *testing.T) {
	t.Parallel()

	authors, _ := relationQueryGenerationSchemas()
	authors.Models[0].GoName = "GoDjRelationObjectGeneratorVersion"
	if _, err := codegen.GenerateRelationObject("authors", authors); err == nil {
		t.Fatal("GenerateRelationObject() accepted object provenance collision")
	}
}

func exportedDeclarations(t *testing.T, name string, source []byte) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		t.Fatalf("parse generated source: %v", err)
	}
	exported := make([]string, 0)
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						if name.IsExported() {
							exported = append(exported, name.Name)
						}
					}
				case *ast.TypeSpec:
					if specification.Name.IsExported() {
						exported = append(exported, specification.Name.Name)
					}
				}
			}
		}
	}
	slices.Sort(exported)
	return exported
}
