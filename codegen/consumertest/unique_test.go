package codegen_test

import (
	"os"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedUniqueMetadataConsumer(t *testing.T) {
	fields := []schema.Field{
		schema.CharField("reference", "Reference", 24), schema.TextField("body", "Body"),
		schema.IntegerField("number", "Number"), schema.BooleanField("flag", "Flag"),
		schema.FloatField("ratio", "Ratio"), schema.DecimalField("cost", "Cost", 8, 2),
		schema.UUIDField("external", "External"), schema.JSONField("payload", "Payload"),
		schema.DateField("day", "Day"), schema.DateTimeField("created", "Created"),
		schema.TimeField("clock", "Clock"), schema.DurationField("elapsed", "Elapsed"),
		schema.ForeignKey("parent", "Parent", schema.Target("unique", "entry"), schema.RelatedName("children"), schema.Protect),
	}
	for index := range fields {
		schema.Unique()(&fields[index])
		schema.Nullable()(&fields[index])
	}
	fields = append(fields, schema.TextField("note", "Note"))
	definition, err := schema.Build(schema.Definition{AppLabel: "unique", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, "example.com/godj-unique")
	writeGeneratedAppFixture(t, root, "models", "models", definition, appFixtureFeatures{projection: true, object: true})
	consumer, err := os.ReadFile("testdata/unique/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	command.Env = testenv.With(command.Env, map[string]string{"GORACE": "", "GODEBUG": ""})
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedUniqueMetadataOwnsCompleteSchema")
}
