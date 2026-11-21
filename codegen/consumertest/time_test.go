package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedClockTimeConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "clocktime", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.TimeField("at", "At", schema.Nullable()),
			schema.TimeField("required", "Required"),
			schema.TimeField("origin", "Origin", schema.Nullable(), schema.Default(clock.Time{})),
			schema.TimeField("scheduled", "Scheduled", schema.Default(clock.Time{Hour: 12, Minute: 34, Second: 56, Microsecond: 123456})),
		}},
		{Name: "clock", GoName: "Time", Fields: []schema.Field{
			schema.TimeField("time", "Time", schema.Nullable()),
			schema.TimeField("time_value", "TimeValue", schema.Default(clock.Time{})),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("clocktime", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-clock-time"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/clocktime/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/clocktimetest/testdata/django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestClockTimeGeneratedDefaults", "TestClockTimeStorageQueryAndOwnership", "TestClockTimeStorageQueryAndOwnership/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestClockTimeStorageQueryAndOwnership/postgres")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
