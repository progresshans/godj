package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedNullableBooleanConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "nullablebool", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.BooleanField("flag", "Flag", schema.Nullable()),
			schema.BooleanField("fallback_false", "FallbackFalse", schema.Nullable(), schema.Default(false)),
			schema.BooleanField("fallback_true", "FallbackTrue", schema.Nullable(), schema.Default(true)),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("nullablebool", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-nullable-boolean"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/nullableboolean/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/nullablebooleantest/testdata/django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestNullableBooleanGeneratedDefaults", "TestNullableBooleanStorageQueryAndOwnership", "TestNullableBooleanStorageQueryAndOwnership/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestNullableBooleanStorageQueryAndOwnership/postgres")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
