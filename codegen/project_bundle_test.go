package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen/internal/testschema"
)

func TestProjectBundleCanonicalRosterAndImmutableAccessors(t *testing.T) {
	spec := projectBundleTestSpec()
	bundle, err := GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	files := bundle.Files()
	if got, want := len(files), 16; got != want {
		t.Fatalf("len(Files()) = %d, want %d", got, want)
	}
	wantPaths := []string{
		"authors/zz_godj_generated.go",
		"authors/zz_godj_relation.go",
		"authors/zz_godj_relation_object.go",
		"authors/zz_godj_relation_projection.go",
		"blog/zz_godj_generated.go",
		"blog/zz_godj_relation.go",
		"blog/zz_godj_relation_object.go",
		"blog/zz_godj_relation_projection.go",
		"project/zz_godj_bindings.go",
		"project/zz_godj_relation_delete.go",
		"project/zz_godj_relation_facade.go",
		"project/zz_godj_relation_object.go",
		"project/zz_godj_relation_prefetch.go",
		"project/zz_godj_relation_query.go",
		"project/zz_godj_relation_reverse.go",
		"project/zz_godj_relation_select_related.go",
	}
	for index, file := range files {
		if file.Path != wantPaths[index] {
			t.Fatalf("Files()[%d].Path = %q, want %q", index, file.Path, wantPaths[index])
		}
		if file.Path == GeneratedManifestPath {
			t.Fatal("Files() includes manifest commit marker")
		}
		if file.Mode != 0o644 {
			t.Fatalf("Files()[%d].Mode = %o, want 0644", index, file.Mode)
		}
		source := file.Source()
		sum := sha256.Sum256(source)
		if got := hex.EncodeToString(sum[:]); got != file.SHA256 {
			t.Fatalf("Files()[%d].SHA256 = %q, computed %q", index, file.SHA256, got)
		}
	}

	originalPath := files[0].Path
	originalSource := files[0].Source()
	files[0].Path = "mutated.go"
	returnedSource := files[1].Source()
	returnedSource[0] ^= 0xff
	manifest := bundle.Manifest()
	manifest[0] ^= 0xff
	if bundle.Files()[0].Path != originalPath || !bytes.Equal(bundle.Files()[0].Source(), originalSource) {
		t.Fatal("Files() mutation changed immutable bundle state")
	}
	if bundle.Files()[1].Source()[0] == returnedSource[0] {
		t.Fatal("GeneratedFile.Source() returned mutable bundle storage")
	}
	if bundle.Manifest()[0] == manifest[0] {
		t.Fatal("Manifest() returned mutable bundle storage")
	}
}

func TestGenerateProjectCanonicalizesAppPermutationAndSnapshotsCaller(t *testing.T) {
	spec := projectBundleTestSpec()
	first, err := GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	permuted := projectBundleTestSpec()
	permuted.Apps[0], permuted.Apps[1] = permuted.Apps[1], permuted.Apps[0]
	second, err := GenerateProject(permuted)
	if err != nil {
		t.Fatalf("GenerateProject(permuted) error = %v", err)
	}
	if first.SnapshotSHA256() != second.SnapshotSHA256() || !bytes.Equal(first.Manifest(), second.Manifest()) {
		t.Fatal("app permutation changed project snapshot or manifest")
	}
	assertProjectBundleFilesEqual(t, first.Files(), second.Files())

	spec.Apps[0].Schema.Models[0].Fields[0].GoName = "Mutated"
	spec.Apps[1].Alias = "mutated"
	if !bytes.Equal(first.Manifest(), second.Manifest()) {
		t.Fatal("caller mutation changed retained bundle")
	}
}

func TestProjectBundleSupportsRootProjectAndRepeatedPackageNames(t *testing.T) {
	spec := projectBundleTestSpec()
	spec.Project.Directory = "."
	spec.Apps[0].Package.PackageName = "models"
	spec.Apps[1].Package.PackageName = "models"
	bundle, err := GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	if got := projectBundleFile(t, bundle, "zz_godj_bindings.go").Owner; got != "project" {
		t.Fatalf("root project binding owner = %q, want project", got)
	}
	for _, file := range bundle.Files() {
		if strings.HasPrefix(file.Path, "./") {
			t.Fatalf("root project output is not canonical: %q", file.Path)
		}
	}
}

func TestGenerateProjectSupportsEmptyAppUniverse(t *testing.T) {
	bundle, err := GenerateProject(ProjectSpec{Project: PackageSpec{
		PackageName: "project",
		ImportPath:  "example.com/empty-project/project",
		Directory:   "project",
	}})
	if err != nil {
		t.Fatalf("GenerateProject(empty) error = %v", err)
	}
	if got := len(bundle.Files()); got != 8 {
		t.Fatalf("len(Files()) = %d, want 8 project files", got)
	}
}

func TestGenerateProjectRejectsReservedAppIdentitiesBeforeBundle(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*AppSpec)
	}{
		{"runtime import", func(app *AppSpec) { app.Package.ImportPath = "github.com/progresshans/godj/orm" }},
		{"standard library import", func(app *AppSpec) { app.Package.ImportPath = "context" }},
		{"generated facade alias", func(app *AppSpec) { app.Alias = "reflect" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := projectBundleTestSpec()
			test.mutate(&spec.Apps[0])
			bundle, err := GenerateProject(spec)
			if err == nil || len(bundle.Files()) != 0 || len(bundle.Manifest()) != 0 {
				t.Fatalf("reserved app identity produced bundle: files=%d manifest=%d error=%v", len(bundle.Files()), len(bundle.Manifest()), err)
			}
		})
	}
}

func TestProjectBundleAppSourcesHaveNoDirectAppImports(t *testing.T) {
	spec := projectBundleTestSpec()
	bundle, err := GenerateProject(spec)
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	appImports := make(map[string]struct{}, len(spec.Apps))
	for _, app := range spec.Apps {
		appImports[app.Package.ImportPath] = struct{}{}
	}
	for _, file := range bundle.Files() {
		if !strings.HasPrefix(file.Owner, "app:") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source(), parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse app-owned output %s: %v", file.Path, err)
		}
		for _, imported := range parsed.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("decode import in %s: %v", file.Path, err)
			}
			if _, isAppImport := appImports[importPath]; isAppImport {
				t.Fatalf("app-owned output %s directly imports app package %q", file.Path, importPath)
			}
		}
	}
}

func TestProjectBundleProducerLimitsUseExactBoundaries(t *testing.T) {
	if err := validateProjectGeneratedFileCount(maxProjectGeneratedFiles); err != nil {
		t.Fatalf("maximum generated file count rejected: %v", err)
	}
	if err := validateProjectGeneratedFileCount(maxProjectGeneratedFiles + 1); err == nil {
		t.Fatal("generated file count above maximum was accepted")
	}
	if err := validateProjectGeneratedPath(strings.Repeat("a", maxProjectGeneratedPathBytes)); err != nil {
		t.Fatalf("maximum generated path rejected: %v", err)
	}
	if err := validateProjectGeneratedPath(strings.Repeat("a", maxProjectGeneratedPathBytes+1)); err == nil {
		t.Fatal("generated path above maximum was accepted")
	}
	if err := validateProjectGeneratedSourceSize("generated.go", maxProjectGeneratedSourceBytes); err != nil {
		t.Fatalf("maximum generated source size rejected: %v", err)
	}
	if err := validateProjectGeneratedSourceSize("generated.go", maxProjectGeneratedSourceBytes+1); err == nil {
		t.Fatal("generated source above maximum was accepted")
	}
}

func assertProjectBundleFilesEqual(t *testing.T, left, right []GeneratedFile) {
	t.Helper()
	if len(left) != len(right) {
		t.Fatalf("file lengths differ: %d != %d", len(left), len(right))
	}
	for index := range left {
		if left[index].Path != right[index].Path || left[index].Owner != right[index].Owner ||
			left[index].SHA256 != right[index].SHA256 || left[index].Mode != right[index].Mode ||
			!bytes.Equal(left[index].Source(), right[index].Source()) {
			t.Fatalf("generated file %d differs after canonical permutation", index)
		}
	}
}

func projectBundleTestSpec() ProjectSpec {
	authors, blog := testschema.Bundle()
	return ProjectSpec{
		Project: PackageSpec{PackageName: "project", ImportPath: "example.com/godj-project-bundle/project", Directory: "project"},
		Apps: []AppSpec{
			{Alias: "blog", Package: PackageSpec{PackageName: "blog", ImportPath: "example.com/godj-project-bundle/blog", Directory: "blog"}, Schema: blog},
			{Alias: "authors", Package: PackageSpec{PackageName: "authors", ImportPath: "example.com/godj-project-bundle/authors", Directory: "authors"}, Schema: authors},
		},
	}
}

func projectBundleFile(t *testing.T, bundle GeneratedBundle, name string) GeneratedFile {
	t.Helper()
	for _, file := range bundle.Files() {
		if file.Path == name {
			return file
		}
	}
	t.Fatalf("bundle file %q not found", name)
	return GeneratedFile{}
}
