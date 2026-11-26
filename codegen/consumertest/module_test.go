package codegen_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
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
	// The host test process stays in this package directory. Its compiler
	// source paths may be trimmed, just like those of generated child modules.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
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
		// Generated projects can instantiate several large typed model packages
		// at once. Bound compiler processes without changing in-package parallelism,
		// race instrumentation or the complete requested package/test set.
		arguments = append([]string{"test", "-p=1", "-count=1"}, arguments[1:]...)
	}
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = directory
	command.Env = generatedTestEnvironment(os.Environ())
	return command
}

func generatedTestEnvironment(environment []string) []string {
	// Each consumer keeps its own writable module. Trim only compiler paths so
	// equivalent generated packages can share build artifacts across TempDirs.
	// Test results remain uncached via -count=1 in generatedGoCommand.
	flags := "-trimpath"
	if consumerRaceEnabled {
		flags += " -race"
	}
	return testenv.OfflineGo(environment, flags)
}

func TestGeneratedConsumerEnvironmentKeepsModeAndIsolatesHostControls(t *testing.T) {
	t.Parallel()
	input := []string{"CGO_ENABLED=0", "GOARCH=386", "GOCACHE=/shared-build-cache", "GOWORK=/host/workspace", "GOWORK=auto", "GOTOOLCHAIN=auto", "GOPROXY=https://host-proxy", "GOSUMDB=host-sumdb", "GONOPROXY=*", "GOFLAGS=-overlay=host.json"}
	before := slices.Clone(input)
	want := []string{"CGO_ENABLED=0", "GOARCH=386", "GOCACHE=/shared-build-cache", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GONOPROXY=none", "GOFLAGS=-trimpath"}
	if consumerRaceEnabled {
		want[len(want)-1] = "GOFLAGS=-trimpath -race"
	}
	want = append(want, "GOENV=off", "GOCACHEPROG=")
	slices.Sort(want)
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
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("parent build metadata unavailable")
	}
	cgo := ""
	for _, setting := range info.Settings {
		if setting.Key == "CGO_ENABLED" {
			cgo = setting.Value
		}
	}
	if cgo != "0" && cgo != "1" {
		t.Fatal("parent CGO metadata unavailable")
	}
	writeGeneratedTestFile(t, directory, "mode_test.go", []byte(fmt.Sprintf(`package consumer
import ("testing"; "runtime/debug")
func TestMode(t *testing.T) {
    info, ok := debug.ReadBuildInfo()
    if !ok {t.Fatal("missing child build info")}
    settings:=map[string]string{}
    for _,v:=range info.Settings {settings[v.Key]=v.Value}
    if settings["GOOS"]!=%q || settings["GOARCH"]!=%q || settings["CGO_ENABLED"]!=%q || (settings["-race"]=="true")!=%t || instrumented!=%t { t.Fatal("generated binary coordinate differs from parent") }
    t.Logf("GODJ_CHILD_MODE goos=%%s goarch=%%s cgo=%%s race=%%t",settings["GOOS"],settings["GOARCH"],settings["CGO_ENABLED"],instrumented)
}
`, runtime.GOOS, runtime.GOARCH, cgo, consumerRaceEnabled, consumerRaceEnabled)))
	cmd := generatedGoCommand(t.Context(), directory, "test", "-json", "-mod=mod", ".")
	raw := runStrictGeneratedCommandWithCapture(t, cmd, func(raw []byte) error { return retainNativeChild("mode", raw) })
	assertGeneratedConsumerTests(t, raw, "TestMode")
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

func retainNativeChild(name string, raw []byte) error {
	dir := os.Getenv("GODJ_NATIVE_EVIDENCE_DIR")
	if dir == "" {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(dir, name+"-child.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	return errors.Join(err, file.Close())
}
