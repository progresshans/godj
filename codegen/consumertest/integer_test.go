package codegen_test

import (
	"math"
	"os"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedIntegerFieldConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "integers", Models: []schema.Model{{Name: "number", GoName: "Number", Fields: []schema.Field{
		schema.IntegerField("amount", "Amount"),
		schema.IntegerField("quota", "Quota", schema.Default(int64(0))),
		schema.IntegerField("optional", "Optional", schema.Nullable()),
		schema.IntegerField("minimum", "Minimum", schema.Nullable(), schema.Default(int64(math.MinInt64))),
		schema.IntegerField("maximum", "Maximum", schema.Nullable(), schema.Default(int64(math.MaxInt64))),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, "example.com/godj-integers")
	writeGeneratedAppFixture(t, root, "models", "models", definition, appFixtureFeatures{projection: true, object: true})
	consumer, err := os.ReadFile("testdata/integer/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	command.Env = testenv.With(command.Env, map[string]string{"GORACE": "", "GODEBUG": ""})
	output := runStrictGeneratedCommand(t, command)
	assertGeneratedConsumerTests(t, output, "TestIntegerGeneratedDefaults", "TestIntegerStorageAndQuery")
	if !consumerRaceEnabled {
		// Runtime remains on the parent platform; this compile checks that full
		// int64 defaults never acquire a platform-sized inferred int type.
		compile := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./models")
		compile.Env = testenv.With(compile.Env, map[string]string{"GOOS": "linux", "GOARCH": "386", "CGO_ENABLED": "0"})
		runStrictGeneratedCommand(t, compile)
	}
}
