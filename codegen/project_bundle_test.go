package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
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

func TestGenerateProjectFullUnionCompilesAndMixedSnapshotFails(t *testing.T) {
	baseline, err := GenerateProject(projectBundleTestSpec())
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	baselineRoot := writeProjectBundleModule(t, baseline)
	if output, err := compileProjectBundleModule(baselineRoot); err != nil {
		t.Fatalf("full generated union does not compile: %v\n%s", err, output)
	}

	changedSpec := projectBundleTestSpec()
	changedSpec.Apps[1].Schema.Models[0].Fields[0].MaxLength++
	changed, err := GenerateProject(changedSpec)
	if err != nil {
		t.Fatalf("GenerateProject(changed) error = %v", err)
	}
	if changed.SnapshotSHA256() == baseline.SnapshotSHA256() {
		t.Fatal("schema change did not change project snapshot")
	}
	changedRoot := writeProjectBundleModule(t, changed)
	if output, err := compileProjectBundleModule(changedRoot); err != nil {
		t.Fatalf("changed generated union does not compile: %v\n%s", err, output)
	}

	for _, oldFile := range baseline.Files() {
		oldFile := oldFile
		t.Run(oldFile.Path, func(t *testing.T) {
			mixedRoot := writeProjectBundleModule(t, changed)
			writeProjectBundleTestFile(t, filepath.Join(mixedRoot, filepath.FromSlash(oldFile.Path)), oldFile.Source())
			if output, err := compileProjectBundleModule(mixedRoot); err == nil {
				t.Fatalf("mixed snapshot unexpectedly compiled\n%s", output)
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
			Name:   "blog_post",
			GoName: "BlogPost",
			Fields: []ir.Field{
				{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
				{
					Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "blog_posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
				{
					Name: "reviewer", GoName: "ReviewerID", Kind: ir.FieldForeignKey, Nullable: true,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "reviewed_posts"},
						OnDelete:    ir.DeleteSetNull,
					},
				},
			},
		}},
	}
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

func writeProjectBundleModule(t *testing.T, bundle GeneratedBundle) string {
	t.Helper()
	directory := t.TempDir()
	root := projectBundleRepositoryRoot(t)
	writeProjectBundleTestFile(t, filepath.Join(directory, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-project-bundle

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(root))))
	for _, file := range bundle.Files() {
		writeProjectBundleTestFile(t, filepath.Join(directory, filepath.FromSlash(file.Path)), file.Source())
	}
	writeProjectBundleTestFile(t, filepath.Join(directory, "consumer", "consumer.go"), []byte(`package consumer

import project "example.com/godj-project-bundle/project"

var _ = project.GoDjProjectRelationFacadeGeneratorVersion
`))
	return directory
}

func compileProjectBundleModule(directory string) ([]byte, error) {
	command := exec.Command("go", "test", "./...")
	command.Dir = directory
	command.Env = projectBundleTestEnvironment(os.Environ())
	return command.CombinedOutput()
}

func projectBundleTestEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"GOWORK": {}, "GOTOOLCHAIN": {}, "GOPROXY": {}, "GOSUMDB": {},
	}
	result := make([]string, 0, len(environment)+4)
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if _, remove := blocked[name]; found && remove {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
}

func projectBundleRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve project bundle test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func writeProjectBundleTestFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", filename, err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}
