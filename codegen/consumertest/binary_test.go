package codegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/binarytest"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedBinaryConsumer(t *testing.T) {
	sample := binaryvalue.Value{Data: "\x00\xffa\x80"}
	definition, err := schema.Build(schema.Definition{AppLabel: "binaryref", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.BinaryField("reference", "Reference", schema.Nullable(), schema.Editable(true), schema.MaxLength(4), schema.Blank()),
			schema.BinaryField("required", "Required"), schema.BinaryField("origin", "Origin", schema.Nullable(), schema.Default(binaryvalue.Value{})),
			schema.BinaryField("scheduled", "Scheduled", schema.Default(sample)), schema.CharField("internal_note", "InternalNote", 32, schema.Editable(false), schema.Default("server")),
		}},
		{Name: "identifier", GoName: "Value", Fields: []schema.Field{
			schema.BinaryField("binary", "Binary", schema.Nullable()), schema.BinaryField("value", "Value", schema.Default(sample)),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("binaryref", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
			schema.BinaryField("token", "Token", schema.Default(sample)),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-binary"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/binary/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	for _, backend := range []string{"sqlite", "postgres"} {
		_ = binarytest.Load(t, backend)
		raw, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/binarytest/testdata/django61-"+backend+".json"))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/reference-"+backend+".json", raw)
	}
	required := []string{"TestBinaryGeneratedDefaults", "TestBinaryStorageQueryAndOwnership", "TestBinaryStorageQueryAndOwnership/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestBinaryStorageQueryAndOwnership/postgres")
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	for _, test := range []struct{ name, source, fragment string }{
		{"predicate rejects base64 text", `package wrong; import "example.com/godj-binary/models"; var _ = models.RecordFields.Reference.Exact("AP8=")`, "untyped string"},
		{"write rejects mutable slice", `package wrong; import "example.com/godj-binary/models"; var _ = models.RecordPatch{}.WithReference([]byte{0,255})`, "[]byte"},
		{"F rejects string", `package wrong; import "example.com/godj-binary/models"; import "github.com/progresshans/godj/orm"; var _ = models.RecordFields.Reference.ExactField(orm.F(models.RecordFields.Label))`, "string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(test.source))
			output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.fragment) || !strings.Contains(string(output), "binaryvalue.Value") {
				t.Fatalf("binary type control did not fail at its intended boundary: %v\n%s", err, output)
			}
		})
	}
}
