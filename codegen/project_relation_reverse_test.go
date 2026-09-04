package codegen_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationReverseIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
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

	authors, blog := relationQueryGenerationSchemas()
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

	authors, blog := relationQueryGenerationSchemas()
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

func TestGeneratedProjectRelationReverseExactNineFileUnionCompiles(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-reverse-project"

	authorsMain, err := codegen.Generate("authors", authors)
	if err != nil {
		t.Fatalf("generate authors main: %v", err)
	}
	authorsMetadata, err := codegen.GenerateRelationMetadata("authors", authors)
	if err != nil {
		t.Fatalf("generate authors metadata: %v", err)
	}
	authorsObject, err := codegen.GenerateRelationObject("authors", authors)
	if err != nil {
		t.Fatalf("generate authors object: %v", err)
	}
	blogMain, err := codegen.Generate("blog", blog)
	if err != nil {
		t.Fatalf("generate blog main: %v", err)
	}
	blogMetadata, err := codegen.GenerateRelationMetadata("blog", blog)
	if err != nil {
		t.Fatalf("generate blog metadata: %v", err)
	}
	blogObject, err := codegen.GenerateRelationObject("blog", blog)
	if err != nil {
		t.Fatalf("generate blog object: %v", err)
	}
	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: "authors", ImportPath: modulePath + "/authors"},
		{Alias: "blog", ImportPath: modulePath + "/blog"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	projectReverse, err := codegen.GenerateProjectRelationReverse("project", []codegen.RelationReversePackage{
		{Alias: "authors", ImportPath: modulePath + "/authors", Schema: authors},
		{Alias: "blog", ImportPath: modulePath + "/blog", Schema: blog},
	})
	if err != nil {
		t.Fatalf("generate project reverse companion: %v", err)
	}

	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))
	generatedFiles := []struct {
		name string
		data []byte
	}{
		{name: "authors/zz_godj_generated.go", data: authorsMain},
		{name: "authors/zz_godj_relation.go", data: authorsMetadata},
		{name: "authors/zz_godj_relation_object.go", data: authorsObject},
		{name: "blog/zz_godj_generated.go", data: blogMain},
		{name: "blog/zz_godj_relation.go", data: blogMetadata},
		{name: "blog/zz_godj_relation_object.go", data: blogObject},
		{name: "project/zz_godj_bindings.go", data: projectBinding},
		{name: "project/zz_godj_relation_reverse.go", data: projectReverse},
	}
	if len(generatedFiles) != 8 {
		t.Fatalf("generated inventory has %d files, want exact eight", len(generatedFiles))
	}
	for _, file := range generatedFiles {
		writeGeneratedTestFile(t, directory, file.name, file.data)
	}
	writeGeneratedTestFile(t, directory, "project/relation_reverse_external_test.go", generatedRelationReverseExternalTest(modulePath))

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("exact eight-file generated reverse project did not compile: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationReverseMissingObjectPrerequisitesFailWithoutReplacingLastKnownGood(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	for _, omitted := range []string{"authors", "blog"} {
		t.Run(omitted, func(t *testing.T) {
			const modulePrefix = "example.com/godj-relation-reverse-missing-object-"
			publicationDirectory := t.TempDir()
			publicationPath := filepath.Join(publicationDirectory, "zz_godj_relation_reverse.go")
			lastKnownGood := []byte("package project\n\nconst LastKnownGood = true\n")
			if err := os.WriteFile(publicationPath, lastKnownGood, 0o644); err != nil {
				t.Fatalf("write last-known-good reverse output: %v", err)
			}

			candidateDirectory := writeProjectRelationReverseVariant(
				t,
				modulePrefix+omitted,
				[]namedRelationReverseSchema{
					{name: "authors", schema: authors},
					{name: "blog", schema: blog},
				},
				"",
				omitted,
			)
			command := exec.Command("go", "test", "-mod=mod", "./...")
			command.Dir = candidateDirectory
			command.Env = generatedTestEnvironment()
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("reverse union without %s relation-object prerequisite unexpectedly compiled", omitted)
			}
			if !bytes.Contains(output, []byte("RelationObjectDescriptor")) ||
				!bytes.Contains(output, []byte("BindRelationStorage")) {
				t.Fatalf("missing %s object diagnostic lacks required interface/method fragments:\n%s", omitted, output)
			}

			got, err := os.ReadFile(publicationPath)
			if err != nil {
				t.Fatalf("read last-known-good reverse output: %v", err)
			}
			if !bytes.Equal(got, lastKnownGood) {
				t.Fatalf("failed %s candidate replaced last-known-good output:\n%s", omitted, got)
			}
		})
	}
}

func TestGeneratedProjectRelationReverseCurrentAndNoEdgeVariantsCompile(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()

	t.Run("current reverse owner is object capable", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-reverse-current"
		generated := generateProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
			{name: "blog", schema: blog},
		})
		for _, required := range [][]byte{
			[]byte("type AuthorsAuthorReverseRelations struct"),
			[]byte("Posts         AuthorsAuthorPostsReverseRelation"),
			[]byte("AuthorsAuthorReverseObjectFactory"),
			[]byte("AuthorsAuthorReverseObject struct"),
			[]byte("type ReverseObjects struct"),
			[]byte("func BindReverseObjects() (ReverseObjects, error)"),
			[]byte(`db "github.com/progresshans/godj/db"`),
			[]byte(`query "github.com/progresshans/godj/query"`),
		} {
			if !bytes.Contains(generated, required) {
				t.Fatalf("current reverse source does not contain %q:\n%s", required, generated)
			}
		}
		compileProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
			{name: "blog", schema: blog},
		}, `package project_test

import (
	"testing"

	project "example.com/godj-relation-reverse-current/project"
)

func TestCurrentReverse(t *testing.T) {
	relations, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	_ = relations.AuthorsAuthor.Posts.Title.Exact("Alpha")
	if _, err := project.BindReverseObjects(); err != nil {
		t.Fatal(err)
	}
}
`)
	})

	t.Run("no reverse edges", func(t *testing.T) {
		const modulePath = "example.com/godj-relation-reverse-empty"
		generated := generateProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
		})
		for _, forbidden := range [][]byte{
			[]byte("AuthorsAuthorReverseRelations"),
			[]byte("AuthorsAuthorReverseObjectFactory"),
			[]byte(`db "github.com/progresshans/godj/db"`),
			[]byte(`query "github.com/progresshans/godj/query"`),
		} {
			if bytes.Contains(generated, forbidden) {
				t.Fatalf("no-edge reverse source contains %q:\n%s", forbidden, generated)
			}
		}
		compileProjectRelationReverseVariant(t, modulePath, []namedRelationReverseSchema{
			{name: "authors", schema: authors},
		}, `package project_test

import (
	"testing"

	project "example.com/godj-relation-reverse-empty/project"
)

func TestEmptyReverse(t *testing.T) {
	if _, err := project.BindReverseRelations(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.BindReverseObjects(); err != nil {
		t.Fatal(err)
	}
}
`)
	})
}

func TestRelationReversePackageAliasesStayASCIIAndAliasImpossibleLocals(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
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

type namedRelationReverseSchema struct {
	name   string
	schema ir.Schema
}

func generateProjectRelationReverseVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
) []byte {
	t.Helper()
	packages := make([]codegen.RelationReversePackage, len(schemas))
	for index, candidate := range schemas {
		packages[index] = codegen.RelationReversePackage{
			Alias:      candidate.name,
			ImportPath: modulePath + "/" + candidate.name,
			Schema:     candidate.schema,
		}
	}
	generated, err := codegen.GenerateProjectRelationReverse("project", packages)
	if err != nil {
		t.Fatalf("generate reverse variant: %v", err)
	}
	return generated
}

func compileProjectRelationReverseVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
	externalTest string,
) {
	t.Helper()
	directory := writeProjectRelationReverseVariant(t, modulePath, schemas, externalTest, "")
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated reverse variant did not compile: %v\n%s", err, output)
	}
}

func writeProjectRelationReverseVariant(
	t *testing.T,
	modulePath string,
	schemas []namedRelationReverseSchema,
	externalTest string,
	omitObjectFor string,
) string {
	t.Helper()
	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))

	bridgePackages := make([]codegen.BridgePackage, len(schemas))
	reversePackages := make([]codegen.RelationReversePackage, len(schemas))
	for index, candidate := range schemas {
		importPath := modulePath + "/" + candidate.name
		bridgePackages[index] = codegen.BridgePackage{Alias: candidate.name, ImportPath: importPath}
		reversePackages[index] = codegen.RelationReversePackage{Alias: candidate.name, ImportPath: importPath, Schema: candidate.schema}

		main, err := codegen.Generate(candidate.name, candidate.schema)
		if err != nil {
			t.Fatalf("generate %s main: %v", candidate.name, err)
		}
		metadata, err := codegen.GenerateRelationMetadata(candidate.name, candidate.schema)
		if err != nil {
			t.Fatalf("generate %s metadata: %v", candidate.name, err)
		}
		object, err := codegen.GenerateRelationObject(candidate.name, candidate.schema)
		if err != nil {
			t.Fatalf("generate %s object: %v", candidate.name, err)
		}
		writeGeneratedTestFile(t, directory, candidate.name+"/zz_godj_generated.go", main)
		writeGeneratedTestFile(t, directory, candidate.name+"/zz_godj_relation.go", metadata)
		if candidate.name != omitObjectFor {
			writeGeneratedTestFile(t, directory, candidate.name+"/zz_godj_relation_object.go", object)
		}
	}
	bridge, err := codegen.GenerateProjectBridge("project", bridgePackages)
	if err != nil {
		t.Fatalf("generate variant bridge: %v", err)
	}
	reverse, err := codegen.GenerateProjectRelationReverse("project", reversePackages)
	if err != nil {
		t.Fatalf("generate variant reverse: %v", err)
	}
	writeGeneratedTestFile(t, directory, "project/zz_godj_bindings.go", bridge)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_reverse.go", reverse)
	if externalTest != "" {
		writeGeneratedTestFile(t, directory, "project/relation_reverse_external_test.go", []byte(externalTest))
	}
	return directory
}

func generatedRelationReverseExternalTest(modulePath string) []byte {
	return []byte(fmt.Sprintf(`package project_test

import (
	"context"
	"errors"
	"testing"

	authors "%s/authors"
	blog "%s/blog"
	project "%s/project"
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

func TestGeneratedReverseSurface(t *testing.T) {
	relations, err := project.BindReverseRelations()
	if err != nil {
		t.Fatalf("BindReverseRelations() error = %%v", err)
	}
	typed := authors.AuthorObjects.Using(&neverBackend{}).Filter(
		relations.AuthorsAuthor.Posts.Title.Exact("Alpha"),
		relations.AuthorsAuthor.ReviewedPosts.Title.Exact("Gamma"),
	).Plan()
	dynamicPredicates, err := relations.AuthorsAuthor.ParseDynamic(nil, []orm.LookupInput{
		{Key: "posts__title", Value: "Alpha"},
		{Key: "reviewed_posts__title", Value: "Gamma"},
	})
	if err != nil {
		t.Fatalf("ParseDynamic() error = %%v", err)
	}
	dynamic := authors.AuthorObjects.Using(&neverBackend{}).Filter(dynamicPredicates...).Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed and dynamic reverse plans differ\ntyped = %%#v\ndynamic = %%#v", typed, dynamic)
	}

	objects, err := project.BindReverseObjects()
	if err != nil {
		t.Fatalf("BindReverseObjects() error = %%v", err)
	}
	author := authors.NewAuthorWithID(1)
	object, err := objects.AuthorsAuthor.From(&neverBackend{}, author)
	if err != nil {
		t.Fatalf("From() error = %%v", err)
	}
	model, err := object.Model()
	if err != nil || model.ID != 1 {
		t.Fatalf("Model() = %%#v, err=%%v", model, err)
	}
	posts1, err := object.Posts()
	if err != nil {
		t.Fatalf("Posts() error = %%v", err)
	}
	posts2, err := object.Posts()
	if err != nil || posts2 != posts1 {
		t.Fatalf("repeated Posts() = %%p, first=%%p, err=%%v", posts2, posts1, err)
	}
	reviewed, err := object.ReviewedPosts()
	if err != nil || reviewed == posts1 {
		t.Fatalf("ReviewedPosts() = %%p, posts=%%p, err=%%v", reviewed, posts1, err)
	}
	fresh, err := object.Fresh()
	if err != nil || fresh == nil || fresh == object {
		t.Fatalf("Fresh() = %%p, object=%%p, err=%%v", fresh, object, err)
	}
	freshPosts, err := fresh.Posts()
	if err != nil || freshPosts == posts1 {
		t.Fatalf("fresh Posts() = %%p, old=%%p, err=%%v", freshPosts, posts1, err)
	}

	copyValue := *object
	_, err = (&copyValue).Model()
	assertInvalidPlan(t, err)
	_, err = new(project.AuthorsAuthorReverseObject).Posts()
	assertInvalidPlan(t, err)
	var nilObject *project.AuthorsAuthorReverseObject
	_, err = nilObject.ReviewedPosts()
	assertInvalidPlan(t, err)
	if _, err := objects.AuthorsAuthor.From(nil, author); err == nil {
		t.Fatal("From() accepted nil backend")
	}
	var typedNil *neverBackend
	if _, err := objects.AuthorsAuthor.From(typedNil, author); err == nil {
		t.Fatal("From() accepted typed-nil backend")
	}

	var _ orm.Predicate[authors.Author] = relations.AuthorsAuthor.Posts.ID.Exact(1)
	var _ *orm.RelatedSet[blog.Post] = posts1
}
`, modulePath, modulePath, modulePath))
}
