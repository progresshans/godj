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
		[]byte(`const GoDjProjectRelationSelectRelatedGeneratorVersion = "godj-codegen-rel-select-related-project-current-v1"`),
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
		[]byte("configurationErr error"),
		[]byte("return BlogPostAuthorSelectRelatedQuery{configurationErr: _err}"),
		[]byte("return BlogPostReviewerSelectRelatedQuery{configurationErr: _err}"),
		[]byte("_, _contextErr := (orm.ForwardSelectQuery[blog.Post, authors.Author]{}).All(_ctx)"),
		[]byte("_terminalErr, _ok := _contextErr.(*query.Error)"),
		[]byte("_terminalErr.Category == query.CategoryBackend && _terminalErr.Code == query.CodeInvalidPlan"),
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

func TestGenerateProjectRelationSelectRelatedLeavesCurrentPrerequisitesStableAndPreservesLastGood(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-select-related-current-stability"
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

func TestGeneratedProjectRelationSelectRelatedExactElevenFileUnionCompiles(t *testing.T) {
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
	if len(files) != 11 {
		t.Fatalf("generated union has %d files, want exact 11: %v", len(files), files)
	}
	writeGeneratedTestFile(
		t,
		directory,
		"project/relation_select_related_cause_test.go",
		generatedRelationSelectRelatedCauseTest(modulePath),
	)
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated exact twelve-file select-related union did not compile or pass: %v\n%s", err, output)
	}

	const facadeModulePath = "example.com/godj-relation-select-related-facade-cause"
	facadeDirectory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		facadeModulePath,
		relationFacadePackages(facadeModulePath, authors, blog),
		nil,
		generatedRelationSelectRelatedFacadeCauseTest(),
	)
	compileGeneratedRelationFacadeUniverse(t, facadeDirectory)
	verifyGeneratedRelationSelectRelatedStalePublicCause(t, authors, blog)
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
			if len(files) != 11 {
				t.Fatalf("generated adversarial union has %d files, want exact 11", len(files))
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
	var zeroSelection project.BlogPostSelectRelated
	beforeResolve := backend.queries
	if _, err := zeroSelection.Author().All(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("public zero-selection typed resolve error = %%v, want preserved query invalid-plan", err)
	}
	if backend.queries != beforeResolve {
		t.Fatalf("public zero-selection typed resolve performed %%d queries", backend.queries-beforeResolve)
	}

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

func generatedRelationSelectRelatedCauseTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project

import (
	"context"
	"errors"
	"testing"

	source %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type selectRelatedCauseBackend struct{ queries int }

func (backend *selectRelatedCauseBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.queries++
	return nil, errors.New("unexpected select-related cause backend I/O")
}

type selectRelatedTypedNilContext struct{ context.Context }

func TestGeneratedTypedSelectRelatedPreservesConfigurationCauseAndContextPrecedence(t *testing.T) {
	objects, err := BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	backend := &selectRelatedCauseBackend{}
	sourceQuery := source.PostObjects.Using(backend)
	valid := objects.BlogPost.SelectRelated(sourceQuery)

	requiredSelection := valid
	requiredSelection.factory.author = BlogPostObjectFactory{}.author
	nullableSelection := valid
	nullableSelection.factory.reviewer = BlogPostObjectFactory{}.reviewer

	resolveQuery := (BlogPostSelectRelated{}).Author()
	requiredQuery := requiredSelection.Author()
	nullableQuery := nullableSelection.Reviewer()
	tests := []struct {
		name    string
		cause   error
		all     func(context.Context) error
		dynamic func() error
	}{
		{
			name:  "resolve",
			cause: resolveQuery.configurationErr,
			all: func(ctx context.Context) error {
				_, err := resolveQuery.All(ctx)
				return err
			},
			dynamic: func() error {
				_, err := (BlogPostSelectRelated{}).ParseDynamic("author")
				return err
			},
		},
		{
			name:  "required bind",
			cause: requiredQuery.configurationErr,
			all: func(ctx context.Context) error {
				_, err := requiredQuery.All(ctx)
				return err
			},
			dynamic: func() error {
				_, err := requiredSelection.ParseDynamic("author")
				return err
			},
		},
		{
			name:  "nullable bind",
			cause: nullableQuery.configurationErr,
			all: func(ctx context.Context) error {
				_, err := nullableQuery.All(ctx)
				return err
			},
			dynamic: func() error {
				_, err := nullableSelection.ParseDynamic("reviewer")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.cause == nil {
				t.Fatal("typed builder did not store its configuration cause")
			}
			before := backend.queries
			if got := test.all(context.Background()); got != test.cause {
				t.Fatalf("background terminal error = %%v (%%p), want exact stored cause %%v (%%p)", got, got, test.cause, test.cause)
			}
			if backend.queries != before {
				t.Fatalf("background terminal performed %%d backend queries", backend.queries-before)
			}

			var nilContext context.Context
			if got := test.all(nilContext); !errors.Is(got, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || got == test.cause {
				t.Fatalf("nil-context terminal error = %%v, want context invalid-plan before stored cause", got)
			}
			var typedNil *selectRelatedTypedNilContext
			if got := test.all(typedNil); !errors.Is(got, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) || got == test.cause {
				t.Fatalf("typed-nil-context terminal error = %%v, want context invalid-plan before stored cause", got)
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if got := test.all(cancelled); !errors.Is(got, context.Canceled) || got == test.cause {
				t.Fatalf("cancelled-context terminal error = %%v, want context.Canceled before stored cause", got)
			}
			expired, expire := context.WithTimeout(context.Background(), -1)
			expire()
			if got := test.all(expired); !errors.Is(got, context.DeadlineExceeded) || got == test.cause {
				t.Fatalf("deadline-context terminal error = %%v, want context.DeadlineExceeded before stored cause", got)
			}
			if backend.queries != before {
				t.Fatalf("context precedence terminals performed %%d backend queries", backend.queries-before)
			}

			dynamicErr := test.dynamic()
			var dynamicQueryErr *query.Error
			var causeQueryErr *query.Error
			if !errors.As(dynamicErr, &dynamicQueryErr) || !errors.As(test.cause, &causeQueryErr) ||
				dynamicQueryErr.Category != causeQueryErr.Category || dynamicQueryErr.Code != causeQueryErr.Code ||
				dynamicQueryErr.Field != causeQueryErr.Field || dynamicQueryErr.Lookup != causeQueryErr.Lookup ||
				dynamicQueryErr.Detail != causeQueryErr.Detail || errors.Unwrap(dynamicErr) != errors.Unwrap(test.cause) {
				t.Fatalf("dynamic control error = %%v, want independently preserved cause equivalent to %%v", dynamicErr, test.cause)
			}
			if errors.Is(dynamicErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
				t.Fatalf("dynamic control degraded to backend invalid-plan: %%v", dynamicErr)
			}
			if backend.queries != before {
				t.Fatalf("dynamic control performed %%d backend queries", backend.queries-before)
			}
		})
	}
	sentinel := errors.New("stored select-related sentinel cause")
	injected := BlogPostAuthorSelectRelatedQuery{configurationErr: &query.Error{
		Category: query.CategoryQuery,
		Code:     query.CodeInvalidPlan,
		Field:    "author",
		Lookup:   "exact",
		Detail:   "injected typed configuration detail",
		Cause:    sentinel,
	}}
	_, injectedErr := injected.All(context.Background())
	if injectedErr != injected.configurationErr || !errors.Is(injectedErr, sentinel) {
		t.Fatalf("injected terminal error = %%v (%%p), want exact stored error %%v (%%p) with sentinel cause", injectedErr, injectedErr, injected.configurationErr, injected.configurationErr)
	}
	var injectedQueryErr *query.Error
	if !errors.As(injectedErr, &injectedQueryErr) || injectedQueryErr.Category != query.CategoryQuery ||
		injectedQueryErr.Code != query.CodeInvalidPlan || injectedQueryErr.Field != "author" ||
		injectedQueryErr.Lookup != "exact" || injectedQueryErr.Detail != "injected typed configuration detail" ||
		injectedQueryErr.Cause != sentinel {
		t.Fatalf("injected structured configuration error changed: %%#v", injectedQueryErr)
	}
	if backend.queries != 0 {
		t.Fatalf("injected configuration terminal performed %%d backend queries", backend.queries)
	}
}

func TestGeneratedSelectRelatedZeroAndCorruptQueriesKeepGenericErrors(t *testing.T) {
	var zeroTyped BlogPostAuthorSelectRelatedQuery
	if _, err := zeroTyped.All(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero typed terminal error = %%v, want backend invalid-plan", err)
	}
	var zeroDynamic BlogPostDynamicSelectRelatedQuery
	if _, err := zeroDynamic.All(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero dynamic terminal error = %%v, want query invalid-plan", err)
	}
	corruptDynamic := BlogPostDynamicSelectRelatedQuery{kind: 255}
	if _, err := corruptDynamic.All(context.Background()); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("corrupt dynamic terminal error = %%v, want query invalid-plan", err)
	}
}
`, modulePath+"/source"))
}

func generatedRelationSelectRelatedFacadeCauseTest() []byte {
	return []byte(`package project

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type selectRelatedFacadeCauseBackend struct{ calls int }

func (backend *selectRelatedFacadeCauseBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.calls++
	return nil, errors.New("unexpected facade cause query")
}

func (backend *selectRelatedFacadeCauseBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.calls++
	return 0, errors.New("unexpected facade cause insert")
}

func (backend *selectRelatedFacadeCauseBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.calls++
	return 0, errors.New("unexpected facade cause update")
}

func (backend *selectRelatedFacadeCauseBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.calls++
	return 0, errors.New("unexpected facade cause delete")
}

func TestProjectFacadePassesThroughTypedSelectRelatedConfigurationCauses(t *testing.T) {
	backend := &selectRelatedFacadeCauseBackend{}
	models, err := Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	selection := models.BlogPost.state.objects.BlogPost.SelectRelated(models.BlogPost.query)

	resolve := (BlogPostSelectRelated{}).Author()
	requiredSelection := selection
	requiredSelection.factory.author = BlogPostObjectFactory{}.author
	required := requiredSelection.Author()
	nullableSelection := selection
	nullableSelection.factory.reviewer = BlogPostObjectFactory{}.reviewer
	nullable := nullableSelection.Reviewer()

	tests := []struct {
		name  string
		cause error
		query BlogPostEagerQuery
	}{
		{
			name:  "resolve",
			cause: resolve.configurationErr,
			query: BlogPostEagerQuery{
				state: models.BlogPost.state, source: models.BlogPost.query, kind: 1, author: resolve,
			},
		},
		{
			name:  "required bind",
			cause: required.configurationErr,
			query: BlogPostEagerQuery{
				state: models.BlogPost.state, source: models.BlogPost.query, kind: 1, author: required,
			},
		},
		{
			name:  "nullable bind",
			cause: nullable.configurationErr,
			query: BlogPostEagerQuery{
				state: models.BlogPost.state, source: models.BlogPost.query, kind: 2, reviewer: nullable,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.cause == nil {
				t.Fatal("typed prerequisite did not store its configuration cause")
			}
			before := backend.calls
			if _, got := test.query.All(context.Background()); got != test.cause {
				t.Fatalf("facade eager terminal error = %v (%p), want exact low-level cause %v (%p)", got, got, test.cause, test.cause)
			}
			if backend.calls != before {
				t.Fatalf("facade cause pass-through performed %d backend calls", backend.calls-before)
			}
		})
	}
}
`)
}

func verifyGeneratedRelationSelectRelatedStalePublicCause(t *testing.T, authors, blog ir.Schema) {
	t.Helper()
	const modulePath = "example.com/godj-relation-select-related-stale-public-cause"
	directory, _ := writeGeneratedRelationFacadeUniverse(
		t,
		modulePath,
		relationFacadePackages(modulePath, authors, blog),
		nil,
		generatedRelationSelectRelatedStalePublicCauseTest(modulePath),
	)
	path := filepath.Join(directory, "project", "zz_godj_relation_select_related.go")
	canonical, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	authorResolver := []byte(`orm.ResolveForwardSelectPath(_selection.factory.model, "author")`)
	reviewerResolver := []byte(`orm.ResolveForwardSelectPath(_selection.factory.model, "reviewer")`)
	if bytes.Count(canonical, authorResolver) != 1 || bytes.Count(canonical, reviewerResolver) != 1 {
		t.Fatalf("canonical typed resolver counts = author %d reviewer %d, want 1/1", bytes.Count(canonical, authorResolver), bytes.Count(canonical, reviewerResolver))
	}
	placeholder := []byte(`orm.ResolveForwardSelectPath(_selection.factory.model, "godj_stale_swap")`)
	stale := bytes.Replace(canonical, authorResolver, placeholder, 1)
	stale = bytes.Replace(stale, reviewerResolver, authorResolver, 1)
	stale = bytes.Replace(stale, placeholder, reviewerResolver, 1)
	if bytes.Count(stale, authorResolver) != 1 || bytes.Count(stale, reviewerResolver) != 1 || bytes.Equal(stale, canonical) {
		t.Fatal("stale typed resolver swap did not produce the exact two-literal mutation")
	}
	if err := os.WriteFile(path, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	compileGeneratedRelationFacadeUniverse(t, directory)
}

func generatedRelationSelectRelatedStalePublicCauseTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project_test

import (
	"context"
	"errors"
	"testing"

	project %q
	source %q
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type staleSelectRelatedBackend struct{ calls int }

func (backend *staleSelectRelatedBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	backend.calls++
	return nil, errors.New("unexpected stale select-related query")
}

func (backend *staleSelectRelatedBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	backend.calls++
	return 0, errors.New("unexpected stale select-related insert")
}

func (backend *staleSelectRelatedBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	backend.calls++
	return 0, errors.New("unexpected stale select-related update")
}

func (backend *staleSelectRelatedBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	backend.calls++
	return 0, errors.New("unexpected stale select-related delete")
}

func TestStaleTypedCompanionPreservesPublicLowLevelAndFacadeBindCauses(t *testing.T) {
	backend := &staleSelectRelatedBackend{}
	objects, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	selected := objects.BlogPost.SelectRelated(source.PostObjects.Using(backend))
	models, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		all  func() error
	}{
		{name: "low-level required", all: func() error { _, err := selected.Author().All(context.Background()); return err }},
		{name: "low-level nullable", all: func() error { _, err := selected.Reviewer().All(context.Background()); return err }},
		{name: "facade required", all: func() error {
			_, err := models.BlogPost.SelectRelated(models.BlogPost.Related.Author).All(context.Background())
			return err
		}},
		{name: "facade nullable", all: func() error {
			_, err := models.BlogPost.SelectRelated(models.BlogPost.Related.Reviewer).All(context.Background())
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := backend.calls
			err := test.all()
			var queryErr *query.Error
			if !errors.As(err, &queryErr) || queryErr.Category != query.CategoryQuery || queryErr.Code != query.CodeInvalidPlan ||
				queryErr.Detail != "forward select path and object handle do not share one canonical project relation" || queryErr.Cause != nil {
				t.Fatalf("public stale-companion error = %%v, want exact structured bind cause", err)
			}
			if errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
				t.Fatalf("public stale-companion cause degraded to backend invalid-plan: %%v", err)
			}
			if backend.calls != before {
				t.Fatalf("public stale-companion terminal performed %%d backend calls", backend.calls-before)
			}
		})
	}
}
`, modulePath+"/project", modulePath+"/blog"))
}
