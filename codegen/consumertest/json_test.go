package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedJSONConsumer(t *testing.T) {
	sample, err := jsonvalue.Parse([]byte(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := schema.Build(schema.Definition{AppLabel: "jsonref", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.JSONField("payload", "Payload", schema.Nullable()),
			schema.JSONField("required", "Required"),
			schema.JSONField("origin", "Origin", schema.Nullable(), schema.Default(jsonvalue.Null())),
			schema.JSONField("scheduled", "Scheduled", schema.Default(sample)),
		}},
		{Name: "identifier", GoName: "JSON", Fields: []schema.Field{
			schema.JSONField("json", "JSON", schema.Nullable()),
			schema.JSONField("json_value", "JSONValue", schema.Default(sample)),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("jsonref", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
			schema.JSONField("token", "Token", schema.Default(sample)),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-json"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/json/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	paths, err := os.ReadFile("testdata/json/json_path_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/json_path_test.go", paths)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/jsontest/testdata/django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)

	containment, err := os.ReadFile("testdata/json/json_containment_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/json_containment_test.go", containment)
	containmentRaw, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/jsontest/testdata/django61-lookups-postgres.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/containment_reference.json", containmentRaw)
	keys, err := os.ReadFile("testdata/json/json_keys_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/json_keys_test.go", keys)
	writeGeneratedTestFile(t, root, "consumer/keys_postgres_reference.json", containmentRaw)
	sqliteRaw, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/jsontest/testdata/django61-lookups-sqlite.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/keys_sqlite_reference.json", sqliteRaw)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestJSONGeneratedDefaults", "TestJSONStorageQueryAndOwnership", "TestJSONStorageQueryAndOwnership/sqlite", "TestJSONStorageQueryAndOwnership/sqlite/paths", "TestJSONStorageQueryAndOwnership/sqlite/containment", "TestJSONStorageQueryAndOwnership/sqlite/keys"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestJSONStorageQueryAndOwnership/postgres", "TestJSONStorageQueryAndOwnership/postgres/paths", "TestJSONStorageQueryAndOwnership/postgres/containment", "TestJSONStorageQueryAndOwnership/postgres/keys")
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	for _, test := range []struct{ name, source, fragment string }{
		{"predicate rejects string", `package wrong; import "example.com/godj-json/models"; var _ = models.RecordFields.Payload.Exact("null")`, "untyped string"},
		{"path rejects string", `package wrong; import "example.com/godj-json/models"; import "github.com/progresshans/godj/query"; var _ = models.RecordFields.Payload.At(query.JSONKey("a")).Exact("null")`, "untyped string"},
		{"contains rejects string", `package wrong; import "example.com/godj-json/models"; var _ = models.RecordFields.Payload.Contains("{}")`, "untyped string"},
		{"write rejects integer", `package wrong; import "example.com/godj-json/models"; var _ = models.RecordPatch{}.WithPayload(int64(1))`, "int64"},
		{"F rejects text", `package wrong; import "example.com/godj-json/models"; import "github.com/progresshans/godj/orm"; var _ = models.RecordFields.Payload.ExactField(orm.F(models.RecordFields.Label))`, "string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(test.source))
			output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.fragment) || !strings.Contains(string(output), "jsonvalue.Value") {
				t.Fatalf("wrong JSON compilation did not fail at expected type boundary: %v\n%s", err, output)
			}
		})
	}
	for _, source := range []string{
		`package wrong; import "example.com/godj-json/models"; var _ = models.RecordFields.Payload.HasKey(1)`,
		`package wrong; import "example.com/godj-json/models"; var _ = models.RecordFields.Payload.HasKeys("a", 1)`,
	} {
		writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(source))
		output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
		if err == nil || !strings.Contains(string(output), "cannot use 1") || !strings.Contains(string(output), "string") {
			t.Fatalf("invalid JSON key type compiled: %v\n%s", err, output)
		}
	}
}
