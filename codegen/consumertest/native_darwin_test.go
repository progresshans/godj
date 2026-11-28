package codegen_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/progresshans/godj/codegen"
)

// The full resource consumer owns baseline/changed, both databases, all surface
// checks and negative compilation on Linux. This owner runs the actual baseline
// SDK generator, independent client and durable HTTP/database comparisons.
func TestNativeGeneratedModelResourceClient(t *testing.T) {
	started := time.Now()
	root := prepareModelResourceModule(t, "baseline", false)
	nativeFixturePrepared(t, started)
	runNativeGeneratedChild(t, root, "sdk", "^TestModelResourceAssembly$/^sqlite$/^generated_client_change$",
		"TestModelResourceAssembly", "TestModelResourceAssembly/sqlite", "TestModelResourceAssembly/sqlite/generated_client_change")
}

func TestNativeGeneratedFilesystemLifecycle(t *testing.T) {
	started := time.Now()
	bundle, err := codegen.GenerateProject(fileConsumerProject(t, documentAndPhotoModels()))
	if err != nil {
		t.Fatal(err)
	}
	root := newGeneratedModule(t, fileModule)
	for _, file := range bundle.Files() {
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	for _, name := range []string{"native_test.go", "serving_test.go"} {
		content, err := os.ReadFile(filepath.Join("testdata", "files", name))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, content)
	}
	nativeFixturePrepared(t, started)
	runNativeGeneratedChild(t, root, "filesystem", "^TestFilesystemLifecycle$",
		"TestFilesystemLifecycle", "TestFilesystemLifecycle/publication_http",
		"TestFilesystemLifecycle/publication_http/ranges_and_conditionals", "TestFilesystemLifecycle/image_rollback_reopen")
}

func runNativeGeneratedChild(t *testing.T, root, suite, pattern string, required ...string) {
	t.Helper()
	cmd := generatedGoCommand(t.Context(), root, "test", "-mod=mod", "-json", "-timeout=10m", "-run", pattern, "./consumer")
	raw := runStrictGeneratedCommandWithCapture(t, cmd, func(output []byte) error {
		return retainNativeChild(suite, output)
	})
	assertGeneratedConsumerTests(t, raw, required...)
}
