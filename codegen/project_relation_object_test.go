package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationObjectIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
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

	authors, blog := testschema.QueryRelation()
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

func TestProjectRelationObjectGeneratorNeverWritesOnFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	authors, blog := testschema.QueryRelation()
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
	authors, blog := testschema.QueryRelation()
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

func TestRelationObjectPackageAliasesStayASCIIAndReserved(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	for _, alias := range []string{"r", "binding", "err", "model0", "relation0", "related0", "snapshot"} {
		packages := relationObjectGenerationPackages(authors, blog)
		packages[1].Alias = alias
		packages[1].ImportPath = "example.com/" + strings.ToLower(alias)
		if _, err := codegen.GenerateProjectRelationObject("project", packages); err != nil {
			t.Fatalf("GenerateProjectRelationObject() rejected safe alias %q: %v", alias, err)
		}
	}
}
