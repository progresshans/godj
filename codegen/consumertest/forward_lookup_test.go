package codegen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedForwardLookupConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "forward_lookup", Models: []schema.Model{
		{Name: "person", GoName: "Person", Fields: []schema.Field{schema.CharField("name", "Name", 40), schema.CharField("nickname", "Nickname", 40, schema.Nullable()), schema.IntegerField("score", "Score", schema.Nullable()), schema.TextField("bio", "Bio", schema.Nullable()), schema.DateTimeField("seen_at", "SeenAt", schema.Nullable()), schema.BooleanField("active", "Active")}},
		{Name: "post", GoName: "Post", Fields: []schema.Field{schema.CharField("title", "Title", 40), schema.ForeignKey("author", "AuthorID", schema.Target("forward_lookup", "person"), schema.RelatedName("posts"), schema.Protect), schema.ForeignKey("reviewer", "ReviewerID", schema.Target("forward_lookup", "person"), schema.RelatedName("reviews"), schema.SetNull, schema.Nullable())}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-forward-lookups"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/forwardlookup/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/forward-lookups-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedForwardLookupReference")
}
