package codegen_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateRelationProjectionIsDeterministicAndByteLocked(t *testing.T) {
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
			name:        "v3 source",
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
				[]byte("query.Integer(_scan.scanID.Int64), orm.ProjectionPresent"),
			},
			forbidden: [][]byte{
				[]byte("godjPrimaryKeyPresent"),
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

	authors, blog := relationQueryGenerationSchemas()
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

func TestGenerateRelationProjectionSnapshotsInputPreservesOldBytesAndLastGood(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	oldAuthorsMainBefore := mustGeneratedCode(t, "authors main before", func() ([]byte, error) {
		return codegen.Generate("authors", authors)
	})
	oldAuthorsMetadataBefore := mustGeneratedCode(t, "authors metadata before", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("authors", authors)
	})
	oldAuthorsObjectBefore := mustGeneratedCode(t, "authors object before", func() ([]byte, error) {
		return codegen.GenerateRelationObject("authors", authors)
	})
	oldBlogMainBefore := mustGeneratedCode(t, "blog main before", func() ([]byte, error) {
		return codegen.Generate("blog", blog)
	})
	oldBlogMetadataBefore := mustGeneratedCode(t, "blog metadata before", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("blog", blog)
	})
	oldBlogQueryBefore := mustGeneratedCode(t, "blog query before", func() ([]byte, error) {
		return codegen.GenerateRelationQuery("blog", blog)
	})
	oldBlogObjectBefore := mustGeneratedCode(t, "blog object before", func() ([]byte, error) {
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
	_, freshBlog := relationQueryGenerationSchemas()
	oldAuthorsMainAfter := mustGeneratedCode(t, "authors main after", func() ([]byte, error) {
		return codegen.Generate("authors", authors)
	})
	oldAuthorsMetadataAfter := mustGeneratedCode(t, "authors metadata after", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("authors", authors)
	})
	oldAuthorsObjectAfter := mustGeneratedCode(t, "authors object after", func() ([]byte, error) {
		return codegen.GenerateRelationObject("authors", authors)
	})
	oldBlogMainAfter := mustGeneratedCode(t, "blog main after", func() ([]byte, error) {
		return codegen.Generate("blog", freshBlog)
	})
	oldBlogMetadataAfter := mustGeneratedCode(t, "blog metadata after", func() ([]byte, error) {
		return codegen.GenerateRelationMetadata("blog", freshBlog)
	})
	oldBlogQueryAfter := mustGeneratedCode(t, "blog query after", func() ([]byte, error) {
		return codegen.GenerateRelationQuery("blog", freshBlog)
	})
	oldBlogObjectAfter := mustGeneratedCode(t, "blog object after", func() ([]byte, error) {
		return codegen.GenerateRelationObject("blog", freshBlog)
	})
	before := [][]byte{oldAuthorsMainBefore, oldAuthorsMetadataBefore, oldAuthorsObjectBefore, oldBlogMainBefore, oldBlogMetadataBefore, oldBlogQueryBefore, oldBlogObjectBefore}
	after := [][]byte{oldAuthorsMainAfter, oldAuthorsMetadataAfter, oldAuthorsObjectAfter, oldBlogMainAfter, oldBlogMetadataAfter, oldBlogQueryAfter, oldBlogObjectAfter}
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("new relation projection generation changed old prerequisite byte stream %d", index)
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

func TestGeneratedRelationProjectionPresenceAndPrivatePrimaryKeyCompile(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-projection"
	directory := writeGeneratedRelationProjectionApps(t, modulePath, authors, blog, true)
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation projection apps did not compile or pass: %v\n%s", err, output)
	}
}

func writeGeneratedRelationProjectionApps(
	t *testing.T,
	modulePath string,
	authors, blog ir.Schema,
	includeTests bool,
) string {
	t.Helper()
	authorsMain, err := codegen.Generate("authors", authors)
	if err != nil {
		t.Fatalf("generate authors main: %v", err)
	}
	authorsMetadata, err := codegen.GenerateRelationMetadata("authors", authors)
	if err != nil {
		t.Fatalf("generate authors metadata: %v", err)
	}
	authorsObject, err := codegen.GenerateRelationObject("authors", authors)
	if err != nil {
		t.Fatalf("generate authors object: %v", err)
	}
	authorsProjection, err := codegen.GenerateRelationProjection("authors", authors)
	if err != nil {
		t.Fatalf("generate authors projection: %v", err)
	}
	blogMain, err := codegen.Generate("blog", blog)
	if err != nil {
		t.Fatalf("generate blog main: %v", err)
	}
	blogMetadata, err := codegen.GenerateRelationMetadata("blog", blog)
	if err != nil {
		t.Fatalf("generate blog metadata: %v", err)
	}
	blogQuery, err := codegen.GenerateRelationQuery("blog", blog)
	if err != nil {
		t.Fatalf("generate blog query: %v", err)
	}
	blogObject, err := codegen.GenerateRelationObject("blog", blog)
	if err != nil {
		t.Fatalf("generate blog object: %v", err)
	}
	blogProjection, err := codegen.GenerateRelationProjection("blog", blog)
	if err != nil {
		t.Fatalf("generate blog projection: %v", err)
	}

	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))
	writeGeneratedTestFile(t, directory, "authors/zz_godj_generated.go", authorsMain)
	writeGeneratedTestFile(t, directory, "authors/zz_godj_relation.go", authorsMetadata)
	writeGeneratedTestFile(t, directory, "authors/zz_godj_relation_object.go", authorsObject)
	writeGeneratedTestFile(t, directory, "authors/zz_godj_relation_projection.go", authorsProjection)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_generated.go", blogMain)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation.go", blogMetadata)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation_query.go", blogQuery)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation_object.go", blogObject)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation_projection.go", blogProjection)
	if includeTests {
		writeGeneratedTestFile(t, directory, "authors/relation_projection_test.go", generatedAuthorsProjectionTest())
		writeGeneratedTestFile(t, directory, "blog/relation_projection_test.go", generatedBlogProjectionTest())
	}
	return directory
}

func generatedAuthorsProjectionTest() []byte {
	return []byte(`package authors

import (
	"database/sql"
	"testing"

	"github.com/progresshans/godj/orm"
)

func TestProjectionPresenceAndPrimaryKey(t *testing.T) {
	scan := (AuthorDescriptor{}).NewProjectionScan()
	destinations := scan.Destinations()
	if len(destinations) != 2 {
		t.Fatalf("destinations = %d, want 2", len(destinations))
	}
	*destinations[0].(*sql.NullInt64) = sql.NullInt64{Int64: 1, Valid: true}
	*destinations[1].(*sql.NullString) = sql.NullString{String: "Ada", Valid: true}
	value, key, presence := scan.Decode()
	if presence != orm.ProjectionPresent || value.ID != 1 || value.Name != "Ada" {
		t.Fatalf("present decode = %#v, %#v, %v", value, key, presence)
	}
	if integer, ok := key.Integer(); !ok || integer != 1 {
		t.Fatalf("key = %#v", key)
	}
	if primary, ok := (AuthorDescriptor{}).PrimaryKey(value); !ok || !primary.Equal(key) {
		t.Fatalf("private primary-key presence was not restored: %#v, %v", primary, ok)
	}

	absent := (AuthorDescriptor{}).NewProjectionScan()
	_, absentKey, presence := absent.Decode()
	if presence != orm.ProjectionAbsent || !absentKey.IsNull() {
		t.Fatalf("absent decode = %#v, %v", absentKey, presence)
	}

	partial := (AuthorDescriptor{}).NewProjectionScan()
	*partial.Destinations()[1].(*sql.NullString) = sql.NullString{String: "Ada", Valid: true}
	_, _, presence = partial.Decode()
	if presence != orm.ProjectionInvalid {
		t.Fatalf("partial presence = %v, want invalid", presence)
	}

	var nilScan *authorProjectionScan
	if nilScan.Destinations() != nil {
		t.Fatal("nil scan returned destinations")
	}
	_, _, presence = nilScan.Decode()
	if presence != orm.ProjectionInvalid {
		t.Fatalf("nil scan presence = %v, want invalid", presence)
	}
}
`)
}

func generatedBlogProjectionTest() []byte {
	return []byte(`package blog

import (
	"database/sql"
	"testing"

	"github.com/progresshans/godj/orm"
)

func TestProjectionNullableFieldAndPresence(t *testing.T) {
	scan := (PostDescriptor{}).NewProjectionScan()
	destinations := scan.Destinations()
	if len(destinations) != 4 {
		t.Fatalf("destinations = %d, want 4", len(destinations))
	}
	*destinations[0].(*sql.NullInt64) = sql.NullInt64{Int64: 10, Valid: true}
	*destinations[1].(*sql.NullString) = sql.NullString{String: "Alpha", Valid: true}
	*destinations[2].(*sql.NullInt64) = sql.NullInt64{Int64: 1, Valid: true}
	value, key, presence := scan.Decode()
	if presence != orm.ProjectionPresent || value.ID != 10 || value.Title != "Alpha" || value.AuthorID != 1 || value.ReviewerID != nil {
		t.Fatalf("present nullable decode = %#v, %#v, %v", value, key, presence)
	}
	*destinations[3].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
	value, _, presence = scan.Decode()
	if presence != orm.ProjectionPresent || value.ReviewerID == nil || *value.ReviewerID != 2 {
		t.Fatalf("present reviewer decode = %#v, %v", value, presence)
	}

	absent := (PostDescriptor{}).NewProjectionScan()
	_, absentKey, presence := absent.Decode()
	if presence != orm.ProjectionAbsent || !absentKey.IsNull() {
		t.Fatalf("absent decode = %#v, %v", absentKey, presence)
	}

	partial := (PostDescriptor{}).NewProjectionScan()
	*partial.Destinations()[3].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
	_, _, presence = partial.Decode()
	if presence != orm.ProjectionInvalid {
		t.Fatalf("nullable-only partial presence = %v, want invalid", presence)
	}
}
`)
}

func mustGeneratedCode(t *testing.T, label string, generate func() ([]byte, error)) []byte {
	t.Helper()
	result, err := generate()
	if err != nil {
		t.Fatalf("generate %s: %v", label, err)
	}
	return result
}
