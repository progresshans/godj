package codegen_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
)

func assertGeneratedConsumerTests(t *testing.T, output []byte, names ...string) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(output))
	required := make(map[string]int, len(names))
	for _, name := range names {
		required[name] = 0
	}
	packagePassed := false
	for {
		var event struct{ Action, Package, Test string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal("invalid generated test output")
		}
		if event.Action == "fail" || event.Action == "skip" {
			t.Fatal("generated consumer failed or skipped a required flow")
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
		t.Fatal("generated consumer did not finish")
	}
	for name, count := range required {
		if count != 1 {
			t.Fatalf("required generated consumer %s completed %d times", name, count)
		}
	}
}

func runStrictGeneratedCommand(t *testing.T, command *exec.Cmd) []byte {
	t.Helper()
	var stdout, stderr gobuild.Capture
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		t.Fatalf("generated consumer: %v: %s", err, gobuild.Summary(stdout.Bytes(), stderr.Bytes(), command.Env))
	}
	if stdout.Len() != len(stdout.Bytes()) || stderr.Len() != len(stderr.Bytes()) || stderr.Len() != 0 {
		t.Fatal("generated consumer output was truncated or contained diagnostics")
	}
	return stdout.Bytes()
}
