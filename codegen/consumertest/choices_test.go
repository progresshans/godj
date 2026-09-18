package codegen_test

import (
	"os"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedChoicesConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "choices", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.CharField("status", "Status", 12, schema.Default("open"), schema.Choices(schema.Choice("open", "<Open>"), schema.Choice("closed", "Closed"))),
		schema.TextField("reason", "Reason", schema.Nullable(), schema.Choices(schema.Choice(" done ", "Keep spaces"), schema.Choice("new\nline", "Multiline"), schema.Choice("", "Blank"))),
		schema.IntegerField("priority", "Priority", schema.Nullable(), schema.Choices(schema.Choice(int64(1), "Urgent"), schema.Choice(int64(0), "Normal"), schema.Choice(int64(-1), "Low"))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, "example.com/godj-choices")
	writeGeneratedAppFixture(t, root, "models", "models", definition, appFixtureFeatures{projection: true, object: true})
	consumer, err := os.ReadFile("testdata/choices/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	command.Env = testenv.With(command.Env, map[string]string{"GORACE": "", "GODEBUG": ""})
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestGeneratedChoiceMetadataAndInputConsumers", "TestGeneratedChoicesDoNotConstrainStorage")
}
