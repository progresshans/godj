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

func TestGenerateProjectRelationReverseIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	packages := relationReverseGenerationPackages(authors, blog)
	first, err := codegen.GenerateProjectRelationReverse("project", packages)
	if err != nil {
		t.Fatalf("GenerateProjectRelationReverse() error = %v", err)
	}
	second, err := codegen.GenerateProjectRelationReverse("project", []codegen.RelationReversePackage{packages[1], packages[0]})
	if err != nil {
		t.Fatalf("GenerateProjectRelationReverse() permuted error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("project relation reverse package order changed bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "relation_reverse", "project.golden"))
	if err != nil {
		t.Fatalf("read project relation reverse golden: %v", err)
	}
	if !bytes.Equal(first, want) {
		t.Fatalf("project relation reverse bytes drifted\ngot:\n%s\nwant:\n%s", first, want)
	}
	for _, fragment := range [][]byte{
		[]byte(`const GoDjProjectRelationReverseGeneratorVersion = "godj-codegen-rel-reverse-project-v1"`),
		[]byte("var _ orm.RelationObjectDescriptor[authors.Author] = authors.AuthorDescriptor{}"),
		[]byte("var _ orm.PrimaryKeyObjectDescriptor[authors.Author] = authors.AuthorDescriptor{}"),
		[]byte("var _ orm.RelationObjectDescriptor[blog.Post] = blog.PostDescriptor{}"),
		[]byte("type AuthorsAuthorPostsReverseRelation struct"),
		[]byte("Title orm.RelatedStringField[authors.Author]"),
		[]byte("type AuthorsAuthorReviewedPostsReverseRelation struct"),
		[]byte("type AuthorsAuthorReverseRelations struct"),
		[]byte("Posts         AuthorsAuthorPostsReverseRelation"),
		[]byte("ReviewedPosts AuthorsAuthorReviewedPostsReverseRelation"),
		[]byte("func (_relations AuthorsAuthorReverseRelations) ParseDynamic("),
		[]byte("return orm.ParseDynamicReverseRelations(_relations.model, _policy, _inputs)"),
		[]byte("type ReverseRelations struct"),
		[]byte("func BindReverseRelations() (ReverseRelations, error)"),
		[]byte(`orm.BindReverse(_model0, "posts", _model1)`),
		[]byte(`orm.BindReverse(_model0, "reviewed_posts", _model1)`),
		[]byte("type AuthorsAuthorReverseObjectFactory struct"),
		[]byte("func (_factory AuthorsAuthorReverseObjectFactory) From("),
		[]byte("type AuthorsAuthorReverseObject struct"),
		[]byte("func (_object *AuthorsAuthorReverseObject) Model()"),
		[]byte("func (_object *AuthorsAuthorReverseObject) Posts()"),
		[]byte("func (_object *AuthorsAuthorReverseObject) ReviewedPosts()"),
		[]byte("func (_object *AuthorsAuthorReverseObject) Fresh()"),
		[]byte("type ReverseObjects struct"),
		[]byte("func BindReverseObjects() (ReverseObjects, error)"),
		[]byte(`orm.BindReverseObject(_model0, "posts", _model1)`),
		[]byte(`orm.BindReverseObject(_model0, "reviewed_posts", _model1)`),
	} {
		if !bytes.Contains(first, fragment) {
			t.Fatalf("project relation reverse source does not contain %q:\n%s", fragment, first)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("type Relations struct"),
		[]byte("type Objects struct"),
		[]byte("func BindRelations("),
		[]byte("func BindObjects("),
		[]byte("GoDjRelationSchema"),
		[]byte("ForeignKeyRelation"),
		[]byte(`"authors_author"`),
		[]byte(`"blog_post"`),
		[]byte(`"author_id"`),
		[]byte("panic("),
		[]byte("func init("),
	} {
		if bytes.Contains(first, forbidden) {
			t.Fatalf("project relation reverse source contains forbidden schema replay %q:\n%s", forbidden, first)
		}
	}

	packages[1].Schema.Models[0].Fields[2].Relation.Reverse.Name = "mutated"
	if bytes.Contains(first, []byte("mutated")) {
		t.Fatal("post-generation schema mutation changed generated project bytes")
	}
}

func TestGenerateProjectRelationReverseRejectsInvalidInputsAndNamespaces(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	valid := relationReverseGenerationPackages(authors, blog)
	duplicateReverse := blog.Clone()
	duplicateReverse.Models[0].Fields[3].Relation.Reverse.Name = "posts"
	missingOwnerPrimaryKey := authors.Clone()
	missingOwnerPrimaryKey.Models[0].Fields[0].PrimaryKey = false
	selfCollision := selfReverseUnionCollisionSchema()

	tests := []struct {
		name     string
		pkg      string
		packages []codegen.RelationReversePackage
	}{
		{name: "invalid generated package", pkg: "bad-package", packages: valid},
		{name: "uppercase alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "Blog", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "underscore alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "my_blog", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "unicode alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "블로그", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "keyword alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "for", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "init alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "init", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved db alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "db", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved orm alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "orm", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved query alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "query", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "reserved ir alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "ir", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared bool alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "bool", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared error alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "error", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared false alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "false", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared nil alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "nil", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "predeclared true alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "true", ImportPath: "example.com/blog", Schema: blog}}},
		{name: "invalid import path", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "blog", ImportPath: "example.com/bad path", Schema: blog}}},
		{name: "reserved db import", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/db", Schema: blog}}},
		{name: "reserved orm import", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/orm", Schema: blog}}},
		{name: "reserved query import", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/query", Schema: blog}}},
		{name: "reserved ir import", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "blog", ImportPath: "github.com/progresshans/godj/schema/ir", Schema: blog}}},
		{name: "duplicate alias", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "app", ImportPath: "example.com/a", Schema: authors}, {Alias: "app", ImportPath: "example.com/b", Schema: blog}}},
		{name: "duplicate import", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "authors", ImportPath: "example.com/app", Schema: authors}, {Alias: "blog", ImportPath: "example.com/app", Schema: blog}}},
		{name: "duplicate app", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "authors", ImportPath: "example.com/a", Schema: authors}, {Alias: "other", ImportPath: "example.com/b", Schema: authors}}},
		{name: "unresolved owner", pkg: "project", packages: []codegen.RelationReversePackage{valid[1]}},
		{name: "owner without primary key", pkg: "project", packages: relationReverseGenerationPackages(missingOwnerPrimaryKey, blog)},
		{name: "duplicate reverse namespace", pkg: "project", packages: relationReverseGenerationPackages(authors, duplicateReverse)},
		{name: "immutable project query union collision", pkg: "project", packages: []codegen.RelationReversePackage{{Alias: "nodes", ImportPath: "example.com/nodes", Schema: selfCollision}}},
	}
	for _, reverseName := range []string{
		"_posts",
		"posts_",
		"reviewed__posts",
		"1posts",
		"posts_1reviewed",
		"reviewedPosts",
		"reviewed-posts",
		"게시물",
		"for",
		"posts_for",
		"init",
		"posts_init",
		"model",
		"fresh",
		"from",
		"parse_dynamic",
	} {
		tests = append(tests, struct {
			name     string
			pkg      string
			packages []codegen.RelationReversePackage
		}{
			name:     "reverse selector " + reverseName,
			pkg:      "project",
			packages: relationReversePackagesWithName(authors, blog, reverseName),
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := codegen.GenerateProjectRelationReverse(test.pkg, test.packages)
			if err == nil {
				t.Fatalf("GenerateProjectRelationReverse() accepted invalid input:\n%s", generated)
			}
			if generated != nil {
				t.Fatalf("GenerateProjectRelationReverse() failure returned non-nil bytes %q", generated)
			}
		})
	}
}

func TestGenerateProjectRelationReverseRejectsImmutablePrerequisiteNamespaceFailures(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	for _, test := range []struct {
		name       string
		fieldIndex int
		goName     string
		wantReason string
	}{
		{name: "object Model method", fieldIndex: 2, goName: "ModelID", wantReason: "object method Model"},
		{name: "object Fresh method", fieldIndex: 2, goName: "FreshID", wantReason: "object method Fresh"},
		{name: "object factory From method", fieldIndex: 3, goName: "FromID", wantReason: "object factory field From"},
		{name: "object private factory field", fieldIndex: 2, goName: "FactoryID", wantReason: "private object field factory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			packages := relationReversePackagesWithFieldGoName(authors, blog, test.fieldIndex, test.goName)
			generated, err := codegen.GenerateProjectRelationReverse("project", packages)
			if err == nil {
				t.Fatalf("GenerateProjectRelationReverse() accepted immutable prerequisite collision:\n%s", generated)
			}
			if generated != nil {
				t.Fatalf("immutable prerequisite collision returned non-nil bytes %q", generated)
			}
			if !strings.Contains(err.Error(), test.wantReason) {
				t.Fatalf("collision error = %v, want reason containing %q", err, test.wantReason)
			}
		})
	}
}

func TestGenerateProjectRelationReverseZeroProjectUsesNoUnusedImports(t *testing.T) {
	t.Parallel()

	generated, err := codegen.GenerateProjectRelationReverse("project", nil)
	if err != nil {
		t.Fatalf("GenerateProjectRelationReverse() error = %v", err)
	}
	for _, fragment := range [][]byte{
		[]byte("type ReverseRelations struct"),
		[]byte("type ReverseObjects struct"),
		[]byte("if _, _err := Bind(); _err != nil"),
		[]byte("return ReverseRelations{}, nil"),
		[]byte("return ReverseObjects{}, nil"),
	} {
		if !bytes.Contains(generated, fragment) {
			t.Fatalf("zero project source does not contain %q:\n%s", fragment, generated)
		}
	}
	if bytes.Contains(generated, []byte("import")) {
		t.Fatalf("zero project source contains unused imports:\n%s", generated)
	}
}

func TestRelationReversePackageAliasesStayASCIIAndAliasImpossibleLocals(t *testing.T) {
	t.Parallel()

	authors, blog := testschema.QueryRelation()
	for _, alias := range []string{"r", "context", "binding", "err", "model0", "relation0", "related0", "snapshot", "value", "factory"} {
		packages := relationReverseGenerationPackages(authors, blog)
		packages[1].Alias = alias
		packages[1].ImportPath = "example.com/" + strings.ToLower(alias)
		generated, err := codegen.GenerateProjectRelationReverse("project", packages)
		if err != nil {
			t.Fatalf("GenerateProjectRelationReverse() rejected safe alias %q: %v", alias, err)
		}
		if !bytes.Contains(generated, []byte(alias+` "example.com/`+strings.ToLower(alias)+`"`)) {
			t.Fatalf("generated source omitted safe alias %q:\n%s", alias, generated)
		}
	}
}

func relationReverseGenerationPackages(authors, blog ir.Schema) []codegen.RelationReversePackage {
	return []codegen.RelationReversePackage{
		{Alias: "authors", ImportPath: "example.com/godj-relation-reverse-project/authors", Schema: authors},
		{Alias: "blog", ImportPath: "example.com/godj-relation-reverse-project/blog", Schema: blog},
	}
}

func relationReversePackagesWithName(authors, blog ir.Schema, name string) []codegen.RelationReversePackage {
	candidate := blog.Clone()
	candidate.Models[0].Fields[2].Relation.Reverse.Name = name
	return relationReverseGenerationPackages(authors, candidate)
}

func relationReversePackagesWithFieldGoName(
	authors, blog ir.Schema,
	fieldIndex int,
	goName string,
) []codegen.RelationReversePackage {
	candidate := blog.Clone()
	candidate.Models[0].Fields[fieldIndex].GoName = goName
	return relationReverseGenerationPackages(authors, candidate)
}

func selfReverseUnionCollisionSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "nodes",
		Models: []ir.Model{{
			Name:   "node",
			GoName: "Node",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name:   "parent",
					GoName: "ParentReverseID",
					Kind:   ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "nodes", ModelName: "node"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "parent"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
}
