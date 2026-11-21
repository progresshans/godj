package codegen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedNullableForwardConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "nullable_reference", Models: []schema.Model{
		{Name: "author", GoName: "Author", Fields: []schema.Field{schema.CharField("name", "Name", 40), schema.IntegerField("rank", "Rank"), schema.TextField("bio", "Bio"), schema.DateTimeField("seen_at", "SeenAt")}},
		{Name: "post", GoName: "Post", Fields: []schema.Field{schema.CharField("title", "Title", 40), schema.ForeignKey("author", "AuthorID", schema.Target("nullable_reference", "author"), schema.RelatedName("posts"), schema.Protect), schema.ForeignKey("reviewer", "ReviewerID", schema.Target("nullable_reference", "author"), schema.RelatedName("reviews"), schema.SetNull, schema.Nullable())}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-nullable-forward"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/nullableforward/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/nullable-forward-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedNullableForwardReference")
}
