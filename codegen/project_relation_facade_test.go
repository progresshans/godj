package codegen_test

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationFacadeIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-facade"
	packages := testfixture.FacadePackages(modulePath, authors, blog)
	first, err := codegen.GenerateProjectRelationFacade("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationFacade(
		"project",
		[]codegen.RelationObjectPackage{packages[1], packages[0]},
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() package permutation error = %v", err)
	}
	reorderedBlog := blog.Clone()
	reorderedBlog.Models[0].Fields[2], reorderedBlog.Models[0].Fields[3] =
		reorderedBlog.Models[0].Fields[3], reorderedBlog.Models[0].Fields[2]
	third, err := codegen.GenerateProjectRelationFacade(
		"project",
		testfixture.FacadePackages(modulePath, authors, reorderedBlog),
	)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() relation permutation error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("package ordering changed facade bytes\nfirst:\n%s\npackage permutation:\n%s", first, second)
	}
	if bytes.Equal(first, third) {
		t.Fatal("schema field-order drift did not change the canonical input hash")
	}
	if !bytes.Equal(facadeWithoutInputHash(first), facadeWithoutInputHash(third)) {
		t.Fatalf("field ordering changed facade surface beyond its provenance hash\nfirst:\n%s\nfield permutation:\n%s", first, third)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "relation_facade", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation facade golden: %v\ngenerated hex: %x", err, first)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation facade golden differs: got %d bytes, want %d", len(first), len(want))
	}

	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationFacadeGeneratorVersion = "godj-codegen-rel-facade-project-current-v5"`),
		[]byte(`const GoDjProjectRelationFacadeInputSHA256 = "`),
		[]byte("type Backend interface {\n\tdb.Queryer\n\tdb.Mutator\n}"),
		[]byte("type authorsAuthorModel = authors.Author"),
		[]byte("type blogPostModel = blog.Post"),
		[]byte("type BlogPost struct {\n\tblogPostModel\n\tstate"),
		[]byte("func (BlogPost) MarshalJSON() ([]byte, error)"),
		[]byte("func (*BlogPost) UnmarshalJSON([]byte) error"),
		[]byte("primaryKeySnapshot        int64"),
		[]byte("primaryKeySnapshotPresent bool"),
		[]byte("authorScalarSnapshot"),
		[]byte("reviewerScalarPresent"),
		[]byte("func relationFacadePrimaryKeyUpdate(_field string) error"),
		[]byte("Category: query.CategoryModelState, Code: query.CodePrimaryKeyUpdateField"),
		[]byte("func (_model *BlogPost) relationFacadeReconcile() error"),
		[]byte("_authorChanged := _authorCurrentKey != _model.authorScalarSnapshot"),
		[]byte("_reviewerChanged := _reviewerCurrentPresent != _model.reviewerScalarPresent"),
		[]byte("PrimaryKey(_model.blogPostModel)"),
		[]byte("&_model.blogPostModel"),
		[]byte("type Models struct {\n\tAuthorsAuthor AuthorsAuthorQuery\n\tBlogPost      BlogPostQuery\n}"),
		[]byte("func Using(_backend Backend) (Models, error)"),
		[]byte("func (_query BlogPostQuery) Distinct() BlogPostQuery"),
		[]byte("func (_query BlogPostQuery) Offset(_offset int) (BlogPostQuery, error)"),
		[]byte("func (_query BlogPostQuery) Count(_ctx context.Context) (int64, error)"),
		[]byte("func SelectBlogPostInto[R any](_ctx context.Context, _source BlogPostQuery, _projection orm.Projection[blog.Post, R]) ([]R, error)"),
		[]byte("func AggregateBlogPostInto[R any](_ctx context.Context, _source BlogPostQuery, _aggregate orm.Aggregate[blog.Post, R]) (R, error)"),
		[]byte("func (_query BlogPostQuery) First(_ctx context.Context) (*BlogPost, bool, error)"),
		[]byte("func (_model *BlogPost) Author(_ctx context.Context) (*AuthorsAuthor, error)"),
		[]byte("func (_model *BlogPost) Reviewer(_ctx context.Context) (*AuthorsAuthor, bool, error)"),
		[]byte("type BlogPostRelationSelector interface"),
		[]byte("type BlogPostRelationSelectors struct"),
		[]byte("func (_query BlogPostQuery) SelectRelated(_selector BlogPostRelationSelector) BlogPostEagerQuery"),
		[]byte("func (_query BlogPostEagerQuery) All(_ctx context.Context) ([]*BlogPost, error)"),
		[]byte("_objects, _err := BindObjects()"),
		[]byte("_result.projection = _state.objects.BlogPost.SelectRelated(_source).Author()"),
		[]byte("_result.projection = _state.objects.BlogPost.SelectRelated(_source).Reviewer()"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("generated facade source does not contain %q:\n%s", fragment, first)
		}
	}
	if bytes.Contains(first, []byte("\tmodel blog.Post")) || bytes.Contains(first, []byte("_model.model")) {
		t.Fatalf("generated facade retained the named raw-model field:\n%s", first)
	}
	if bytes.Contains(first, []byte("\t\treturn _err\n\t\treturn _err")) {
		t.Fatalf("generated facade contains a duplicated error return:\n%s", first)
	}
	for _, targetKeySnapshot := range [][]byte{
		[]byte("_authorTarget.relationFacadePrimaryKey()"),
		[]byte("_reviewerTarget.relationFacadePrimaryKey()"),
	} {
		if count := bytes.Count(first, targetKeySnapshot); count != 1 {
			t.Fatalf("generated facade target-key snapshot %q count = %d, want exactly 1", targetKeySnapshot, count)
		}
	}
	if bindIndex, nilIndex := bytes.Index(first, []byte("BindObjects()")), bytes.Index(first, []byte("relationFacadeNil(_backend)")); bindIndex < 0 || nilIndex < 0 || bindIndex >= nilIndex {
		t.Fatalf("BindObjects index %d must precede backend nil validation index %d", bindIndex, nilIndex)
	}
	wantExported := []string{
		"AggregateAuthorsAuthorInto",
		"AggregateBlogPostInto",
		"AuthorsAuthor",
		"AuthorsAuthorQuery",
		"Backend",
		"BlogPost",
		"BlogPostEagerQuery",
		"BlogPostQuery",
		"BlogPostRelationSelector",
		"BlogPostRelationSelectors",
		"GoDjProjectRelationFacadeGeneratorVersion",
		"GoDjProjectRelationFacadeInputSHA256",
		"Models",
		"SelectAuthorsAuthorInto",
		"SelectBlogPostInto",
		"Using",
	}
	if got := projectRelationFacadeExportedDeclarations(t, first); !slices.Equal(got, wantExported) {
		t.Fatalf("project relation facade exported declarations = %v, want %v", got, wantExported)
	}

	packages[1].Schema.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated facade bytes")
	}
}

func TestGenerateProjectRelationFacadeRejectsInvalidInputsBeforeBytes(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	valid := testfixture.FacadePackages("example.com/godj-relation-facade-invalid", authors, blog)
	reservedAlias := testfixture.FacadePackages("example.com/godj-relation-facade-reserved", authors, blog)
	reservedAlias[0].Alias = "reflect"
	reservedPath := testfixture.FacadePackages("example.com/godj-relation-facade-reserved", authors, blog)
	reservedPath[0].ImportPath = "reflect"
	unwrapCollision := blog.Clone()
	unwrapCollision.Models[0].Fields[2].GoName = "UnwrapID"
	saveCollision := blog.Clone()
	saveCollision.Models[0].Fields[2].GoName = "SaveID"
	derivedMethodCollision := blog.Clone()
	derivedMethodCollision.Models[0].Fields[3].GoName = "WithAuthorID"
	marshalFieldCollision := authors.Clone()
	marshalFieldCollision.Models[0].Fields[1].GoName = "MarshalJSON"
	unmarshalFieldCollision := authors.Clone()
	unmarshalFieldCollision.Models[0].Fields[1].GoName = "UnmarshalJSON"
	aliasCollision := testfixture.FacadePackages("example.com/godj-relation-facade-alias", authors, blog)
	aliasCollision[0].Alias = "blogPostModel"

	for _, test := range []struct {
		name     string
		pkg      string
		packages []codegen.RelationObjectPackage
		contains string
	}{
		{name: "invalid package", pkg: "bad-package", packages: valid, contains: "package"},
		{name: "missing target", pkg: "project", packages: valid[1:], contains: "target"},
		{name: "reserved reflect alias", pkg: "project", packages: reservedAlias, contains: "reflect"},
		{name: "reserved reflect path", pkg: "project", packages: reservedPath, contains: "reflect"},
		{name: "wrapper Unwrap collision", pkg: "project", packages: testfixture.FacadePackages("example.com/godj-relation-facade-unwrap", authors, unwrapCollision), contains: "Unwrap"},
		{name: "wrapper Save collision", pkg: "project", packages: testfixture.FacadePackages("example.com/godj-relation-facade-save", authors, saveCollision), contains: "Save"},
		{name: "derived method collision", pkg: "project", packages: testfixture.FacadePackages("example.com/godj-relation-facade-derived", authors, derivedMethodCollision), contains: "WithAuthor"},
		{name: "MarshalJSON promoted field collision", pkg: "project", packages: testfixture.FacadePackages("example.com/godj-relation-facade-marshal", marshalFieldCollision, blog), contains: "MarshalJSON"},
		{name: "UnmarshalJSON promoted field collision", pkg: "project", packages: testfixture.FacadePackages("example.com/godj-relation-facade-unmarshal", unmarshalFieldCollision, blog), contains: "UnmarshalJSON"},
		{name: "private raw alias import collision", pkg: "project", packages: aliasCollision, contains: "blogPostModel"},
		{name: "all-model surface collision", pkg: "project", packages: relationFacadeSurfaceCollisionPackages(), contains: "Models field ABC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			generated, err := codegen.GenerateProjectRelationFacade(test.pkg, test.packages)
			if err == nil {
				t.Fatal("GenerateProjectRelationFacade() accepted invalid input")
			}
			if len(generated) != 0 {
				t.Fatalf("invalid input returned %d partial bytes", len(generated))
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error %q does not identify %q", err, test.contains)
			}
		})
	}
}

func TestProjectRelationFacadeReservedImports(t *testing.T) {
	authors, blog := testschema.QueryRelation()
	for _, reserved := range []string{"any", "context", "int", "int64", "iota", "ir", "reflect", "sync", "true"} {
		t.Run(reserved+" alias", func(t *testing.T) {
			packages := testfixture.FacadePackages("example.com/godj-relation-facade-reserved-"+reserved, authors, blog)
			packages[0].Alias = reserved
			if generated, err := codegen.GenerateProjectRelationFacade("project", packages); err == nil || len(generated) != 0 {
				t.Fatalf("reserved alias %q = (%d bytes, %v)", reserved, len(generated), err)
			}
		})
	}
	for _, reserved := range []string{"context", "ir", "sync"} {
		t.Run(reserved+" path", func(t *testing.T) {
			packages := testfixture.FacadePackages("example.com/godj-relation-facade-reserved-"+reserved, authors, blog)
			path := reserved
			if reserved == "ir" {
				path = "github.com/progresshans/godj/schema/ir"
			}
			packages[0].ImportPath = path
			if generated, err := codegen.GenerateProjectRelationFacade("project", packages); err == nil || len(generated) != 0 {
				t.Fatalf("reserved path %q = (%d bytes, %v)", reserved, len(generated), err)
			}
		})
	}
	t.Run("non-conflicting alias", func(t *testing.T) {
		packages := testfixture.FacadePackages("example.com/godj-relation-facade-non-conflicting", authors, blog)
		packages[0].Alias = "domainapp"
		generated, err := codegen.GenerateProjectRelationFacade("project", packages)
		if err != nil {
			t.Fatalf("non-conflicting alias rejected: %v", err)
		}
		if len(generated) == 0 {
			t.Fatal("non-conflicting alias returned empty generated bytes")
		}
	})
}

func TestProjectRelationFacadeFirstPublicationAndPrerequisitePreservation(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-facade-publication"
	packages := testfixture.FacadePackages(modulePath, authors, blog)
	before := projectRelationFacadePrerequisiteBytes(t, modulePath, authors, blog, packages)
	candidate, err := codegen.GenerateProjectRelationFacade("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationFacade() error = %v", err)
	}
	after := projectRelationFacadePrerequisiteBytes(t, modulePath, authors, blog, packages)
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("facade generation changed prerequisite byte stream %d", index)
		}
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "zz_godj_relation_facade.go")
	verifyCalls := 0
	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Check: true,
		Verify: func(context.Context, string) error {
			verifyCalls++
			return nil
		},
	})
	if !errors.Is(err, codegen.ErrDrift) || verifyCalls != 0 {
		t.Fatalf("missing Check = %v, verify calls = %d, want drift/0", err, verifyCalls)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Check created target: %v", err)
	}

	verifyFailure := errors.New("candidate union rejected")
	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Verify: func(context.Context, string) error { return verifyFailure },
	})
	if !errors.Is(err, verifyFailure) {
		t.Fatalf("failed first publication error = %v, want verifier failure", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed first publication created target: %v", err)
	}

	err = codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Verify: func(_ context.Context, candidatePath string) error {
			verifyCalls++
			got, readErr := os.ReadFile(candidatePath)
			if readErr != nil {
				return readErr
			}
			if !bytes.Equal(got, candidate) {
				return errors.New("candidate bytes changed")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("first publication error = %v", err)
	}
	if verifyCalls != 1 {
		t.Fatalf("write verifier calls = %d, want 1", verifyCalls)
	}
	verifyCalls = 0
	if err := codegen.WriteFile(context.Background(), path, candidate, codegen.WriteOptions{
		Check: true,
		Verify: func(context.Context, string) error {
			verifyCalls++
			return nil
		},
	}); err != nil || verifyCalls != 0 {
		t.Fatalf("exact Check = %v, verify calls = %d, want nil/0", err, verifyCalls)
	}

	replacement := bytes.Replace(candidate, []byte("backend is nil"), []byte("backend was nil"), 1)
	if bytes.Equal(replacement, candidate) {
		t.Fatal("replacement mutation did not change candidate")
	}
	err = codegen.WriteFile(context.Background(), path, replacement, codegen.WriteOptions{
		Verify: func(context.Context, string) error { return verifyFailure },
	})
	if !errors.Is(err, verifyFailure) {
		t.Fatalf("failed replacement error = %v, want verifier failure", err)
	}
	lastGood, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read last-good facade: %v", err)
	}
	if !bytes.Equal(lastGood, candidate) {
		t.Fatal("failed replacement changed last-good facade bytes")
	}
}

func relationFacadeSurfaceCollisionPackages() []codegen.RelationObjectPackage {
	first := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "one",
		Models: []ir.Model{{
			Name: "bc", GoName: "BC", DBTable: "one_bc",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	second := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "two",
		Models: []ir.Model{{
			Name: "c", GoName: "C", DBTable: "two_c",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	return []codegen.RelationObjectPackage{
		{Alias: "a", ImportPath: "example.com/godj-relation-facade-collision/a", Schema: first},
		{Alias: "aB", ImportPath: "example.com/godj-relation-facade-collision/ab", Schema: second},
	}
}

func projectRelationFacadePrerequisiteBytes(
	t *testing.T,
	modulePath string,
	authors, blog ir.Schema,
	packages []codegen.RelationObjectPackage,
) [][]byte {
	t.Helper()
	return [][]byte{
		testfixture.Generate(t, "project binding", func() ([]byte, error) {
			return codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
				{Alias: "authors", ImportPath: modulePath + "/authors"},
				{Alias: "blog", ImportPath: modulePath + "/blog"},
			})
		}),
		testfixture.Generate(t, "project object", func() ([]byte, error) {
			return codegen.GenerateProjectRelationObject("project", packages)
		}),
		testfixture.Generate(t, "project select related", func() ([]byte, error) {
			return codegen.GenerateProjectRelationSelectRelated("project", packages)
		}),
		testfixture.Generate(t, "authors main", func() ([]byte, error) { return codegen.Generate("authors", authors) }),
		testfixture.Generate(t, "blog main", func() ([]byte, error) { return codegen.Generate("blog", blog) }),
	}
}

func projectRelationFacadeExportedDeclarations(t *testing.T, source []byte) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "project_relation_facade.go", source, 0)
	if err != nil {
		t.Fatalf("parse generated relation facade source: %v", err)
	}
	exported := make([]string, 0)
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Recv == nil && declaration.Name.IsExported() {
				exported = append(exported, declaration.Name.Name)
			}
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				switch specification := specification.(type) {
				case *ast.ValueSpec:
					for _, name := range specification.Names {
						if name.IsExported() {
							exported = append(exported, name.Name)
						}
					}
				case *ast.TypeSpec:
					if specification.Name.IsExported() {
						exported = append(exported, specification.Name.Name)
					}
				}
			}
		}
	}
	slices.Sort(exported)
	return exported
}

func facadeWithoutInputHash(source []byte) []byte {
	lines := bytes.Split(source, []byte("\n"))
	for index := range lines {
		if bytes.HasPrefix(lines[index], []byte("const GoDjProjectRelationFacadeInputSHA256 = ")) {
			lines[index] = []byte("const GoDjProjectRelationFacadeInputSHA256 = \"<canonical-input>\"")
		}
	}
	return bytes.Join(lines, []byte("\n"))
}
