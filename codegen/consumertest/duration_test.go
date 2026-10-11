package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedDurationConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "durationref", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.DurationField("elapsed", "Elapsed", schema.Nullable()),
			schema.DurationField("required", "Required"),
			schema.DurationField("origin", "Origin", schema.Nullable(), schema.Default(duration.Duration{})),
			schema.DurationField("scheduled", "Scheduled", schema.Default(duration.Duration{Days: 1, Microseconds: 7384123456})),
		}},
		{Name: "clock", GoName: "Duration", Fields: []schema.Field{
			schema.DurationField("duration", "Duration", schema.Nullable()),
			schema.DurationField("duration_value", "DurationValue", schema.Default(duration.Duration{})),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("durationref", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-duration"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/duration/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/durationtest/testdata/django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestDurationGeneratedDefaults", "TestDurationStorageQueryAndOwnership", "TestDurationStorageQueryAndOwnership/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestDurationStorageQueryAndOwnership/postgres")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
