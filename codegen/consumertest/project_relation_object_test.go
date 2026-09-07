package codegen_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedRelationObjectProjectCompilesBindsAndHasNoAppEdges(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-object-project"
	directory := writeGeneratedRelationObjectProject(t, modulePath, "authors", "blog", authors, blog, true)
	validateGeneratedRelationObjectImportGraph(t, directory, modulePath, "authors", "blog")

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation object project did not compile and bind: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationObjectConsumesUnrelatedBoundModels(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	authors.Models = append(authors.Models, ir.Model{
		Name: "profile", GoName: "Profile", DBTable: "authors_profile",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldChar, MaxLength: 80},
		},
	})

	const modulePath = "example.com/godj-relation-object-unrelated"
	directory := writeGeneratedRelationObjectProject(t, modulePath, "authors", "blog", authors, blog, false)
	generated, err := os.ReadFile(filepath.Join(directory, "project", "zz_godj_relation_object.go"))
	if err != nil {
		t.Fatalf("read generated project relation object: %v", err)
	}
	if !bytes.Contains(generated, []byte("\t_ = _model1\n")) {
		t.Fatalf("unrelated bound model result is not consumed after validation:\n%s", generated)
	}
	if err := os.Remove(filepath.Join(directory, "project", "zz_godj_relation_query.go")); err != nil {
		t.Fatalf("remove unrelated project-query fixture from object-only compile proof: %v", err)
	}

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated project with an unrelated bound model did not compile: %v\n%s", err, output)
	}
}

func TestGeneratedRelationObjectValidateSelectorCompilesWithPrivateSelfCheck(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	blog.Models[0].Fields[2].GoName = "ValidateID"
	const modulePath = "example.com/godj-relation-object-validate-selector"
	directory := writeGeneratedRelationObjectProject(t, modulePath, "authors", "blog", authors, blog, false)
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Validate relation selector did not compile beside the private self-check: %v\n%s", err, output)
	}
}

func writeGeneratedRelationObjectProject(
	t *testing.T,
	modulePath, targetPackage, sourcePackage string,
	authors, blog ir.Schema,
	includeExternalTest bool,
) string {
	t.Helper()
	authorsMain, err := codegen.Generate(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate authors main: %v", err)
	}
	authorsMetadata, err := codegen.GenerateRelationMetadata(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate authors metadata: %v", err)
	}
	authorsObject, err := codegen.GenerateRelationObject(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate authors object companion: %v", err)
	}
	blogMain, err := codegen.Generate(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate blog main: %v", err)
	}
	blogMetadata, err := codegen.GenerateRelationMetadata(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate blog metadata: %v", err)
	}
	blogObject, err := codegen.GenerateRelationObject(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate blog object companion: %v", err)
	}
	packages := []codegen.RelationObjectPackage{
		{Alias: targetPackage, ImportPath: modulePath + "/target", Schema: authors},
		{Alias: sourcePackage, ImportPath: modulePath + "/source", Schema: blog},
	}
	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: targetPackage, ImportPath: modulePath + "/target"},
		{Alias: sourcePackage, ImportPath: modulePath + "/source"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	projectQuery, err := codegen.GenerateProjectRelationQuery("project", []codegen.RelationQueryPackage{
		{Alias: targetPackage, ImportPath: modulePath + "/target", Schema: authors},
		{Alias: sourcePackage, ImportPath: modulePath + "/source", Schema: blog},
	})
	if err != nil {
		t.Fatalf("generate project relation query: %v", err)
	}
	projectObject, err := codegen.GenerateProjectRelationObject("project", packages)
	if err != nil {
		t.Fatalf("generate project relation object: %v", err)
	}

	directory := newGeneratedModule(t, modulePath)
	writeGeneratedTestFile(t, directory, "target/zz_godj_generated.go", authorsMain)
	writeGeneratedTestFile(t, directory, "target/zz_godj_relation.go", authorsMetadata)
	writeGeneratedTestFile(t, directory, "target/zz_godj_relation_object.go", authorsObject)
	writeGeneratedTestFile(t, directory, "source/zz_godj_generated.go", blogMain)
	writeGeneratedTestFile(t, directory, "source/zz_godj_relation.go", blogMetadata)
	writeGeneratedTestFile(t, directory, "source/zz_godj_relation_object.go", blogObject)
	writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", projectBinding)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_query.go", projectQuery)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_object.go", projectObject)
	if includeExternalTest {
		surface := strings.ToUpper(sourcePackage[:1]) + sourcePackage[1:] + "Post"
		writeGeneratedTestFile(t, directory, "project/relation_object_test.go", generatedRelationObjectExternalTest(modulePath, surface))
	}
	return directory
}

func generatedRelationObjectExternalTest(modulePath, surface string) []byte {
	result := []byte(fmt.Sprintf(`package project_test

import (
	"context"
	"errors"
	"testing"

	source "%s/source"
	project "%s/project"
	target "%s/target"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type neverBackend struct{}

func (*neverBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unexpected query")
}

func assertInvalidPlan(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("error = %%v, want query invalid-plan", err)
	}
}

func TestGeneratedRelationObjects(t *testing.T) {
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatalf("BindObjects() error = %%v", err)
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatalf("BindRelations() error = %%v", err)
	}
	typed := source.PostObjects.Using(&neverBackend{}).Filter(
		relations.BlogPost.Author.Name.Exact("Ada"),
		objects.BlogPost.Reviewer.IsNull(true),
	).Plan()
	dynamicPredicates, err := objects.BlogPost.ParseDynamic(nil, []orm.LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "reviewer__isnull", Value: true},
	})
	if err != nil {
		t.Fatalf("ParseDynamic() error = %%v", err)
	}
	dynamic := source.PostObjects.Using(&neverBackend{}).Filter(dynamicPredicates...).Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed and dynamic object plans differ\ntyped = %%#v\ndynamic = %%#v", typed, dynamic)
	}

	if storage, ok := (target.AuthorDescriptor{}).BindRelationStorage(target.GoDjRelationSchema().Models[0].Fields[0]); ok || storage != nil {
		t.Fatal("relation-free target descriptor exposed relation storage")
	}
	metadata := source.GoDjRelationSchema()
	authorField := metadata.Models[0].Fields[2]
	authorStorage, ok := (source.PostDescriptor{}).BindRelationStorage(authorField)
	if !ok || authorStorage == nil {
		t.Fatal("required relation storage was not bound")
	}
	authorValue, ok := authorStorage.Value(source.Post{AuthorID: 7})
	if integer, exact := authorValue.Integer(); !ok || !exact || integer != 7 {
		t.Fatalf("required relation value = %%#v, ok=%%v", authorValue, ok)
	}
	reviewerField := metadata.Models[0].Fields[3]
	reviewerStorage, ok := (source.PostDescriptor{}).BindRelationStorage(reviewerField)
	if !ok || reviewerStorage == nil {
		t.Fatal("nullable relation storage was not bound")
	}
	nullValue, ok := reviewerStorage.Value(source.Post{})
	if !ok || !nullValue.IsNull() {
		t.Fatalf("nullable absent relation value = %%#v, ok=%%v", nullValue, ok)
	}
	reviewerID := int64(2)
	reviewerValue, ok := reviewerStorage.Value(source.Post{ReviewerID: &reviewerID})
	if integer, exact := reviewerValue.Integer(); !ok || !exact || integer != 2 {
		t.Fatalf("nullable present relation value = %%#v, ok=%%v", reviewerValue, ok)
	}
	mutated := reviewerField.Clone()
	mutated.Column = "wrong"
	if storage, ok := (source.PostDescriptor{}).BindRelationStorage(mutated); ok || storage != nil {
		t.Fatal("relation storage accepted non-canonical field metadata")
	}

	post := source.Post{ID: 10, Title: "Alpha", AuthorID: 1, ReviewerID: &reviewerID}
	object, err := objects.BlogPost.From(&neverBackend{}, post)
	if err != nil {
		t.Fatalf("From() error = %%v", err)
	}
	first, err := object.Model()
	if err != nil {
		t.Fatalf("Model() error = %%v", err)
	}
	*first.ReviewerID = 99
	second, err := object.Model()
	if err != nil || second.ReviewerID == nil || *second.ReviewerID != 2 {
		t.Fatalf("Model() clone = %%#v, err=%%v", second, err)
	}
	fresh, err := object.Fresh()
	if err != nil || fresh == nil || fresh == object {
		t.Fatalf("Fresh() = %%p, err=%%v", fresh, err)
	}
	copyValue := *object
	_, err = (&copyValue).Model()
	assertInvalidPlan(t, err)
	_, err = new(project.BlogPostObject).Model()
	assertInvalidPlan(t, err)
	var nilObject *project.BlogPostObject
	_, err = nilObject.Model()
	assertInvalidPlan(t, err)
	if _, err := objects.BlogPost.From(nil, post); err == nil {
		t.Fatal("From() accepted nil backend")
	}
	var typedNil *neverBackend
	if _, err := objects.BlogPost.From(typedNil, post); err == nil {
		t.Fatal("From() accepted typed-nil backend")
	}
}
`, modulePath, modulePath, modulePath))
	return bytes.ReplaceAll(result, []byte("BlogPost"), []byte(surface))
}

func validateGeneratedRelationObjectImportGraph(
	t *testing.T,
	directory, modulePath, targetPackage, sourcePackage string,
) {
	t.Helper()
	command := generatedGoCommand(t.Context(), directory, "list", "-mod=mod", "-json", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list generated relation object project imports: %v\n%s", err, output)
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
	targetPath := modulePath + "/target"
	sourcePath := modulePath + "/source"
	projectPath := modulePath + "/project"
	target := packages[targetPath]
	source := packages[sourcePath]
	project := packages[projectPath]
	if slices.Contains(target.Imports, sourcePath) || slices.Contains(target.Deps, sourcePath) {
		t.Errorf("generated %s app has a direct/dependency edge to %s", targetPackage, sourcePackage)
	}
	if slices.Contains(source.Imports, targetPath) || slices.Contains(source.Deps, targetPath) {
		t.Errorf("generated %s app has a direct/dependency edge to %s", sourcePackage, targetPackage)
	}
	wantProjectImports := []string{
		"context",
		targetPath,
		sourcePath,
		"github.com/progresshans/godj/db",
		"github.com/progresshans/godj/orm",
		"github.com/progresshans/godj/query",
		"github.com/progresshans/godj/schema/ir",
	}
	for _, required := range wantProjectImports {
		if !slices.Contains(project.Imports, required) {
			t.Errorf("generated project does not directly import %s: %v", required, project.Imports)
		}
	}
	for _, imported := range project.Imports {
		if !slices.Contains(wantProjectImports, imported) {
			t.Errorf("generated project has unexpected direct import %s", imported)
		}
	}
}

func TestGeneratedProjectRelationObjectAdversarialAliasesCompile(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	for _, test := range []struct {
		name   string
		target string
		source string
	}{
		{name: "binding locals", target: "err", source: "model0"},
		{name: "method locals", target: "value", source: "f"},
	} {
		t.Run(test.name, func(t *testing.T) {
			modulePath := "example.com/godj-relation-object-alias-" + strings.ReplaceAll(test.name, " ", "-")
			directory := writeGeneratedRelationObjectProject(t, modulePath, test.target, test.source, authors, blog, true)
			command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated project aliases target=%q source=%q did not compile: %v\n%s", test.target, test.source, err, output)
			}
		})
	}
}
