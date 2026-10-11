package codegen_test

import (
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedBlankPolicyConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "blank_reference", Models: []schema.Model{
		{Name: "label", GoName: "Label", Fields: []schema.Field{schema.CharField("name", "Name", 30)}},
		{Name: "owner", GoName: "Owner", Fields: []schema.Field{
			schema.CharField("name", "Name", 30, schema.Blank()),
			schema.EmailField("address", "Address", schema.Nullable(), schema.Blank()),
			schema.IntegerField("score", "Score", schema.Default(int64(3)), schema.Blank()),
		}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target("blank_reference", "label"), schema.RelatedName("owners"), schema.ManyToManyBlank())}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-blank"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/blank/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestGeneratedBlankPolicyStorageAndHistory", "TestGeneratedBlankPolicyStorageAndHistory/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestGeneratedBlankPolicyStorageAndHistory/postgres")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
