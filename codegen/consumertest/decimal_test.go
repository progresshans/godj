package codegen_test

import (
	"github.com/progresshans/godj/decimal"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedDecimalConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "decimalref", Models: []schema.Model{
		{Name: "record", GoName: "Record", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.DecimalField("cost", "Cost", 12, 2, schema.Nullable()),
			schema.DecimalField("required", "Required", 12, 2),
			schema.DecimalField("origin", "Origin", 12, 2, schema.Nullable(), schema.Default(decimal.Decimal{})),
			schema.DecimalField("scheduled", "Scheduled", 12, 2, schema.Default(decimal.Decimal{Coefficient: "15", Exponent: -1})),
		}},
		{Name: "number", GoName: "Decimal", Fields: []schema.Field{
			schema.DecimalField("decimal", "Decimal", 12, 2, schema.Nullable()),
			schema.DecimalField("decimal_value", "DecimalValue", 12, 2, schema.Default(decimal.Decimal{Coefficient: "-0"})),
		}},
		{Name: "precise", GoName: "Precise", Fields: []schema.Field{
			schema.DecimalField("value", "Value", 30, 12),
			schema.DecimalField("mirror", "Mirror", 32, 14),
		}},
		{Name: "boundary", GoName: "Boundary", Fields: []schema.Field{
			schema.DecimalField("large", "Large", 1000, 500),
			schema.DecimalField("tiny", "Tiny", 1000, 1000),
		}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{
			schema.TextField("label", "Label"), schema.ForeignKey("record", "RecordID", schema.Target("decimalref", "record"), schema.RelatedName("links"), schema.SetNull, schema.Nullable()),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-decimal"
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: definition}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, module)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	nextDefinition := definition.Clone()
	for modelIndex := range nextDefinition.Models {
		for fieldIndex := range nextDefinition.Models[modelIndex].Fields {
			field := &nextDefinition.Models[modelIndex].Fields[fieldIndex]
			if nextDefinition.Models[modelIndex].Name == "record" && field.Name == "cost" {
				field.Decimal.MaxDigits = 14
			}
		}
	}
	nextBundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "nextproject", ImportPath: module + "/nextproject", Directory: "nextproject"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "nextmodels", ImportPath: module + "/nextmodels", Directory: "nextmodels"}, Schema: nextDefinition}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range nextBundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	consumer, err := os.ReadFile("testdata/decimal/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	precisionConsumer, err := os.ReadFile("testdata/decimal/precision_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/precision_test.go", precisionConsumer)
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/decimaltest/testdata/django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	precisionReference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "internal/decimaltest/testdata/precision-changes-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/precision_reference.json", precisionReference)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	required := []string{"TestDecimalGeneratedDefaults", "TestDecimalStorageQueryAndOwnership", "TestDecimalStorageQueryAndOwnership/sqlite"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestDecimalStorageQueryAndOwnership/postgres")
	}
	required = append(required, "TestDecimalPrecisionMigration", "TestDecimalPrecisionIncomingRelation", "TestDecimalPrecisionIncomingRelation/sqlite", "TestDecimalGeneratedPrecisionEvolution", "TestDecimalGeneratedPrecisionEvolution/sqlite")
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestDecimalGeneratedPrecisionEvolution/postgres", "TestDecimalPrecisionIncomingRelation/postgres")
	}
	for _, name := range []string{"widen_whole", "widen_scale_and_precision", "reduce_scale_exact", "reduce_scale_rounding", "reduce_whole_safe", "reduce_whole_overflow", "scale_uses_existing_whole_capacity", "zero_whole_digits", "zero_scale_exact", "zero_scale_rounding", "empty_tightening", "identity"} {
		required = append(required, "TestDecimalPrecisionMigration/"+name+"/sqlite")
		if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
			required = append(required, "TestDecimalPrecisionMigration/"+name+"/postgres")
		}
	}
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
	for _, test := range []struct{ name, source, fragment string }{
		{"predicate rejects float", `package wrong; import "example.com/godj-decimal/models"; var _ = models.RecordFields.Cost.Exact(float64(1))`, "float64(1)"},
		{"write rejects string", `package wrong; import "example.com/godj-decimal/models"; var _ = models.RecordPatch{}.WithCost("1.5")`, "untyped string"},
		{"reference rejects integer", `package wrong; import "example.com/godj-decimal/models"; import "github.com/progresshans/godj/orm"; var _ = models.RecordFields.Cost.ExactField(orm.F(models.RecordFields.ID))`, "int64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeGeneratedTestFile(t, root, "wrong/wrong.go", []byte(test.source))
			output, err := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./wrong").CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.fragment) || !strings.Contains(string(output), "decimal.Decimal") {
				t.Fatalf("wrong scalar compilation did not fail at expected type boundary: %v\n%s", err, output)
			}
		})
	}
}
