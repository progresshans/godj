package compatibility

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/testenv"
)

// A probe is useful before a host has acquired runtime modules. A previously
// warm module cache must not conceal an accidental storage/image dependency.
func TestCompatibilityProbeBuildsWithoutRuntimeModules(t *testing.T) {
	repository, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	const declaration = "module github.com/progresshans/godj\n"
	if !bytes.HasPrefix(module, []byte(declaration)) {
		t.Fatal("unexpected module authority")
	}
	root := t.TempDir()
	module = bytes.Replace(module, []byte(declaration), []byte("module example.com/offline-abi-probe\n"), 1)
	module = append(module, []byte(fmt.Sprintf("\nrequire github.com/progresshans/godj v0.0.0\nreplace github.com/progresshans/godj => %q\n", filepath.ToSlash(repository)))...)
	sums, err := os.ReadFile(filepath.Join(repository, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"go.mod": module, "go.sum": sums,
		"main.go": []byte("package main\nimport (\"encoding/json\";\"os\";\"github.com/progresshans/godj/project/compatibility\")\nfunc main(){json.NewEncoder(os.Stdout).Encode(compatibility.Current())}\n"),
	} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cache, err := exec.CommandContext(ctx, "go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	environment := testenv.Map(os.Environ())
	for name, value := range map[string]string{
		"HOME": filepath.Join(root, "home"), "GOMODCACHE": filepath.Join(root, "modules"), "GOPATH": filepath.Join(root, "gopath"),
		"GOCACHE": strings.TrimSpace(string(cache)), "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOFLAGS": "",
	} {
		environment[name] = value
	}
	command := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", os.DevNull, ".")
	command.Dir, command.Env = root, testenv.Sorted(environment)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ABI-only executable required runtime modules: %v\n%s", err, output)
	}
}
