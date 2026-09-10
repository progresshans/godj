package codegen_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func newGeneratedModule(t *testing.T, modulePath string) string {
	t.Helper()
	directory := t.TempDir()
	writeGeneratedTestFile(t, directory, "go.mod", []byte(fmt.Sprintf(`module %s

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, modulePath, filepath.ToSlash(codegenRepositoryRoot(t)))))
	checksums, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, directory, "go.sum", checksums)
	return directory
}

func codegenRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve codegen consumer test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func writeGeneratedTestFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create generated fixture directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write generated fixture %s: %v", name, err)
	}
}

func generatedGoCommand(ctx context.Context, directory string, arguments ...string) *exec.Cmd {
	if len(arguments) > 0 && arguments[0] == "test" {
		arguments = append([]string{"test", "-count=1"}, arguments[1:]...)
	}
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = directory
	command.Env = generatedTestEnvironment(os.Environ())
	return command
}

func generatedTestEnvironment(environment []string) []string {
	flags := "GOFLAGS="
	if consumerRaceEnabled {
		flags += "-race"
	}
	settings := []string{"GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GONOPROXY=none", flags}
	blocked := make(map[string]bool, len(settings))
	for _, setting := range settings {
		name, _, _ := strings.Cut(setting, "=")
		blocked[name] = true
	}
	result := make([]string, 0, len(environment)+len(settings))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if !found || !blocked[name] {
			result = append(result, entry)
		}
	}
	return append(result, settings...)
}

func TestGeneratedConsumerEnvironmentKeepsModeAndIsolatesHostControls(t *testing.T) {
	t.Parallel()
	input := []string{"CGO_ENABLED=0", "GOARCH=386", "GOCACHE=/shared-build-cache", "GOWORK=/host/workspace", "GOWORK=auto", "GOTOOLCHAIN=auto", "GOPROXY=https://host-proxy", "GOSUMDB=host-sumdb", "GONOPROXY=*", "GOFLAGS=-overlay=host.json"}
	before := slices.Clone(input)
	want := []string{"CGO_ENABLED=0", "GOARCH=386", "GOCACHE=/shared-build-cache", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GONOPROXY=none", "GOFLAGS="}
	if consumerRaceEnabled {
		want[len(want)-1] = "GOFLAGS=-race"
	}
	got := generatedTestEnvironment(input)
	if !slices.Equal(got, want) {
		t.Fatalf("consumer environment = %q, want %q", got, want)
	}
	got[0] = "mutated"
	if !slices.Equal(input, before) {
		t.Fatalf("environment construction mutated caller: %q", input)
	}
}

func TestGeneratedConsumerActuallyUsesParentRaceMode(t *testing.T) {
	directory := newGeneratedModule(t, "example.com/consumer-mode")
	writeGeneratedTestFile(t, directory, "race.go", []byte("//go:build race\n\npackage consumer\nconst instrumented = true\n"))
	writeGeneratedTestFile(t, directory, "norace.go", []byte("//go:build !race\n\npackage consumer\nconst instrumented = false\n"))
	writeGeneratedTestFile(t, directory, "mode_test.go", []byte(fmt.Sprintf(`package consumer
import "testing"
func TestMode(t *testing.T) {
    if instrumented != %t { t.Fatal("child race mode differs from parent") }
}
`, consumerRaceEnabled)))
	if output, err := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", ".").CombinedOutput(); err != nil {
		t.Fatalf("child mode: %v\n%s", err, output)
	}
}

func TestGeneratedConsumerCommandStopsBeforeStartingWhenCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	command := generatedGoCommand(ctx, t.TempDir(), "version")
	if err := command.Run(); !errors.Is(err, context.Canceled) || command.Process != nil {
		t.Fatalf("canceled command = %v, process = %v; want cancellation without starting Go", err, command.Process)
	}
}
