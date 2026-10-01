package codegen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/testenv"
)

func TestGeneratedConsumerReusesBuildCacheAcrossIsolatedDirectories(t *testing.T) {
	const source = `package cacheprobe
import _ "embed"
//go:embed payload.txt
var payload string
func version() int { return 1 }
`
	const test = `package cacheprobe
import (
    "os"
    "strconv"
    "testing"
)
func TestExecution(t *testing.T) {
    want, err := strconv.Atoi(os.Getenv("GODJ_CACHE_PROBE_VERSION"))
    if err != nil || version() != want || payload != "owned embedded input\n" {
        t.Fatal("compiled source or embedded input changed", version(), want, err)
    }
    file, err := os.OpenFile(os.Getenv("GODJ_CACHE_PROBE_MARKER"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
    if err != nil { t.Fatal(err) }
    _, writeErr := file.WriteString("ran\n")
    closeErr := file.Close()
    if writeErr != nil || closeErr != nil { t.Fatal(writeErr, closeErr) }
}
`
	newModule := func() string {
		directory := newGeneratedModule(t, "example.com/godj-cache-probe")
		writeGeneratedTestFile(t, directory, "probe.go", []byte(source))
		writeGeneratedTestFile(t, directory, "probe_test.go", []byte(test))
		writeGeneratedTestFile(t, directory, "payload.txt", []byte("owned embedded input\n"))
		return directory
	}
	first, second := newModule(), newModule()
	if first == second {
		t.Fatal("consumer modules lost writable directory isolation")
	}
	type artifact struct{ Dir, BuildID, Export string }
	inspect := func(directory string) artifact {
		t.Helper()
		output := runStrictGeneratedCommand(t, generatedGoCommand(t.Context(), directory, "list", "-mod=mod", "-export", "-json", "."))
		var result artifact
		if err := json.Unmarshal(output, &result); err != nil || result.BuildID == "" || result.Export == "" || result.Dir == "" {
			t.Fatal("missing package build identity or changed execution directory", err)
		}
		originalDirectory, originalErr := os.Stat(directory)
		listedDirectory, listedErr := os.Stat(result.Dir)
		if originalErr != nil || listedErr != nil || !os.SameFile(originalDirectory, listedDirectory) {
			t.Fatal("consumer execution directory changed", originalErr, listedErr)
		}
		if info, err := os.Stat(result.Export); err != nil || !info.Mode().IsRegular() {
			t.Fatal("compiled cache artifact missing", err)
		}
		return result
	}
	before := inspect(first)
	other := inspect(second)
	if before.BuildID != other.BuildID || before.Export != other.Export {
		t.Fatal("equivalent isolated modules did not reuse the compiled cache artifact")
	}
	// A marker outside either module is intentionally not an input tracked by
	// Go's test-result cache. Repeating identical commands must still execute
	// the test, including when its compiled artifact is reused.
	marker := filepath.Join(t.TempDir(), "executions.txt")
	runs := 0
	execute := func(directory, version string) {
		t.Helper()
		command := generatedGoCommand(t.Context(), directory, "test", "-mod=mod", "-json", ".")
		command.Env = testenv.With(command.Env, map[string]string{"GODJ_CACHE_PROBE_MARKER": marker, "GODJ_CACHE_PROBE_VERSION": version})
		assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), "TestExecution")
		runs++
		contents, err := os.ReadFile(marker)
		if err != nil || string(contents) != strings.Repeat("ran\n", runs) {
			t.Fatal("successful test result was reused instead of executing the consumer", err)
		}
	}
	execute(first, "1")
	execute(first, "1")
	execute(second, "1")
	writeGeneratedTestFile(t, second, "probe.go", []byte(strings.Replace(source, "return 1", "return 2", 1)))
	changed := inspect(second)
	if changed.BuildID == before.BuildID || changed.Export == before.Export {
		t.Fatal("source change reused the old compiled artifact")
	}
	execute(second, "2")
	retained := inspect(first)
	if retained.BuildID != before.BuildID || retained.Export != before.Export {
		t.Fatal("another module's source edit invalidated unchanged input")
	}
	execute(first, "1")
}
