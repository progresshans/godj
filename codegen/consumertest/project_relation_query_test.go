package codegen_test

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/codegen/internal/testfixture"
	"github.com/progresshans/godj/internal/testschema"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedProjectRelationQueryScalarOnlyModelsCompile(t *testing.T) {
	authors, _ := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-query-scalar-only"
	bindingSource, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{{Alias: "authors", ImportPath: modulePath + "/authors"}})
	if err != nil {
		t.Fatalf("generate scalar project binding: %v", err)
	}
	querySource, err := codegen.GenerateProjectRelationQuery("project", []codegen.RelationQueryPackage{{
		Alias: "authors", ImportPath: modulePath + "/authors", Schema: authors,
	}})
	if err != nil {
		t.Fatalf("generate scalar project query: %v", err)
	}
	if bytes.Contains(querySource, []byte("_model0")) {
		t.Fatalf("scalar-only project query binds an unused model:\n%s", querySource)
	}

	directory := newGeneratedModule(t, modulePath)
	writeGeneratedAppFixture(t, directory, "authors", "authors", authors, appFixtureFeatures{})
	writeGeneratedTestFile(t, directory, "project/zz_godj_bindings.go", bindingSource)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_query.go", querySource)
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("scalar-only generated project did not compile: %v\n%s\nproject source:\n%s", err, output, querySource)
	}
}

func TestGeneratedRelationQueryProjectCompilesBindsAndHasNoAppEdges(t *testing.T) {
	authors, blog := sparseRelationQueryGenerationSchemas()
	projectBinding, err := codegen.GenerateProjectBridge("project", []codegen.BridgePackage{
		{Alias: "authors", ImportPath: "example.com/godj-relation-query-project/authors"},
		{Alias: "blog", ImportPath: "example.com/godj-relation-query-project/blog"},
	})
	if err != nil {
		t.Fatalf("generate project binding: %v", err)
	}
	projectQuery, err := codegen.GenerateProjectRelationQuery("project", testfixture.QueryPackages(authors, blog))
	if err != nil {
		t.Fatalf("generate project relation query: %v", err)
	}

	directory := newGeneratedModule(t, "example.com/godj-relation-query-project")
	writeGeneratedAppFixture(t, directory, "authors", "authors", authors, appFixtureFeatures{})
	writeGeneratedAppFixture(t, directory, "blog", "blog", blog, appFixtureFeatures{})
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
	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated relation query project did not compile and bind: %v\n%s", err, output)
	}
	if got, want := bytes.Count(projectQuery, []byte("orm.BindModel(")), 4; got != want {
		t.Fatalf("generated relation query BindModel count = %d, want %d:\n%s", got, want, projectQuery)
	}
	for _, fragment := range [][]byte{
		[]byte("\t_ = _model1\n"),
		[]byte("\t_ = _model2\n"),
		[]byte(`orm.BindForward(_model3, "author", _model0)`),
	} {
		if !bytes.Contains(projectQuery, fragment) {
			t.Fatalf("sparse relation query source does not contain %q:\n%s", fragment, projectQuery)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("\t_ = _model0\n"),
		[]byte("\t_ = _model3\n"),
	} {
		if bytes.Contains(projectQuery, forbidden) {
			t.Fatalf("sparse relation query source consumes used binding %q:\n%s", forbidden, projectQuery)
		}
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

	authors, blog := testschema.QueryRelation()
	const modulePath = "example.com/godj-relation-query-alias"
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

	directory := newGeneratedModule(t, modulePath)
	writeGeneratedAppFixture(t, directory, "target", "target", authors, appFixtureFeatures{})
	writeGeneratedAppFixture(t, directory, "source", alias, blog, appFixtureFeatures{})
	writeGeneratedTestFile(t, directory, "project/zz_godj_binding.go", projectBinding)
	writeGeneratedTestFile(t, directory, "project/zz_godj_relation_query.go", projectQuery)

	command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "./...")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated project with alias %q did not compile: %v\n%s\nproject source:\n%s", alias, err, output, projectQuery)
	}
}

func sparseRelationQueryGenerationSchemas() (ir.Schema, ir.Schema) {
	authors, blog := testschema.QueryRelation()
	authors.Models = append(authors.Models,
		ir.Model{
			Name: "category", GoName: "Category",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 80},
			},
		},
		ir.Model{
			Name: "profile", GoName: "Profile",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
				{Name: "label", GoName: "Label", Kind: ir.FieldChar, MaxLength: 80},
			},
		},
	)
	blog.Models[0].Fields = append(blog.Models[0].Fields, ir.Field{
		Name: "category", GoName: "CategoryID", Kind: ir.FieldForeignKey, Nullable: true,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "category"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "categorized_posts"},
			OnDelete:    ir.DeleteSetNull,
		},
	})
	return authors, blog
}

func validateGeneratedRelationQueryImportGraph(t *testing.T, directory string) {
	t.Helper()

	command := generatedGoCommand(t.Context(), directory, "list", "-mod=mod", "-json", "./...")
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
