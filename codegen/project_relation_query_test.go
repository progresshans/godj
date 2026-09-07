package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/codegen/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationQueryIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	packages := testfixture.QueryPackages(authors, blog)
	first, err := codegen.GenerateProjectRelationQuery("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationQuery() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationQuery("project", []codegen.RelationQueryPackage{packages[1], packages[0]})
	if err != nil {
		t.Fatalf("GenerateProjectRelationQuery() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("project relation query package order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "relation_query", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation query golden: %v", err)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation query bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}
	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationQueryGeneratorVersion = "godj-codegen-rel-query-project-v1"`),
		[]byte(`authors "example.com/godj-relation-query-project/authors"`),
		[]byte(`blog "example.com/godj-relation-query-project/blog"`),
		[]byte("type BlogPostAuthorRelation struct"),
		[]byte("ID   orm.RelatedIntegerField[blog.Post]"),
		[]byte("Name orm.RelatedStringField[blog.Post]"),
		[]byte("type BlogPostRelations struct"),
		[]byte("Author BlogPostAuthorRelation"),
		[]byte("model  orm.BoundModel[blog.Post]"),
		[]byte("func (_relations BlogPostRelations) ParseDynamic("),
		[]byte("type Relations struct"),
		[]byte("BlogPost BlogPostRelations"),
		[]byte("func BindRelations() (Relations, error)"),
		[]byte(`ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}`),
		[]byte(`ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}`),
		[]byte(`orm.BindForward(_model1, "author", _model0)`),
		[]byte("_relation0.Integer(authors.AuthorFields.ID)"),
		[]byte("_relation0.String(authors.AuthorFields.Name)"),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("project relation query source does not contain %q:\n%s", fragment, first)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("AuthorsAuthorRelations"),
		[]byte("GoDjRelationSchema"),
		[]byte("ForeignKeyRelation"),
		[]byte("DBTable"),
		[]byte(`"blog_post"`),
		[]byte(`"author_id"`),
		[]byte("panic("),
		[]byte("func init("),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("project relation query source contains forbidden schema replay %q:\n%s", forbidden, first)
		}
	}

	packages[1].Schema.Models[0].Fields[2].Relation.Target.AppLabel = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated project bytes")
	}
}

func TestGenerateProjectRelationQueryRejectsInvalidInputsAndNamespaces(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	valid := testfixture.QueryPackages(authors, blog)
	for _, test := range []struct {
		name     string
		pkg      string
		packages []codegen.RelationQueryPackage
	}{
		{name: "invalid generated package", pkg: "bad-package", packages: valid},
		{name: "uppercase alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "Blog", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "underscore alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "my_blog", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "unicode alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "블로그", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "keyword alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "for", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "init alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "init", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved orm alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "orm", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved ir alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "ir", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared error alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "error", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared nil alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "nil", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "invalid import path", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "blog", ImportPath: "example.com/bad path", Schema: blog}}},
		{name: "reserved orm import path", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/orm", Schema: blog}}},
		{name: "duplicate alias", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "app", ImportPath: "example.com/a", Schema: authors}, {Alias: "app", ImportPath: "example.com/b", Schema: blog}}},
		{name: "duplicate import path", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "authors", ImportPath: "example.com/app", Schema: authors}, {Alias: "blog", ImportPath: "example.com/app", Schema: blog}}},
		{name: "duplicate app label", pkg: "project", packages: []codegen.RelationQueryPackage{{Alias: "authors", ImportPath: "example.com/a", Schema: authors}, {Alias: "other", ImportPath: "example.com/b", Schema: authors}}},
		{name: "unresolved target", pkg: "project", packages: []codegen.RelationQueryPackage{valid[1]}},
		{name: "missing terminal ID", pkg: "project", packages: relationQueryPackagesWithFieldGoName(authors, blog, "Author")},
		{name: "ParseDynamic collision", pkg: "project", packages: relationQueryPackagesWithFieldGoName(authors, blog, "ParseDynamicID")},
		{name: "derived surface collision", pkg: "project", packages: collidingRelationQuerySurfaces()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codegen.GenerateProjectRelationQuery(test.pkg, test.packages); err == nil {
				t.Fatal("GenerateProjectRelationQuery() accepted invalid input")
			}
		})
	}
}

func TestGenerateProjectRelationQueryZeroProjectUsesNoUnusedImports(t *testing.T) {
	t.Parallel()

	generated, err := codegen.GenerateProjectRelationQuery("project", nil)
	if err != nil {
		t.Fatalf("GenerateProjectRelationQuery() error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte("type Relations struct"),
		[]byte("if _, _err := Bind(); _err != nil"),
		[]byte("return Relations{}, nil"),
	} {
		if !bytes.Contains(generated, fragment) {
			t.Fatalf("zero project source does not contain %q:\n%s", fragment, generated)
		}
	}
	if bytes.Contains(generated, []byte("import")) {
		t.Fatalf("zero project source contains unused imports:\n%s", generated)
	}
}

func TestProjectRelationQueryGeneratorNeverWritesOnFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sentinelPath := filepath.Join(directory, "committed.go")
	sentinel := []byte("package committed\n\nconst LastGood = true\n")
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	authors, blog := testschema.QueryRelation()
	if _, err := codegen.GenerateProjectRelationQuery(
		"project",
		relationQueryPackagesWithFieldGoName(authors, blog, "Author"),
	); err == nil {
		t.Fatal("GenerateProjectRelationQuery() accepted invalid selector")
	}
	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("pure-byte validation failure changed sentinel: %q", got)
	}
}

func relationQueryPackagesWithFieldGoName(authors, blog ir.Schema, goName string) []codegen.RelationQueryPackage {
	blog = blog.Clone()
	blog.Models[0].Fields[2].GoName = goName
	return testfixture.QueryPackages(authors, blog)
}

func collidingRelationQuerySurfaces() []codegen.RelationQueryPackage {
	target := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "target",
		Models: []ir.Model{{
			Name:   "record",
			GoName: "Record",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	source := func(appLabel, modelName, goName, reverse string) ir.Schema {
		return ir.Schema{
			FormatVersion: ir.CurrentFormatVersion,
			AppLabel:      appLabel,
			Models: []ir.Model{{
				Name:   modelName,
				GoName: goName,
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
					{
						Name: "target", GoName: "TargetID", Kind: ir.FieldForeignKey,
						Relation: &ir.ForeignKeyRelation{
							Target:      ir.ModelIdentity{AppLabel: "target", ModelName: "record"},
							Cardinality: ir.RelationManyToOne,
							Reverse:     ir.ReverseRelation{Name: reverse},
							OnDelete:    ir.DeleteProtect,
						},
					},
				},
			}},
		}
	}
	return []codegen.RelationQueryPackage{
		{Alias: "a", ImportPath: "example.com/a", Schema: source("one", "bc", "BC", "from_one")},
		{Alias: "aB", ImportPath: "example.com/ab", Schema: source("two", "c", "C", "from_two")},
		{Alias: "target", ImportPath: "example.com/target", Schema: target},
	}
}
