package codegen_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedCurrentRelationProjectCompilesBindsAndReturnsFreshSchemas(t *testing.T) {
	authors, blog := testschema.Relation()
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

	directory := newGeneratedModule(t, "example.com/godj-relation-project")
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

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated external project did not compile and bind: %v\n%s", err, output)
	}
}

func validateGeneratedImportGraph(t *testing.T, directory string) {
	t.Helper()

	command := generatedGoCommand(t.Context(), directory, "list", "-mod=mod", "-json", "./...")
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
	authors, blog := testschema.Relation()
	authors.FormatVersion = ir.CurrentFormatVersion
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
