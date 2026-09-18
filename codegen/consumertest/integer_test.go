package codegen_test

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
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
	output := runIntegerConsumerCommand(t, command)
	decoder := json.NewDecoder(bytes.NewReader(output))
	required := map[string]int{"TestIntegerGeneratedDefaults": 0, "TestIntegerStorageAndQuery": 0}
	packagePassed := false
	for {
		var event struct{ Action, Package, Test string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal("invalid generated integer test output")
		}
		if event.Action == "fail" || event.Action == "skip" {
			t.Fatal("generated integer consumer failed or skipped a required flow")
		}
		if event.Action == "pass" && event.Test == "" {
			packagePassed = true
		}
		if event.Action == "pass" {
			if _, found := required[event.Test]; found {
				required[event.Test]++
			}
		}
	}
	if !packagePassed {
		t.Fatal("generated integer consumer did not finish")
	}
	for name, count := range required {
		if count != 1 {
			t.Fatalf("required integer consumer %s completed %d times", name, count)
		}
	}
	if !consumerRaceEnabled {
		// Runtime remains on the parent platform; this compile checks that full
		// int64 defaults never acquire a platform-sized inferred int type.
		compile := generatedGoCommand(t.Context(), root, "build", "-mod=mod", "./models")
		compile.Env = testenv.With(compile.Env, map[string]string{"GOOS": "linux", "GOARCH": "386", "CGO_ENABLED": "0"})
		runIntegerConsumerCommand(t, compile)
	}
}

func runIntegerConsumerCommand(t *testing.T, command *exec.Cmd) []byte {
	t.Helper()
	var stdout, stderr gobuild.Capture
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		t.Fatalf("generated integer consumer: %v: %s", err, gobuild.Summary(stdout.Bytes(), stderr.Bytes(), command.Env))
	}
	if stdout.Len() != len(stdout.Bytes()) || stderr.Len() != len(stderr.Bytes()) || stderr.Len() != 0 {
		t.Fatal("generated integer consumer output was truncated or contained diagnostics")
	}
	return stdout.Bytes()
}
