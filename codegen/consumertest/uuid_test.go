package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/uuid"
)

func TestGeneratedUUIDConsumer(t *testing.T) {
	sample, err := uuid.Parse("12345678-9abc-4def-8123-456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := schema.Build(schema.Definition{AppLabel: "uuidref", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.UUIDField("reference", "Reference", schema.Nullable()),
			schema.UUIDField("required", "Required"),
			schema.UUIDField("origin", "Origin", schema.Nullable(), schema.Default(uuid.UUID{})),
			schema.UUIDField("scheduled", "Scheduled", schema.Default(sample)),
		}},
		{Name: "identifier", GoName: "UUID", Fields: []schema.Field{
			schema.UUIDField("uuid", "UUID", schema.Nullable()),
			schema.UUIDField("uuid_value", "UUIDValue", schema.Default(sample)),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("uuidref", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
			schema.UUIDField("token", "Token", schema.Default(sample)),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-uuid"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/uuid/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/uuidtest/testdata/django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestUUIDGeneratedDefaults", "TestUUIDStorageQueryAndOwnership", "TestUUIDStorageQueryAndOwnership/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestUUIDStorageQueryAndOwnership/postgres")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	for _, test := range []struct{ name, source, fragment string }{
		{"predicate rejects string", `package wrong; import "example.com/godj-uuid/models"; var _ = models.RecordFields.Reference.Exact("00000000-0000-0000-0000-000000000000")`, "untyped string"},
		{"write rejects integer", `package wrong; import "example.com/godj-uuid/models"; var _ = models.RecordPatch{}.WithReference(int64(1))`, "int64"},
		{"reference rejects text", `package wrong; import "example.com/godj-uuid/models"; import "github.com/progresshans/godj/orm"; var _ = models.RecordFields.Reference.ExactField(orm.F(models.RecordFields.Label))`, "string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(test.source))
			output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.fragment) || !strings.Contains(string(output), "uuid.UUID") {
				t.Fatalf("wrong UUID compilation did not fail at expected type boundary: %v\n%s", err, output)
			}
		})
	}
}
