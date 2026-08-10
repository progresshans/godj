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
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGenerateProjectRelationQueryIsCanonicalAndByteLocked(t *testing.T) {
	t.Parallel()

	authors, blog := relationQueryGenerationSchemas()
	packages := relationQueryGenerationPackages(authors, blog)
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

	authors, blog := relationQueryGenerationSchemas()
	valid := relationQueryGenerationPackages(authors, blog)
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

func TestGeneratedRelationQueryProjectCompilesBindsAndHasNoAppEdges(t *testing.T) {
	authors, blog := relationQueryGenerationSchemas()
	authorsMain, err := codegen.Generate("authors", authors)
	if err != nil {
		t.Fatalf("generate authors main: %v", err)
	}
	authorsMetadata, err := codegen.GenerateRelationMetadata("authors", authors)
	if err != nil {
		t.Fatalf("generate authors metadata: %v", err)
	}
	blogMain, err := codegen.Generate("blog", blog)
	if err != nil {
		t.Fatalf("generate blog main: %v", err)
	}
	blogMetadata, err := codegen.GenerateRelationMetadata("blog", blog)
	if err != nil {
		t.Fatalf("generate blog metadata: %v", err)
	}
	blogQuery, err := codegen.GenerateRelationQuery("blog", blog)
	if err != nil {
		t.Fatalf("generate blog query companion: %v", err)
	}
	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: "authors", ImportPath: "example.com/godj-relation-query-project/authors"},
		{Alias: "blog", ImportPath: "example.com/godj-relation-query-project/blog"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	projectQuery, err := codegen.GenerateProjectRelationQuery("project", relationQueryGenerationPackages(authors, blog))
	if err != nil {
		t.Fatalf("generate project relation query: %v", err)
	}

	root := codegenRepositoryRoot(t)
	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module example.com/godj-relation-query-project

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(root))))
	writeGeneratedTestFile(t, directory, "authors/zz_godj_generated.go", authorsMain)
	writeGeneratedTestFile(t, directory, "authors/zz_godj_relation.go", authorsMetadata)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_generated.go", blogMain)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation.go", blogMetadata)
	writeGeneratedTestFile(t, directory, "blog/zz_godj_relation_query.go", blogQuery)
	writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", projectBinding)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_query.go", projectQuery)
	writeGeneratedTestFile(t, directory, "project/relation_query_test.go", []byte(`package project_test

import (
	"database/sql"
	"testing"

	"example.com/godj-relation-query-project/blog"
	"example.com/godj-relation-query-project/project"
	"github.com/progresshans/godj/orm"
)

type postRow struct{}

func (postRow) Scan(destinations ...any) error {
	*destinations[0].(*int64) = 10
	*destinations[1].(*string) = "Hello"
	*destinations[2].(*int64) = 1
	*destinations[3].(*sql.NullInt64) = sql.NullInt64{Int64: 2, Valid: true}
	return nil
}

func TestGeneratedRelationQueryProject(t *testing.T) {
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatalf("BindRelations() error = %v", err)
	}
	typed := blog.PostObjects.Using(nil).Filter(
		relations.BlogPost.Author.Name.Exact("Ada"),
		relations.BlogPost.Author.ID.Exact(1),
	).Plan()
	dynamicPredicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{
		{Key: "author__name", Value: "Ada"},
		{Key: "author__id", Value: int64(1)},
	})
	if err != nil {
		t.Fatalf("ParseDynamic() error = %v", err)
	}
	dynamic := blog.PostObjects.Using(nil).Filter(dynamicPredicates...).Plan()
	if !typed.Equal(dynamic) {
		t.Fatalf("typed and dynamic generated plans differ\ntyped = %#v\ndynamic = %#v", typed, dynamic)
	}

	post, err := (blog.PostDescriptor{}).Scan(postRow{})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if post.ID != 10 || post.Title != "Hello" || post.AuthorID != 1 || post.ReviewerID == nil || *post.ReviewerID != 2 {
		t.Fatalf("Scan() post = %#v", post)
	}
	clone := (blog.PostDescriptor{}).CloneModel(post)
	if clone.ReviewerID == post.ReviewerID || clone.ReviewerID == nil || *clone.ReviewerID != 2 {
		t.Fatalf("CloneModel() did not deep-copy nullable FK: post=%#v clone=%#v", post, clone)
	}
	firstMetadata := (blog.PostDescriptor{}).Metadata()
	firstMetadata.Fields[2].Relation.Target.AppLabel = "mutated"
	secondMetadata := (blog.PostDescriptor{}).Metadata()
	if secondMetadata.Fields[2].Relation.Target.AppLabel == "mutated" {
		t.Fatal("Metadata() returned aliased relation state")
	}
}
`))

	validateGeneratedRelationQueryImportGraph(t, directory)
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation query project did not compile and bind: %v\n%s", err, output)
	}
}

func TestGeneratedProjectRelationQueryAdversarialAliasesCompile(t *testing.T) {
	for _, alias := range []string{"r", "binding", "err", "model0", "relation0", "terminal0"} {
		t.Run(alias, func(t *testing.T) {
			validateGeneratedProjectRelationQueryAlias(t, alias)
		})
	}
}

func validateGeneratedProjectRelationQueryAlias(t *testing.T, alias string) {
	t.Helper()

	authors, blog := relationQueryGenerationSchemas()
	const modulePath = "example.com/godj-relation-query-alias"
	targetMain, err := codegen.Generate("target", authors)
	if err != nil {
		t.Fatalf("generate target main: %v", err)
	}
	targetMetadata, err := codegen.GenerateRelationMetadata("target", authors)
	if err != nil {
		t.Fatalf("generate target metadata: %v", err)
	}
	sourceMain, err := codegen.Generate(alias, blog)
	if err != nil {
		t.Fatalf("generate source main: %v", err)
	}
	sourceMetadata, err := codegen.GenerateRelationMetadata(alias, blog)
	if err != nil {
		t.Fatalf("generate source metadata: %v", err)
	}
	sourceQuery, err := codegen.GenerateRelationQuery(alias, blog)
	if err != nil {
		t.Fatalf("generate source query: %v", err)
	}
	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: "target", ImportPath: modulePath + "/target"},
		{Alias: alias, ImportPath: modulePath + "/source"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	projectQuery, err := codegen.GenerateProjectRelationQuery("project", []codegen.RelationQueryPackage{
		{Alias: "target", ImportPath: modulePath + "/target", Schema: authors},
		{Alias: alias, ImportPath: modulePath + "/source", Schema: blog},
	})
	if err != nil {
		t.Fatalf("generate project query: %v", err)
	}

	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))
	writeGeneratedTestFile(t, directory, "target/zz_godj_generated.go", targetMain)
	writeGeneratedTestFile(t, directory, "target/zz_godj_relation.go", targetMetadata)
	writeGeneratedTestFile(t, directory, "source/zz_godj_generated.go", sourceMain)
	writeGeneratedTestFile(t, directory, "source/zz_godj_relation.go", sourceMetadata)
	writeGeneratedTestFile(t, directory, "source/zz_godj_relation_query.go", sourceQuery)
	writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", projectBinding)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_query.go", projectQuery)

	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated project with alias %q did not compile: %v\n%s\nproject source:\n%s", alias, err, output, projectQuery)
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
	authors, blog := relationQueryGenerationSchemas()
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

func relationQueryGenerationPackages(authors, blog ir.Schema) []codegen.RelationQueryPackage {
	return []codegen.RelationQueryPackage{
		{Alias: "authors", ImportPath: "example.com/godj-relation-query-project/authors", Schema: authors},
		{Alias: "blog", ImportPath: "example.com/godj-relation-query-project/blog", Schema: blog},
	}
}

func relationQueryPackagesWithFieldGoName(authors, blog ir.Schema, goName string) []codegen.RelationQueryPackage {
	blog = blog.Clone()
	blog.Models[0].Fields[2].GoName = goName
	return relationQueryGenerationPackages(authors, blog)
}

func collidingRelationQuerySurfaces() []codegen.RelationQueryPackage {
	target := ir.Schema{
		FormatVersion: ir.FormatVersion,
		AppLabel:      "target",
		Models: []ir.Model{{
			Name:   "record",
			GoName: "Record",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	source := func(appLabel, modelName, goName, reverse string) ir.Schema {
		return ir.Schema{
			FormatVersion: ir.RelationFormatVersion,
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

func validateGeneratedRelationQueryImportGraph(t *testing.T, directory string) {
	t.Helper()

	command := exec.Command("go", "list", "-mod=mod", "-json", "./...")
	command.Dir = directory
	command.Env = generatedTestEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("list generated relation query project imports: %v\n%s", err, output)
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

	const (
		authorsPath = "example.com/godj-relation-query-project/authors"
		blogPath    = "example.com/godj-relation-query-project/blog"
		projectPath = "example.com/godj-relation-query-project/project"
		ormPath     = "github.com/progresshans/godj/orm"
		irPath      = "github.com/progresshans/godj/schema/ir"
	)
	authors := packages[authorsPath]
	blog := packages[blogPath]
	project := packages[projectPath]
	for _, check := range []struct {
		name      string
		listed    listedPackage
		forbidden string
	}{
		{name: "authors -> blog", listed: authors, forbidden: blogPath},
		{name: "blog -> authors", listed: blog, forbidden: authorsPath},
	} {
		if slices.Contains(check.listed.Imports, check.forbidden) || slices.Contains(check.listed.Deps, check.forbidden) {
			t.Errorf("generated app direct import/dependency edge exists: %s", check.name)
		}
	}
	wantProjectImports := []string{authorsPath, blogPath, ormPath, irPath}
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
