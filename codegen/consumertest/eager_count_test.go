package codegen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedEagerCountConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "count_reference", Models: []schema.Model{
		{Name: "author", GoName: "Author", Fields: []schema.Field{schema.CharField("name", "Name", 40)}},
		{Name: "post", GoName: "Post", Fields: []schema.Field{
			schema.CharField("title", "Title", 40),
			schema.ForeignKey("author", "AuthorID", schema.Target("count_reference", "author"), schema.RelatedName("posts"), schema.Protect),
			schema.ForeignKey("reviewer", "ReviewerID", schema.Target("count_reference", "author"), schema.RelatedName("reviews"), schema.SetNull, schema.Nullable()),
		}},
		{Name: "comment", GoName: "Comment", Fields: []schema.Field{
			schema.ForeignKey("post", "PostID", schema.Target("count_reference", "post"), schema.RelatedName("comments"), schema.Protect),
			schema.CharField("body", "Body", 40),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-eager-count"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"},
		Apps:    []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/eagercount/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/eager-count-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/eager-count-django61.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedEagerCountReference", "TestGeneratedEagerCountPublicSurfaces")
}
