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
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = directory
	command.Env = generatedTestEnvironment(os.Environ())
	return command
}

func generatedTestEnvironment(environment []string, overrides ...string) []string {
	settings := append([]string{"GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off"}, overrides...)
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

func TestGeneratedConsumerEnvironmentKeepsModeAndIndependentOverrides(t *testing.T) {
	t.Parallel()
	input := []string{"CGO_ENABLED=0", "GOARCH=386", "GOCACHE=/shared-build-cache", "GOWORK=/host/workspace", "GOWORK=auto", "GOTOOLCHAIN=auto", "GOPROXY=https://host-proxy", "GOSUMDB=host-sumdb"}
	before := slices.Clone(input)
	want := []string{"CGO_ENABLED=0", "GOARCH=386", "GOCACHE=/shared-build-cache", "GOSUMDB=host-sumdb", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off"}
	if got := generatedTestEnvironment(input); !slices.Equal(got, want) {
		t.Fatalf("consumer environment = %q, want %q", got, want)
	}
	strict := generatedTestEnvironment(input, "GOSUMDB=off")
	if slices.Contains(strict, "GOSUMDB=host-sumdb") || !slices.Contains(strict, "GOSUMDB=off") {
		t.Fatalf("bundle checksum override = %q", strict)
	}
	strict[0] = "mutated"
	if !slices.Equal(input, before) {
		t.Fatalf("environment construction mutated caller: %q", input)
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
