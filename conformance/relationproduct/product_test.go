package relationproduct_test

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/relationproduct"
	"github.com/progresshans/godj/conformance/relationproduct/blog"
	"github.com/progresshans/godj/conformance/relationproduct/fixture"
	"github.com/progresshans/godj/schema/ir"
)

func TestCheckedInGeneratedRelationProjectMatchesDeterministicCandidates(t *testing.T) {
	t.Parallel()

	authors, err := fixture.AuthorsSchema()
	if err != nil {
		t.Fatal(err)
	}
	blogSchema, err := fixture.BlogSchema()
	if err != nil {
		t.Fatal(err)
	}
	candidates := []struct {
		path string
		data []byte
	}{
		{path: "authors/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("authors", authors) })},
		{path: "authors/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("authors", authors) })},
		{path: "blog/zz_godj_generated.go", data: generated(t, func() ([]byte, error) { return codegen.Generate("blog", blogSchema) })},
		{path: "blog/zz_godj_relation.go", data: generated(t, func() ([]byte, error) { return codegen.GenerateRelationMetadata("blog", blogSchema) })},
		{path: "project/zz_godj_bindings.go", data: generated(t, func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "blog", ImportPath: "github.com/progresshans/godj/conformance/relationproduct/blog"},
				{Alias: "authors", ImportPath: "github.com/progresshans/godj/conformance/relationproduct/authors"},
			})
		})},
	}
	root := relationProductDirectory(t)
	for _, candidate := range candidates {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, candidate.data) {
			t.Fatalf("checked-in generated file %s differs from deterministic candidate", candidate.path)
		}
	}
}

func TestGeneratedRelationProjectBindsActualForwardAndReverseMetadata(t *testing.T) {
	t.Parallel()

	binding, err := relationproduct.Binding()
	if err != nil {
		t.Fatal(err)
	}
	wantForward := []struct {
		field       string
		column      string
		nullable    bool
		reverse     string
		onDelete    ir.DeletePolicy
		cardinality ir.RelationCardinality
	}{
		{field: "author", column: "author_id", reverse: "posts", onDelete: ir.DeleteProtect, cardinality: ir.RelationManyToOne},
		{field: "reviewer", column: "reviewer_id", nullable: true, reverse: "reviewed_posts", onDelete: ir.DeleteSetNull, cardinality: ir.RelationManyToOne},
	}
	forward := binding.ForwardRelations()
	if len(forward) != len(wantForward) {
		t.Fatalf("forward relation count = %d, want %d", len(forward), len(wantForward))
	}
	for index, want := range wantForward {
		got := forward[index]
		if got.Source != (ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}) ||
			got.Field != want.field || got.Column != want.column ||
			got.Target != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
			got.Nullable != want.nullable || got.Cardinality != want.cardinality ||
			got.Reverse != (ir.ReverseRelation{Name: want.reverse}) || got.OnDelete != want.onDelete {
			t.Fatalf("forward relation %d = %#v", index, got)
		}
	}
	wantReverse := []struct {
		name  string
		field string
	}{{name: "posts", field: "author"}, {name: "reviewed_posts", field: "reviewer"}}
	reverse := binding.ReverseRelations()
	if len(reverse) != len(wantReverse) {
		t.Fatalf("reverse relation count = %d, want %d", len(reverse), len(wantReverse))
	}
	for index, want := range wantReverse {
		got := reverse[index]
		if got.Owner != (ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}) ||
			got.Name != want.name || got.Target != (ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}) ||
			got.SourceField != want.field || got.Cardinality != ir.RelationOneToMany {
			t.Fatalf("reverse relation %d = %#v", index, got)
		}
	}

	forward[0].Field = "mutated"
	reverse[0].Name = "mutated"
	if binding.ForwardRelations()[0].Field != "author" || binding.ReverseRelations()[0].Name != "posts" {
		t.Fatal("binding accessors exposed retained metadata")
	}
}

func TestGeneratedScalarStorageAndImportEdgesStayMetadataOnly(t *testing.T) {
	t.Parallel()

	post := blog.Post{}
	var required int64 = post.AuthorID
	var nullable *int64 = post.ReviewerID
	_, _ = required, nullable

	root := relationProductDirectory(t)
	for _, relative := range []string{
		"authors/zz_godj_generated.go",
		"authors/zz_godj_relation.go",
		"blog/zz_godj_generated.go",
		"blog/zz_godj_relation.go",
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, relative), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			if strings.Contains(path, "/conformance/relationproduct/authors") || strings.Contains(path, "/conformance/relationproduct/blog") {
				t.Fatalf("generated app file %s has app-to-app import %q", relative, path)
			}
		}
	}
}

func TestObserverImportsOnlyGeneratedBridgeAndRuntimeMetadata(t *testing.T) {
	t.Parallel()

	root := relationProductDirectory(t)
	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "observer.go"), nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(parsed.Imports))
	for _, imported := range parsed.Imports {
		got = append(got, strings.Trim(imported.Path.Value, `"`))
	}
	want := []string{
		"github.com/progresshans/godj/conformance/relationproduct/project",
		"github.com/progresshans/godj/orm",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("observer imports = %#v, want %#v", got, want)
	}
}

func generated(t *testing.T, generate func() ([]byte, error)) []byte {
	t.Helper()
	contents, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func relationProductDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate relation product test source")
	}
	return filepath.Dir(source)
}
