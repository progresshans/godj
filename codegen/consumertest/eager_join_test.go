package codegen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedEagerJoinConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "join_reference", Models: []schema.Model{
		{Name: "person", GoName: "Person", Fields: []schema.Field{schema.CharField("name", "Name", 40), schema.CharField("nickname", "Nickname", 40, schema.Nullable()), schema.BooleanField("active", "Active")}},
		{Name: "post", GoName: "Post", Fields: []schema.Field{schema.CharField("title", "Title", 40), schema.ForeignKey("author", "AuthorID", schema.Target("join_reference", "person"), schema.RelatedName("posts"), schema.Protect), schema.ForeignKey("reviewer", "ReviewerID", schema.Target("join_reference", "person"), schema.RelatedName("reviews"), schema.SetNull, schema.Nullable())}},
		{Name: "comment", GoName: "Comment", Fields: []schema.Field{schema.ForeignKey("post", "PostID", schema.Target("join_reference", "post"), schema.RelatedName("comments"), schema.Protect), schema.CharField("body", "Body", 40)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-eager-joins"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/eagerjoin/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "orm/testdata/eager-joins-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedEagerJoinReference", "TestGeneratedEagerJoinOwnsDuplicateRows")
}
