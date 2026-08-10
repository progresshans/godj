package codegen_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectBridgeIsCanonicalAndImportsOnlyAppsAndORM(t *testing.T) {
	t.Parallel()

	input := []codegen.BridgePackage{
		{Alias: "blog", ImportPath: "example.com/relation/blog/models"},
		{Alias: "authors", ImportPath: "example.com/relation/authors/models"},
	}
	first, err := codegen.GenerateProjectBridge("binding", input)
	if err != nil {
		t.Fatalf("GenerateProjectBridge() error = %v", err)
	}
	second, err := codegen.GenerateProjectBridge("binding", []codegen.BridgePackage{input[1], input[0]})
	if err != nil {
		t.Fatalf("GenerateProjectBridge() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("bridge input order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectBindingGeneratorVersion = "godj-codegen-rel-project-v1"`),
		[]byte("func Bind() (orm.ProjectBinding, error)"),
		[]byte(`"github.com/progresshans/godj/orm"`),
		[]byte(`authors "example.com/relation/authors/models"`),
		[]byte(`blog "example.com/relation/blog/models"`),
		[]byte("authors.GoDjRelationSchema()"),
		[]byte("blog.GoDjRelationSchema()"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("bridge source does not contain %q:\n%s", fragment, first)
		}
	}
	if bytes.Index(first, []byte("authors.GoDjRelationSchema()")) > bytes.Index(first, []byte("blog.GoDjRelationSchema()")) {
		t.Fatalf("bridge calls are not in canonical alias order:\n%s", first)
	}
	for _, forbidden := range [][]byte{
		[]byte("schema/ir"),
		[]byte("schema.Target"),
		[]byte("ForeignKeyRelation"),
		[]byte("encoding/json"),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("bridge source contains forbidden relation duplication %q:\n%s", forbidden, first)
		}
	}

	input[0].Alias = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation input mutation changed bridge candidate bytes")
	}
}

func TestGenerateProjectBridgeZeroProject(t *testing.T) {
	t.Parallel()

	generated, err := codegen.GenerateProjectBridge("binding", nil)
	if err != nil {
		t.Fatalf("GenerateProjectBridge() error = %v", err)
	}
	if !bytes.Contains(generated, []byte("return orm.BindProject()")) {
		t.Fatalf("zero bridge does not bind an empty project:\n%s", generated)
	}
}

func TestGenerateProjectBridgeRejectsInvalidOrAmbiguousImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pkg      string
		packages []codegen.BridgePackage
	}{
		{name: "invalid package", pkg: "bad-package"},
		{name: "blank package identifier", pkg: "_"},
		{name: "invalid alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "bad-alias", ImportPath: "example.com/app"}}},
		{name: "blank alias", pkg: "binding", packages: []codegen.BridgePackage{{ImportPath: "example.com/app"}}},
		{name: "init alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "init", ImportPath: "example.com/app"}}},
		{name: "reserved orm alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "orm", ImportPath: "example.com/app"}}},
		{name: "fixed function collision", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "Bind", ImportPath: "example.com/app"}}},
		{name: "duplicate alias", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/one"}, {Alias: "app", ImportPath: "example.com/two"}}},
		{name: "duplicate path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "one", ImportPath: "example.com/app"}, {Alias: "two", ImportPath: "example.com/app"}}},
		{name: "blank path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app"}}},
		{name: "space in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/bad path"}}},
		{name: "compiler punctuation in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "unsafe!x"}}},
		{name: "replacement rune in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/\ufffd"}}},
		{name: "invalid utf8 in path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: string([]byte{0xff})}}},
		{name: "reserved go path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "go"}}},
		{name: "reserved type path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "type"}}},
		{name: "leading dash path", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "-example/app"}}},
		{name: "empty path element", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com//app"}}},
		{name: "windows reserved element", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/con.txt"}}},
		{name: "windows short name element", pkg: "binding", packages: []codegen.BridgePackage{{Alias: "app", ImportPath: "example.com/app~1"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codegen.GenerateProjectBridge(test.pkg, test.packages); err == nil {
				t.Fatal("GenerateProjectBridge() accepted invalid input")
			}
		})
	}
}

func TestPureByteGeneratorsDoNotWriteCommittedSentinelsOnValidationFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	authors, blog := relationGenerationSchemas()
	blog.Models[0].Fields[1].Relation.Target.AppLabel = "bad-target"
	if _, err := codegen.Generate("models", blog); err == nil {
		t.Fatal("Generate() accepted invalid relation candidate")
	}
	if _, err := codegen.GenerateRelationMetadata("models", blog); err == nil {
		t.Fatal("GenerateRelationMetadata() accepted invalid relation candidate")
	}
	if _, err := codegen.GenerateProjectBridge("binding", []codegen.BridgePackage{
		{Alias: "authors", ImportPath: "example.com/app"},
		{Alias: "authors", ImportPath: "example.com/other"},
	}); err == nil {
		t.Fatal("GenerateProjectBridge() accepted invalid candidate set")
	}
	_ = authors

	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}

func TestGeneratedMixedV2V3ProjectCompilesBindsAndReturnsFreshSchemas(t *testing.T) {
	authors, blog := relationGenerationSchemas()
	validateGeneratedRelationProject(t, authors, blog, 2, 2)
}

func TestGeneratedMutualAndSelfRelationProjectHasZeroAppImportEdges(t *testing.T) {
	authors, blog := mutualRelationGenerationSchemas()
	validateGeneratedRelationProject(t, authors, blog, 4, 4)
}

func validateGeneratedRelationProject(t *testing.T, authorsSchema, blogSchema ir.Schema, wantForward, wantReverse int) {
	t.Helper()

	authorsMain, err := codegen.Generate("authors", authorsSchema)
	if err != nil {
		t.Fatalf("generate authors main: %v", err)
	}
	authorsCompanion, err := codegen.GenerateRelationMetadata("authors", authorsSchema)
	if err != nil {
		t.Fatalf("generate authors companion: %v", err)
	}
	blogMain, err := codegen.Generate("blog", blogSchema)
	if err != nil {
		t.Fatalf("generate blog main: %v", err)
	}
	blogCompanion, err := codegen.GenerateRelationMetadata("blog", blogSchema)
	if err != nil {
		t.Fatalf("generate blog companion: %v", err)
	}
	bridge, err := codegen.GenerateProjectBridge("binding", []codegen.BridgePackage{
		{Alias: "blog", ImportPath: "example.com/godj-relation-project/blog"},
		{Alias: "authors", ImportPath: "example.com/godj-relation-project/authors"},
	})
	if err != nil {
		t.Fatalf("generate project bridge: %v", err)
	}

	for name, source := range map[string][]byte{
		"authors main":      authorsMain,
		"authors companion": authorsCompanion,
	} {
		if bytes.Contains(source, []byte("example.com/godj-relation-project/blog")) {
			t.Fatalf("%s has an authors -> blog generated import edge:\n%s", name, source)
		}
	}
	for name, source := range map[string][]byte{
		"blog main":      blogMain,
		"blog companion": blogCompanion,
	} {
		if bytes.Contains(source, []byte("example.com/godj-relation-project/authors")) {
			t.Fatalf("%s has a blog -> authors generated import edge:\n%s", name, source)
		}
	}

	root := codegenRepositoryRoot(t)
	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module example.com/godj-relation-project

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(root))))
	writeGeneratedTestFile(t, directory, "authors/zz_godj_generated.go", authorsMain)
	writeGeneratedTestFile(t, directory, "authors/zz_godj_relation.go", authorsCompanion)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_generated.go", blogMain)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation.go", blogCompanion)
	writeGeneratedTestFile(t, directory, "binding/zz_godj_binding.go", bridge)
	writeGeneratedTestFile(t, directory, "binding/binding_test.go", []byte(fmt.Sprintf(`package binding_test

import (
	"testing"

	"example.com/godj-relation-project/binding"
	"example.com/godj-relation-project/blog"
)

func TestGeneratedProjectBinding(t *testing.T) {
	bound, err := binding.Bind()
	if err != nil {
		t.Fatalf("Bind() error = %%v", err)
	}
	if got, want := len(bound.ForwardRelations()), %d; got != want {
		t.Fatalf("forward relations = %%d, want %%d", got, want)
	}
	if got, want := len(bound.ReverseRelations()), %d; got != want {
		t.Fatalf("reverse relations = %%d, want %%d", got, want)
	}

	first := blog.GoDjRelationSchema()
	second := blog.GoDjRelationSchema()
	if first.Models[0].Fields[1].Relation != nil {
		first.Models[0].Fields[1].Relation.Target.AppLabel = "mutated"
		if second.Models[0].Fields[1].Relation.Target.AppLabel == "mutated" {
			t.Fatal("GoDjRelationSchema returned aliased relation pointers")
		}
	}
	first.Models[0].Fields[0].Name = "mutated"
	if second.Models[0].Fields[0].Name == "mutated" {
		t.Fatal("GoDjRelationSchema returned aliased model fields")
	}
}
`, wantForward, wantReverse)))

	validateGeneratedImportGraph(t, directory)

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated external project did not compile and bind: %v\n%s", err, output)
	}
}

func validateGeneratedImportGraph(t *testing.T, directory string) {
	t.Helper()

	command := exec.Command("go", "list", "-mod=mod", "-json", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list generated external project imports: %v\n%s", err, output)
	}

	type listedPackage struct {
		ImportPath string
		Imports    []string
		Deps       []string
	}
	packages := make(map[string]listedPackage)
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed listedPackage
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode generated go list output: %v", err)
		}
		packages[listed.ImportPath] = listed
	}

	const (
		authorsPath = "example.com/godj-relation-project/authors"
		blogPath    = "example.com/godj-relation-project/blog"
		bindingPath = "example.com/godj-relation-project/binding"
		ormPath     = "github.com/progresshans/godj/orm"
	)
	authors, ok := packages[authorsPath]
	if !ok {
		t.Fatalf("go list omitted %s", authorsPath)
	}
	blog, ok := packages[blogPath]
	if !ok {
		t.Fatalf("go list omitted %s", blogPath)
	}
	binding, ok := packages[bindingPath]
	if !ok {
		t.Fatalf("go list omitted %s", bindingPath)
	}
	for _, check := range []struct {
		name      string
		listed    listedPackage
		forbidden string
	}{
		{name: "authors -> blog", listed: authors, forbidden: blogPath},
		{name: "blog -> authors", listed: blog, forbidden: authorsPath},
	} {
		if slices.Contains(check.listed.Imports, check.forbidden) {
			t.Errorf("generated app direct import edge exists: %s", check.name)
		}
		if slices.Contains(check.listed.Deps, check.forbidden) {
			t.Errorf("generated app dependency edge exists: %s", check.name)
		}
	}

	wantBridgeImports := []string{authorsPath, blogPath, ormPath}
	for _, required := range wantBridgeImports {
		if !slices.Contains(binding.Imports, required) {
			t.Errorf("generated bridge does not directly import %s: %v", required, binding.Imports)
		}
	}
	for _, imported := range binding.Imports {
		if !slices.Contains(wantBridgeImports, imported) {
			t.Errorf("generated bridge has unexpected direct import %s", imported)
		}
	}
}

func mutualRelationGenerationSchemas() (ir.Schema, ir.Schema) {
	authors, blog := relationGenerationSchemas()
	authors.FormatVersion = ir.RelationFormatVersion
	authors.Models[0].Fields = append(authors.Models[0].Fields,
		ir.Field{
			Name:     "favorite_post",
			GoName:   "FavoritePostID",
			Kind:     ir.FieldForeignKey,
			Nullable: true,
			Relation: &ir.ForeignKeyRelation{
				Target:      ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
				Cardinality: ir.RelationManyToOne,
				Reverse:     ir.ReverseRelation{Name: "favored_by"},
				OnDelete:    ir.DeleteSetNull,
			},
		},
		ir.Field{
			Name:     "manager",
			GoName:   "ManagerID",
			Kind:     ir.FieldForeignKey,
			Nullable: true,
			Relation: &ir.ForeignKeyRelation{
				Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
				Cardinality: ir.RelationManyToOne,
				Reverse:     ir.ReverseRelation{Name: "reports"},
				OnDelete:    ir.DeleteSetNull,
			},
		},
	)
	return authors, blog
}

func codegenRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve codegen test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), ".."))
}

func writeGeneratedTestFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create generated fixture directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write generated fixture %s: %v", name, err)
	}
}

func generatedTestEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GOTOOLCHAIN=") || strings.HasPrefix(entry, "GOPROXY=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off")
}
