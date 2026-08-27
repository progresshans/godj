//go:build darwin || linux

package compiletest

import (
	"context"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
	"github.com/progresshans/godj/schema/ir"
)

// TestProjectBundleCandidateExternalSurface keeps the orchestration contract
// honest from a package outside projectgenerate: a virtual new package must
// compile before any generated target exists, then the exact committed bytes
// must pass the read-only check.
func TestProjectBundleCandidateExternalSurface(t *testing.T) {
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{
		PackageName: "project",
		ImportPath:  "example.com/godj-project-bundle/project",
		Directory:   "project",
	}})
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	root := t.TempDir()
	stage := t.TempDir()
	goModTemplate, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "compiletest", "testdata", "project_bundle", "go.mod.txt"))
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "compiletest", "testdata", "project_bundle", "consumer.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeProjectBundleCompileFile(t, root, "go.mod", []byte(fmt.Sprintf(string(goModTemplate), filepath.ToSlash(repositoryRoot(t)))))
	writeProjectBundleCompileFile(t, root, "consumer/consumer.go", consumer)
	writeProjectBundleCompileBundle(t, stage, bundle)

	verifier, err := projectgenerate.NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatalf("NewGoCandidateVerifier() error = %v", err)
	}
	if err := verifier.Verify(context.Background(), stage); err != nil {
		t.Fatalf("Verify(virtual bundle) error = %v", err)
	}
	for _, file := range bundle.Files() {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); !os.IsNotExist(err) {
			t.Fatalf("Verify published target %q", file.Path)
		}
	}

	writeProjectBundleCompileBundle(t, root, bundle)
	report, err := projectgenerate.Check(context.Background(), root, bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("Check(committed bundle) report=%#v error=%v", report, err)
	}
}

func TestProjectBundleCanonicalFacadeExternalSurface(t *testing.T) {
	bundle, err := codegen.GenerateProject(canonicalFacadeCompileSpec())
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	root := t.TempDir()
	stage := t.TempDir()
	testdata := filepath.Join(repositoryRoot(t), "internal", "compiletest", "testdata", "project_bundle")
	goModTemplate, err := os.ReadFile(filepath.Join(testdata, "go.mod.txt"))
	if err != nil {
		t.Fatal(err)
	}
	methods, err := os.ReadFile(filepath.Join(testdata, "canonical_facade_methods.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := os.ReadFile(filepath.Join(testdata, "canonical_facade_consumer.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalFacadeMethodSource(methods); err != nil {
		t.Fatalf("validate canonical facade app methods: %v", err)
	}
	if err := validateCanonicalFacadeConsumerSource(consumer); err != nil {
		t.Fatalf("validate canonical facade external consumer: %v", err)
	}
	writeProjectBundleCompileFile(t, root, "go.mod", []byte(fmt.Sprintf(string(goModTemplate), filepath.ToSlash(repositoryRoot(t)))))
	writeProjectBundleCompileFile(t, root, "blog/methods.go", methods)
	writeProjectBundleCompileFile(t, root, "consumer/consumer.go", consumer)
	writeProjectBundleCompileBundle(t, stage, bundle)

	verifier, err := projectgenerate.NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatalf("NewGoCandidateVerifier() error = %v", err)
	}
	if err := verifier.Verify(context.Background(), stage); err != nil {
		t.Fatalf("Verify(canonical facade virtual bundle) error = %v", err)
	}
	for _, file := range bundle.Files() {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); !os.IsNotExist(err) {
			t.Fatalf("Verify published canonical facade target %q", file.Path)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(codegen.GeneratedManifestPath))); !os.IsNotExist(err) {
		t.Fatalf("Verify published canonical facade manifest")
	}

	writeProjectBundleCompileBundle(t, root, bundle)
	report, err := projectgenerate.Check(context.Background(), root, bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("Check(canonical facade committed bundle) report=%#v error=%v", report, err)
	}
}

func validateCanonicalFacadeMethodSource(source []byte) error {
	file, err := parseRelationFacadeSource("methods.go", source, "blog")
	if err != nil {
		return err
	}
	want := map[string]bool{
		"Post.DisplayTitle:value":     true,
		"Post.NormalizeTitle:pointer": true,
	}
	seen := make(map[string]bool, len(want))
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			return fmt.Errorf("canonical facade method source contains %T declaration", declaration)
		}
		key, err := relationFacadeFunctionKey(function)
		if err != nil {
			return err
		}
		pointer := false
		if function.Recv != nil && len(function.Recv.List) == 1 {
			_, pointer = function.Recv.List[0].Type.(*ast.StarExpr)
		}
		if pointer {
			key += ":pointer"
		} else {
			key += ":value"
		}
		if !want[key] || seen[key] {
			return fmt.Errorf("canonical facade method source contains forbidden or duplicate %q", key)
		}
		seen[key] = true
	}
	if len(seen) != len(want) {
		return fmt.Errorf("canonical facade method source inventory = %d, want %d", len(seen), len(want))
	}
	return nil
}

func validateCanonicalFacadeConsumerSource(source []byte) error {
	file, err := parseRelationFacadeSource("canonical_facade_consumer.go", source, "consumer")
	if err != nil {
		return err
	}
	wantCalls := map[string]int{
		"models.BlogPost.Filter": 1,
		"post.DisplayTitle":      1,
		"post.NormalizeTitle":    1,
		"post.WithAuthor":        1,
		"post.WithAuthorID":      1,
		"post.Author":            1,
		"post.Save":              1,
		"post.Unwrap":            1,
		"json.Marshal":           1,
		"json.Unmarshal":         1,
		"loaded.NormalizeTitle":  1,
	}
	seenCalls := make(map[string]int, len(wantCalls))
	directTitle := 0
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.CallExpr:
			path := relationFacadeSelectorPath(node.Fun)
			if wantCalls[path] != 0 {
				seenCalls[path]++
			}
		case *ast.SelectorExpr:
			if relationFacadeSelectorPath(node) == "post.Title" {
				directTitle++
			}
		}
		return true
	})
	for path, want := range wantCalls {
		if got := seenCalls[path]; got != want {
			return fmt.Errorf("canonical facade consumer call %s count = %d, want %d", path, got, want)
		}
	}
	if directTitle != 2 {
		return fmt.Errorf("canonical facade consumer direct scalar post.Title count = %d, want 2", directTitle)
	}
	return nil
}

func canonicalFacadeCompileSpec() codegen.ProjectSpec {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "authors",
		Models: []ir.Model{{
			Name:   "author",
			GoName: "Author",
			Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100}},
		}},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []ir.Field{
				{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"},
		Apps: []codegen.AppSpec{
			{Alias: "blog", Package: codegen.PackageSpec{PackageName: "blog", ImportPath: "example.com/godj-project-bundle/blog", Directory: "blog"}, Schema: blog},
			{Alias: "authors", Package: codegen.PackageSpec{PackageName: "authors", ImportPath: "example.com/godj-project-bundle/authors", Directory: "authors"}, Schema: authors},
		},
	}
}

func writeProjectBundleCompileBundle(t *testing.T, root string, bundle codegen.GeneratedBundle) {
	t.Helper()
	for _, file := range bundle.Files() {
		writeProjectBundleCompileFile(t, root, file.Path, file.Source())
	}
	writeProjectBundleCompileFile(t, root, codegen.GeneratedManifestPath, bundle.Manifest())
}

func writeProjectBundleCompileFile(t *testing.T, root, relative string, contents []byte) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
