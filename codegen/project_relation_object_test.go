package codegen_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationObjectIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	packages := relationObjectGenerationPackages(authors, blog)
	first, err := codegen.GenerateProjectRelationObject("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationObject() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationObject("project", []codegen.RelationObjectPackage{packages[1], packages[0]})
	if err != nil {
		t.Fatalf("GenerateProjectRelationObject() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("project relation object package order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "relation_object", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation object golden: %v\ngenerated:\n%s", err, first)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation object bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}
	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationObjectGeneratorVersion = "godj-codegen-rel-object-project-v1"`),
		[]byte(`context "context"`),
		[]byte(`db "github.com/progresshans/godj/db"`),
		[]byte(`orm "github.com/progresshans/godj/orm"`),
		[]byte(`ir "github.com/progresshans/godj/schema/ir"`),
		[]byte(`query "github.com/progresshans/godj/query"`),
		[]byte("type BlogPostReviewerObjectRelation struct"),
		[]byte("func (_relation BlogPostReviewerObjectRelation) IsNull(_value bool) orm.Predicate[blog.Post]"),
		[]byte("type BlogPostObjectFactory struct"),
		[]byte("Reviewer BlogPostReviewerObjectRelation"),
		[]byte("func (_factory BlogPostObjectFactory) ParseDynamic("),
		[]byte("func (_factory BlogPostObjectFactory) From(_backend db.Queryer, _value blog.Post) (*BlogPostObject, error)"),
		[]byte("type BlogPostObject struct"),
		[]byte("_self    *BlogPostObject"),
		[]byte("func (_object *BlogPostObject) _validate() error"),
		[]byte("func (_object *BlogPostObject) Model() (blog.Post, error)"),
		[]byte("func (_object *BlogPostObject) Author(_ctx context.Context) (authors.Author, error)"),
		[]byte("func (_object *BlogPostObject) Reviewer(_ctx context.Context) (authors.Author, bool, error)"),
		[]byte("func (_object *BlogPostObject) Fresh() (*BlogPostObject, error)"),
		[]byte("type Objects struct"),
		[]byte("BlogPost BlogPostObjectFactory"),
		[]byte("func BindObjects() (Objects, error)"),
		[]byte(`orm.BindRequiredForwardObject(_model1, "author", _model0)`),
		[]byte(`orm.BindNullableForwardObject(_model1, "reviewer", _model0)`),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("project relation object source does not contain %q:\n%s", fragment, first)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("GoDjRelationSchema"),
		[]byte("ForeignKeyRelation"),
		[]byte(`"blog_post"`),
		[]byte(`"author_id"`),
		[]byte("reflect."),
		[]byte("panic("),
		[]byte("func init("),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("project relation object source contains forbidden schema replay %q:\n%s", forbidden, first)
		}
	}
	packages[1].Schema.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated project object bytes")
	}
}

func TestGenerateProjectRelationObjectRejectsInvalidInputsAndNamespaces(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	valid := relationObjectGenerationPackages(authors, blog)
	falseAlias := relationObjectGenerationPackages(authors, blog)
	falseAlias[0].Alias = "false"
	collisionAuthors, collisionBlog := relationObjectProjectNamespaceCollisionSchemas()
	tests := []struct {
		name     string
		pkg      string
		packages []codegen.RelationObjectPackage
		contains string
	}{
		{name: "invalid generated package", pkg: "bad-package", packages: valid},
		{name: "uppercase alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "Blog", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "underscore alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "my_blog", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "unicode alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "블로그", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "keyword alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "for", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "init alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "init", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved context alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "context", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved db alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "db", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved orm alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "orm", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved ir alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "ir", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved query alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "query", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared bool alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "bool", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared error alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "error", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared false alias", pkg: "project", packages: falseAlias, contains: `invalid relation object package alias "false"`},
		{name: "predeclared nil alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "nil", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "invalid import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "blog", ImportPath: "example.com/bad path", Schema: blog}}},
		{name: "reserved context import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "blog", ImportPath: "context", Schema: blog}}},
		{name: "reserved db import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/db", Schema: blog}}},
		{name: "reserved orm import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/orm", Schema: blog}}},
		{name: "reserved ir import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/schema/ir", Schema: blog}}},
		{name: "reserved query import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/query", Schema: blog}}},
		{name: "duplicate alias", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "app", ImportPath: "example.com/a", Schema: authors}, {Alias: "app", ImportPath: "example.com/b", Schema: blog}}},
		{name: "duplicate import path", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "authors", ImportPath: "example.com/app", Schema: authors}, {Alias: "blog", ImportPath: "example.com/app", Schema: blog}}},
		{name: "duplicate app label", pkg: "project", packages: []codegen.RelationObjectPackage{{Alias: "authors", ImportPath: "example.com/a", Schema: authors}, {Alias: "other", ImportPath: "example.com/b", Schema: authors}}},
		{name: "unresolved target", pkg: "project", packages: []codegen.RelationObjectPackage{valid[1]}},
		{name: "missing relation ID selector", pkg: "project", packages: relationObjectPackagesWithFieldGoName(authors, blog, 2, "Author")},
		{name: "Model method collision", pkg: "project", packages: relationObjectPackagesWithFieldGoName(authors, blog, 2, "ModelID")},
		{name: "Fresh method collision", pkg: "project", packages: relationObjectPackagesWithFieldGoName(authors, blog, 2, "FreshID")},
		{name: "nullable From field collision", pkg: "project", packages: relationObjectPackagesWithFieldGoName(authors, blog, 3, "FromID")},
		{name: "nullable ParseDynamic field collision", pkg: "project", packages: relationObjectPackagesWithFieldGoName(authors, blog, 3, "ParseDynamicID")},
		{name: "project query and object type collision", pkg: "project", packages: relationObjectGenerationPackages(collisionAuthors, collisionBlog)},
		{name: "derived surface collision", pkg: "project", packages: collidingRelationObjectSurfaces()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := codegen.GenerateProjectRelationObject(test.pkg, test.packages)
			if err == nil {
				t.Fatal("GenerateProjectRelationObject() accepted invalid input")
			}
			if len(generated) != 0 {
				t.Fatalf("GenerateProjectRelationObject() returned %d bytes for invalid input", len(generated))
			}
			if test.contains != "" && !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("GenerateProjectRelationObject() error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestGenerateProjectRelationObjectZeroProjectUsesNoUnusedImports(t *testing.T) {
	t.Parallel()

	generated, err := codegen.GenerateProjectRelationObject("project", nil)
	if err != nil {
		t.Fatalf("GenerateProjectRelationObject() error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte("type Objects struct"),
		[]byte("if _, _err := Bind(); _err != nil"),
		[]byte("return Objects{}, nil"),
	} {
		if !bytes.Contains(generated, fragment) {
			t.Fatalf("zero project source does not contain %q:\n%s", fragment, generated)
		}
	}
	if bytes.Contains(generated, []byte("import")) {
		t.Fatalf("zero project source contains unused imports:\n%s", generated)
	}
}

func TestGeneratedRelationObjectProjectCompilesBindsAndHasNoAppEdges(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-object-project"
	directory := writeGeneratedRelationObjectProject(t, modulePath, "authors", "blog", authors, blog, true)
	validateGeneratedRelationObjectImportGraph(t, directory, modulePath, "authors", "blog")

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation object project did not compile and bind: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationObjectConsumesUnrelatedBoundModels(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
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

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated project with an unrelated bound model did not compile: %v\n%s", err, output)
	}
}

func TestGeneratedRelationObjectValidateSelectorCompilesWithPrivateSelfCheck(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	blog.Models[0].Fields[2].GoName = "ValidateID"
	const modulePath = "example.com/godj-relation-object-validate-selector"
	directory := writeGeneratedRelationObjectProject(t, modulePath, "authors", "blog", authors, blog, false)
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Validate relation selector did not compile beside the private self-check: %v\n%s", err, output)
	}
}

func TestProjectRelationObjectGeneratorNeverWritesOnFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	authors, blog := relationQueryGenerationSchemas()
	if _, err := codegen.GenerateProjectRelationObject(
		"project",
		relationObjectPackagesWithFieldGoName(authors, blog, 2, "Author"),
	); err == nil {
		t.Fatal("GenerateProjectRelationObject() accepted invalid selector")
	}
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}

func relationObjectGenerationPackages(authors, blog ir.Schema) []codegen.RelationObjectPackage {
	return []codegen.RelationObjectPackage{
		{Alias: "authors", ImportPath: "example.com/godj-relation-object-project/authors", Schema: authors},
		{Alias: "blog", ImportPath: "example.com/godj-relation-object-project/blog", Schema: blog},
	}
}

func relationObjectPackagesWithFieldGoName(authors, blog ir.Schema, fieldIndex int, goName string) []codegen.RelationObjectPackage {
	blog = blog.Clone()
	blog.Models[0].Fields[fieldIndex].GoName = goName
	return relationObjectGenerationPackages(authors, blog)
}

func relationObjectProjectNamespaceCollisionSchemas() (ir.Schema, ir.Schema) {
	authors, blog := relationQueryGenerationSchemas()
	blog.Models[0].Fields[2].GoName = "ReviewerObjectID"
	return authors, blog
}

func collidingRelationObjectSurfaces() []codegen.RelationObjectPackage {
	queryPackages := collidingRelationQuerySurfaces()
	result := make([]codegen.RelationObjectPackage, len(queryPackages))
	for index, candidate := range queryPackages {
		result[index] = codegen.RelationObjectPackage(candidate)
	}
	return result
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

	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))
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
	command := exec.Command("go", "list", "-mod=mod", "-json", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
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

func TestRelationObjectPackageAliasesStayASCIIAndReserved(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	for _, alias := range []string{"r", "binding", "err", "model0", "relation0", "related0", "snapshot"} {
		packages := relationObjectGenerationPackages(authors, blog)
		packages[1].Alias = alias
		packages[1].ImportPath = "example.com/" + strings.ToLower(alias)
		if _, err := codegen.GenerateProjectRelationObject("project", packages); err != nil {
			t.Fatalf("GenerateProjectRelationObject() rejected safe alias %q: %v", alias, err)
		}
	}
}

func TestGeneratedProjectRelationObjectAdversarialAliasesCompile(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
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
			command := exec.Command("go", "test", "-mod=mod", "./...")
			command.Dir = directory
			command.Env = generatedTestEnvironment()
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated project aliases target=%q source=%q did not compile: %v\n%s", test.target, test.source, err, output)
			}
		})
	}
}
