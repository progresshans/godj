package codegen_test

import (
	"github.com/progresshans/godj/internal/testenv"
	"github.com/progresshans/godj/schema"
	"os"
	"testing"
	"time"
)

func TestGeneratedDateTimeFieldConsumer(t *testing.T) {
	definition, err := schema.Build(schema.Definition{AppLabel: "events", Models: []schema.Model{{Name: "event", GoName: "Event", Fields: []schema.Field{
		schema.DateTimeField("time", "Time"),
		schema.DateTimeField("time_value", "TimeValue", schema.Nullable()),
		schema.DateTimeField("optional", "Optional", schema.Nullable()),
		schema.DateTimeField("scheduled", "Scheduled", schema.Default(time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("declaration", 9*3600)))),
		schema.DateTimeField("origin", "Origin", schema.Nullable(), schema.Default(time.Time{})),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, "example.com/godj-datetime")
	writeGeneratedAppFixture(t, root, "models", "models", definition, appFixtureFeatures{projection: true, object: true})
	consumer, err := os.ReadFile("testdata/datetime/consumer_test.go")
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/consumer_test.go", consumer)
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	command.Env = testenv.With(command.Env, map[string]string{"GORACE": "", "GODEBUG": "", "TZ": "Pacific/Chatham"})
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestDateTimeGeneratedDefaults", "TestDateTimeStorageAndQuery")
	if !consumerRaceEnabled {
		compile := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./models")
		compile.Env = testenv.With(compile.Env, map[string]string{"GOOS": "linux", "GOARCH": "386", "CGO_ENABLED": "0"})
		runStrictGeneratedCommand(t, compile)
	}
}
