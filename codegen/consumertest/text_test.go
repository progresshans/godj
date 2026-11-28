package codegen_test

import (
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedTextFieldConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "notes", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{
		schema.TextField("body", "Body"),
		schema.TextField("abstract", "Abstract", schema.Nullable()),
		schema.TextField("seed", "Seed", schema.Default(strings.Repeat("line\n日本語 ", 1000))),
		schema.TextField("empty", "Empty", schema.Nullable(), schema.Default("")),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, "example.com/godj-text")
	writeGeneratedAppFixture(t, root, "models", "models", definition, appFixtureFeatures{projection: true, object: true})
	consumer, err := os.ReadFile("testdata/text/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	command.Env = testenv.With(command.Env, map[string]string{"GORACE": "", "GODEBUG": ""})
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestTextGeneratedDefaults", "TestTextStorageAndQuery")
}
