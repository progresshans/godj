package codegen_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationSelectRelatedIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	packages := relationSelectRelatedPackages("example.com/godj-relation-select-related", "authors", "blog", authors, blog)
	first, err := codegen.GenerateProjectRelationSelectRelated("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationSelectRelated() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationSelectRelated("project", []codegen.RelationObjectPackage{packages[1], packages[0]})
	if err != nil {
		t.Fatalf("GenerateProjectRelationSelectRelated() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("project relation select-related package order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "relation_select_related", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation select-related golden: %v\ngenerated:\n%s", err, first)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation select-related bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}
	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationSelectRelatedGeneratorVersion = "godj-codegen-rel-select-related-project-v1"`),
		[]byte("var _ orm.ProjectionDescriptor[authors.Author] = authors.AuthorDescriptor{}"),
		[]byte("var _ orm.ProjectionDescriptor[blog.Post] = blog.PostDescriptor{}"),
		[]byte("type BlogPostSelectRelated struct"),
		[]byte("func (_factory BlogPostObjectFactory) SelectRelated(_source orm.QuerySet[blog.Post]) BlogPostSelectRelated"),
		[]byte("type BlogPostAuthorSelectRelatedQuery struct"),
		[]byte("type BlogPostReviewerSelectRelatedQuery struct"),
		[]byte(`orm.ResolveForwardSelectPath(_selection.factory.model, "author")`),
		[]byte("orm.BindRequiredForwardSelect(_path, _selection.factory.author)"),
		[]byte(`orm.ResolveForwardSelectPath(_selection.factory.model, "reviewer")`),
		[]byte("orm.BindNullableForwardSelect(_path, _selection.factory.reviewer)"),
		[]byte("_selected, _err := _query.query.All(_ctx)"),
		[]byte("_object.author = _related"),
		[]byte("_object.reviewer = _related"),
		[]byte("type BlogPostDynamicSelectRelatedQuery struct"),
		[]byte("func (_selection BlogPostSelectRelated) ParseDynamic(_path string)"),
		[]byte(`case "author":`),
		[]byte(`case "reviewer":`),
		[]byte("func (_query BlogPostDynamicSelectRelatedQuery) All(_ctx context.Context)"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("project relation select-related source does not contain %q:\n%s", fragment, first)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("BindSelectRelated"),
		[]byte("type SelectRelated struct"),
		[]byte("func Bind"),
		[]byte(".Author().Reviewer()"),
		[]byte("interface{}"),
		[]byte("reflect."),
		[]byte("panic("),
		[]byte("func init("),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("project relation select-related source contains forbidden %q:\n%s", forbidden, first)
		}
	}
	wantExported := []string{
		"BlogPostAuthorSelectRelatedQuery",
		"BlogPostDynamicSelectRelatedQuery",
		"BlogPostReviewerSelectRelatedQuery",
		"BlogPostSelectRelated",
		"GoDjProjectRelationSelectRelatedGeneratorVersion",
	}
	if got := exportedDeclarations(t, "project_relation_select_related.go", first); !slices.Equal(got, wantExported) {
		t.Fatalf("project relation select-related exported declarations = %v, want %v", got, wantExported)
	}
	packages[1].Schema.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated project select-related bytes")
	}
}

func TestGenerateProjectRelationSelectRelatedRejectsInvalidInputsAndNamespaces(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	valid := relationSelectRelatedPackages("example.com/godj-relation-select-related", "authors", "blog", authors, blog)
	selectRelatedCollision := blog.Clone()
	selectRelatedCollision.Models[0].Fields[3].GoName = "SelectRelatedID"
	requiredSelectRelatedCollision := blog.Clone()
	requiredSelectRelatedCollision.Models[0].Fields[2].GoName = "SelectRelatedID"
	parseDynamicCollision := blog.Clone()
	parseDynamicCollision.Models[0].Fields[2].GoName = "ParseDynamicID"
	dynamicKindCollision := blog.Clone()
	dynamicKindCollision.Models[0].Fields[2].GoName = "KindID"
	projectionCollision := authors.Clone()
	projectionCollision.Models[0].GoName = "GoDjRelationProjectionGeneratorVersion"
	sqlAlias := relationSelectRelatedPackages("example.com/godj-relation-select-related", "sql", "blog", authors, blog)
	lenAlias := relationSelectRelatedPackages("example.com/godj-relation-select-related", "authors", "len", authors, blog)
	databaseSQLPath := relationSelectRelatedPackages("example.com/godj-relation-select-related", "authors", "blog", authors, blog)
	databaseSQLPath[0].ImportPath = "database/sql"
	for _, test := range []struct {
		name     string
		pkg      string
		packages []codegen.RelationObjectPackage
		contains string
	}{
		{name: "invalid generated package", pkg: "bad-package", packages: valid},
		{name: "missing target package", pkg: "project", packages: valid[1:]},
		{name: "reserved sql alias", pkg: "project", packages: sqlAlias, contains: "sql"},
		{name: "used predeclared len alias", pkg: "project", packages: lenAlias, contains: "len"},
		{name: "reserved database sql path", pkg: "project", packages: databaseSQLPath, contains: "database/sql"},
		{
			name: "projection prerequisite collision",
			pkg:  "project",
			packages: relationSelectRelatedPackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				projectionCollision,
				blog,
			),
			contains: "GoDjRelationProjectionGeneratorVersion",
		},
		{
			name: "factory SelectRelated collision",
			pkg:  "project",
			packages: relationSelectRelatedPackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				authors,
				selectRelatedCollision,
			),
			contains: "SelectRelated",
		},
		{
			name: "required SelectRelated collision",
			pkg:  "project",
			packages: relationSelectRelatedPackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				authors,
				requiredSelectRelatedCollision,
			),
			contains: "SelectRelated",
		},
		{
			name: "builder ParseDynamic collision",
			pkg:  "project",
			packages: relationSelectRelatedPackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				authors,
				parseDynamicCollision,
			),
			contains: "ParseDynamic",
		},
		{
			name: "dynamic discriminator collision",
			pkg:  "project",
			packages: relationSelectRelatedPackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				authors,
				dynamicKindCollision,
			),
			contains: "kind",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := codegen.GenerateProjectRelationSelectRelated(test.pkg, test.packages)
			if err == nil {
				t.Fatal("GenerateProjectRelationSelectRelated() accepted invalid input")
			}
			if len(generated) != 0 {
				t.Fatalf("invalid input returned %d partial bytes", len(generated))
			}
			if test.contains != "" && !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error %q does not identify %q", err, test.contains)
			}
		})
	}
}

func TestGenerateProjectRelationSelectRelatedPreservesOldBytesAndLastGood(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-select-related-old-lock"
	packages := relationSelectRelatedPackages(modulePath, "authors", "blog", authors, blog)
	oldBindingBefore := mustGeneratedCode(t, "project binding before", func() ([]byte, error) {
		return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
			{Alias: "authors", ImportPath: modulePath + "/authors"},
			{Alias: "blog", ImportPath: modulePath + "/blog"},
		})
	})
	oldObjectBefore := mustGeneratedCode(t, "project object before", func() ([]byte, error) {
		return codegen.GenerateProjectRelationObject("project", packages)
	})
	oldAuthorsProjectionBefore := mustGeneratedCode(t, "authors projection before", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("authors", authors)
	})
	oldBlogProjectionBefore := mustGeneratedCode(t, "blog projection before", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("blog", blog)
	})
	if _, err := codegen.GenerateProjectRelationSelectRelated("project", packages); err != nil {
		t.Fatalf("GenerateProjectRelationSelectRelated() error = %v", err)
	}
	oldBindingAfter := mustGeneratedCode(t, "project binding after", func() ([]byte, error) {
		return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
			{Alias: "authors", ImportPath: modulePath + "/authors"},
			{Alias: "blog", ImportPath: modulePath + "/blog"},
		})
	})
	oldObjectAfter := mustGeneratedCode(t, "project object after", func() ([]byte, error) {
		return codegen.GenerateProjectRelationObject("project", packages)
	})
	oldAuthorsProjectionAfter := mustGeneratedCode(t, "authors projection after", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("authors", authors)
	})
	oldBlogProjectionAfter := mustGeneratedCode(t, "blog projection after", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("blog", blog)
	})
	before := [][]byte{oldBindingBefore, oldObjectBefore, oldAuthorsProjectionBefore, oldBlogProjectionBefore}
	after := [][]byte{oldBindingAfter, oldObjectAfter, oldAuthorsProjectionAfter, oldBlogProjectionAfter}
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("new project select-related generation changed prerequisite byte stream %d", index)
		}
	}

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if _, err := codegen.GenerateProjectRelationSelectRelated("bad-package", packages); err == nil {
		t.Fatal("GenerateProjectRelationSelectRelated() accepted invalid package")
	}
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}

func TestGeneratedProjectRelationSelectRelatedExactTwelveFileUnionCompiles(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-select-related-union"
	directory, files := writeGeneratedRelationSelectRelatedProject(
		t,
		modulePath,
		"authors",
		"blog",
		authors,
		blog,
		true,
	)
	if len(files) != 12 {
		t.Fatalf("generated union has %d files, want exact 12: %v", len(files), files)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated exact twelve-file select-related union did not compile or pass: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationSelectRelatedAdversarialAliasesCompile(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	for _, test := range []struct {
		name   string
		target string
		source string
	}{
		{name: "binding locals", target: "err", source: "model0"},
		{name: "query locals", target: "value", source: "selection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			modulePath := "example.com/godj-relation-select-related-alias-" + strings.ReplaceAll(test.name, " ", "-")
			directory, files := writeGeneratedRelationSelectRelatedProject(
				t,
				modulePath,
				test.target,
				test.source,
				authors,
				blog,
				true,
			)
			if len(files) != 12 {
				t.Fatalf("generated adversarial union has %d files, want exact 12", len(files))
			}
			command := exec.Command("go", "test", "-mod=mod", "./...")
			command.Dir = directory
			command.Env = generatedTestEnvironment()
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("generated aliases target=%q source=%q did not compile: %v\n%s", test.target, test.source, err, output)
			}
		})
	}
}

func TestGeneratedProjectRelationSelectRelatedMissingProjectionPrerequisiteFailsCompile(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-select-related-missing-projection"
	directory, _ := writeGeneratedRelationSelectRelatedProject(
		t,
		modulePath,
		"authors",
		"blog",
		authors,
		blog,
		false,
	)
	missing := filepath.Join(directory, "target", "zz_godj_relation_projection.go")
	if err := os.Remove(missing); err != nil {
		t.Fatalf("remove exact temporary projection prerequisite: %v", err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("generated union without target projection companion unexpectedly compiled")
	}
	if !bytes.Contains(output, []byte("ProjectionDescriptor")) && !bytes.Contains(output, []byte("NewProjectionScan")) {
		t.Fatalf("missing projection failure does not identify the prerequisite ABI:\n%s", output)
	}
}

func relationSelectRelatedPackages(
	modulePath, targetAlias, sourceAlias string,
	authors, blog ir.Schema,
) []codegen.RelationObjectPackage {
	return []codegen.RelationObjectPackage{
		{Alias: targetAlias, ImportPath: modulePath + "/target", Schema: authors},
		{Alias: sourceAlias, ImportPath: modulePath + "/source", Schema: blog},
	}
}

func writeGeneratedRelationSelectRelatedProject(
	t *testing.T,
	modulePath, targetPackage, sourcePackage string,
	authors, blog ir.Schema,
	includeExternalTest bool,
) (string, []string) {
	t.Helper()
	authorsMain, err := codegen.Generate(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate target main: %v", err)
	}
	authorsMetadata, err := codegen.GenerateRelationMetadata(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate target metadata: %v", err)
	}
	authorsObject, err := codegen.GenerateRelationObject(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate target object: %v", err)
	}
	authorsProjection, err := codegen.GenerateRelationProjection(targetPackage, authors)
	if err != nil {
		t.Fatalf("generate target projection: %v", err)
	}
	blogMain, err := codegen.Generate(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate source main: %v", err)
	}
	blogMetadata, err := codegen.GenerateRelationMetadata(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate source metadata: %v", err)
	}
	blogQuery, err := codegen.GenerateRelationQuery(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate source query: %v", err)
	}
	blogObject, err := codegen.GenerateRelationObject(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate source object: %v", err)
	}
	blogProjection, err := codegen.GenerateRelationProjection(sourcePackage, blog)
	if err != nil {
		t.Fatalf("generate source projection: %v", err)
	}
	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: targetPackage, ImportPath: modulePath + "/target"},
		{Alias: sourcePackage, ImportPath: modulePath + "/source"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	packages := relationSelectRelatedPackages(modulePath, targetPackage, sourcePackage, authors, blog)
	projectObject, err := codegen.GenerateProjectRelationObject("project", packages)
	if err != nil {
		t.Fatalf("generate project object: %v", err)
	}
	projectSelectRelated, err := codegen.GenerateProjectRelationSelectRelated("project", packages)
	if err != nil {
		t.Fatalf("generate project select-related: %v", err)
	}

	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))
	files := []struct {
		name string
		data []byte
	}{
		{name: "target/zz_godj_generated.go", data: authorsMain},
		{name: "target/zz_godj_relation.go", data: authorsMetadata},
		{name: "target/zz_godj_relation_object.go", data: authorsObject},
		{name: "target/zz_godj_relation_projection.go", data: authorsProjection},
		{name: "source/zz_godj_generated.go", data: blogMain},
		{name: "source/zz_godj_relation.go", data: blogMetadata},
		{name: "source/zz_godj_relation_query.go", data: blogQuery},
		{name: "source/zz_godj_relation_object.go", data: blogObject},
		{name: "source/zz_godj_relation_projection.go", data: blogProjection},
		{name: "project/zz_godj_binding.go", data: projectBinding},
		{name: "project/zz_godj_relation_object.go", data: projectObject},
		{name: "project/zz_godj_relation_select_related.go", data: projectSelectRelated},
	}
	names := make([]string, len(files))
	for index, file := range files {
		writeGeneratedTestFile(t, directory, file.name, file.data)
		names[index] = file.name
	}
	if includeExternalTest {
		surface := strings.ToUpper(sourcePackage[:1]) + sourcePackage[1:] + "Post"
		writeGeneratedTestFile(
			t,
			directory,
			"project/relation_select_related_external_test.go",
			generatedRelationSelectRelatedExternalTest(modulePath, surface),
		)
	}
	return directory, names
}

func generatedRelationSelectRelatedExternalTest(modulePath, surface string) []byte {
	result := []byte(fmt.Sprintf(`package project_test

import (
	"context"
	"errors"
	"testing"

	project "%s/project"
	source "%s/source"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type countingBackend struct{ queries int }

func (backend *countingBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.queries++
	return nil, errors.New("stop after generated query assembly")
}

func TestGeneratedSelectRelatedSurfaceAndDynamicValidation(t *testing.T) {
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatalf("BindObjects() error = %%v", err)
	}
	backend := &countingBackend{}
	sourceQuery := source.PostObjects.Using(backend).OrderBy(source.PostFields.ID.Asc())
	selected := objects.BlogPost.SelectRelated(sourceQuery)

	if _, err := selected.Author().All(context.Background()); err == nil {
		t.Fatal("typed Author All unexpectedly succeeded")
	}
	if backend.queries != 1 {
		t.Fatalf("typed Author queries = %%d, want 1", backend.queries)
	}
	if _, err := selected.Reviewer().All(context.Background()); err == nil {
		t.Fatal("typed Reviewer All unexpectedly succeeded")
	}
	if backend.queries != 2 {
		t.Fatalf("typed Reviewer queries = %%d, want 2", backend.queries)
	}

	dynamic, err := selected.ParseDynamic("author")
	if err != nil {
		t.Fatalf("ParseDynamic(author) error = %%v", err)
	}
	if _, err := dynamic.All(context.Background()); err == nil {
		t.Fatal("dynamic Author All unexpectedly succeeded")
	}
	if backend.queries != 3 {
		t.Fatalf("dynamic Author queries = %%d, want 3", backend.queries)
	}

	for _, path := range []string{"", " ", "posts", "reviewed_posts", "unknown", "author__name"} {
		before := backend.queries
		_, err := selected.ParseDynamic(path)
		if !errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidRelatedPath, Field: path}) {
			t.Fatalf("ParseDynamic(%%q) error = %%v", path, err)
		}
		if backend.queries != before {
			t.Fatalf("ParseDynamic(%%q) performed I/O", path)
		}
	}

	var zero project.BlogPostDynamicSelectRelatedQuery
	if _, err := zero.All(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero dynamic All error = %%v", err)
	}
}
`, modulePath, modulePath))
	return bytes.ReplaceAll(result, []byte("BlogPost"), []byte(surface))
}
