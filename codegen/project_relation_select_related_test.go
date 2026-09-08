package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/internal/testschema"
)

func TestGenerateProjectRelationSelectRelatedIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	packages := testfixture.TargetSourcePackages("example.com/godj-relation-select-related", "authors", "blog", authors, blog)
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
		[]byte(`const GoDjProjectRelationSelectRelatedGeneratorVersion = "godj-codegen-rel-select-related-project-current-v2"`),
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
		[]byte("_selected, _err := _query.query.WithConfigurationError(_query.configurationErr).All(_ctx)"),
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

	authors, blog := testschema.QueryRelation()
	valid := testfixture.TargetSourcePackages("example.com/godj-relation-select-related", "authors", "blog", authors, blog)
	selectRelatedCollision := blog.Clone()
	selectRelatedCollision.Models[0].Fields[3].GoName = "SelectRelatedID"
	parseDynamicCollision := blog.Clone()
	parseDynamicCollision.Models[0].Fields[2].GoName = "ParseDynamicID"
	projectionCollision := authors.Clone()
	projectionCollision.Models[0].GoName = "GoDjRelationProjectionGeneratorVersion"
	sqlAlias := testfixture.TargetSourcePackages("example.com/godj-relation-select-related", "sql", "blog", authors, blog)
	lenAlias := testfixture.TargetSourcePackages("example.com/godj-relation-select-related", "authors", "len", authors, blog)
	anyAlias := testfixture.TargetSourcePackages("example.com/godj-relation-select-related", "authors", "any", authors, blog)
	databaseSQLPath := testfixture.TargetSourcePackages("example.com/godj-relation-select-related", "authors", "blog", authors, blog)
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
		{name: "generic any constraint alias", pkg: "project", packages: anyAlias, contains: "any"},
		{name: "reserved database sql path", pkg: "project", packages: databaseSQLPath, contains: "database/sql"},
		{
			name: "projection prerequisite collision",
			pkg:  "project",
			packages: testfixture.TargetSourcePackages(
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
			packages: testfixture.TargetSourcePackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				authors,
				selectRelatedCollision,
			),
			contains: "SelectRelated",
		},
		{
			name: "builder ParseDynamic collision",
			pkg:  "project",
			packages: testfixture.TargetSourcePackages(
				"example.com/godj-relation-select-related-collision",
				"authors",
				"blog",
				authors,
				parseDynamicCollision,
			),
			contains: "ParseDynamic",
		},
		{
			name: "shared terminal interface collision",
			pkg:  "project",
			packages: testfixture.TargetSourcePackages(
				"example.com/godj-relation-select-related-collision",
				"relationSelectQuery",
				"blog",
				authors,
				blog,
			),
			contains: "relationSelectQuery",
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

	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-select-related-current-stability"
	packages := testfixture.TargetSourcePackages(modulePath, "authors", "blog", authors, blog)
	oldBindingBefore := testfixture.Generate(t, "project binding before", func() ([]byte, error) {
		return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
			{Alias: "authors", ImportPath: modulePath + "/authors"},
			{Alias: "blog", ImportPath: modulePath + "/blog"},
		})
	})
	oldObjectBefore := testfixture.Generate(t, "project object before", func() ([]byte, error) {
		return codegen.GenerateProjectRelationObject("project", packages)
	})
	oldAuthorsProjectionBefore := testfixture.Generate(t, "authors projection before", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("authors", authors)
	})
	oldBlogProjectionBefore := testfixture.Generate(t, "blog projection before", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("blog", blog)
	})
	if _, err := codegen.GenerateProjectRelationSelectRelated("project", packages); err != nil {
		t.Fatalf("GenerateProjectRelationSelectRelated() error = %v", err)
	}
	oldBindingAfter := testfixture.Generate(t, "project binding after", func() ([]byte, error) {
		return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
			{Alias: "authors", ImportPath: modulePath + "/authors"},
			{Alias: "blog", ImportPath: modulePath + "/blog"},
		})
	})
	oldObjectAfter := testfixture.Generate(t, "project object after", func() ([]byte, error) {
		return codegen.GenerateProjectRelationObject("project", packages)
	})
	oldAuthorsProjectionAfter := testfixture.Generate(t, "authors projection after", func() ([]byte, error) {
		return codegen.GenerateRelationProjection("authors", authors)
	})
	oldBlogProjectionAfter := testfixture.Generate(t, "blog projection after", func() ([]byte, error) {
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
