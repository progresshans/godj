// Package sourceaudit cross-checks test-owned attestation inventories against
// the native Go toolchain's actual local dependencies and embedded assets.
// It is used by conformance tests, never by a product request or capture loader.
package sourceaudit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/testenv"
)

// RequireDependencies does not derive an expected inventory from the owner's
// own lists. Go independently reports files selected for the current build
// mode, including native sources and go:embed assets. Every local dependency
// must be hashed and its containing path protected from symlink substitution.
func RequireDependencies(t *testing.T, owned, protected func(string) bool, targets ...string) {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("locate source audit repository")
		}
		root = parent
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.Contains(string(module), "module github.com/progresshans/godj\n") {
		t.Fatal("source audit root is not GoDj")
	}
	// Print only local dependency file paths. %q provides an unambiguous frame
	// across host paths containing whitespace; no file contents are emitted.
	template := `{{if and .Module (eq .Module.Path "github.com/progresshans/godj")}}{{range .GoFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{range .CgoFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{range .EmbedFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{range .CFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{range .HFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{range .SFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{range .SysoFiles}}{{printf "%q\n" (printf "%s/%s" $.Dir .)}}{{end}}{{end}}`
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", append([]string{"list", "-deps", "-f", template}, targets...)...)
	command.Dir = root
	command.Env = testenv.OfflineGo(os.Environ(), dependencyFlags)
	command.WaitDelay = 5 * time.Second
	var output, diagnostic gobuild.Capture
	command.Stdout = &output
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		t.Fatalf("native dependency discovery failed: %v: %s", err, gobuild.Summary(output.Bytes(), diagnostic.Bytes(), command.Env))
	}
	if output.Len() != len(output.Bytes()) || diagnostic.Len() != 0 {
		t.Fatal("dependency discovery was truncated or had diagnostics")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(output.Bytes()), "\n") {
		if line == "" {
			continue
		}
		absolute, err := strconv.Unquote(line)
		if err != nil {
			t.Fatal("invalid native dependency framing")
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil || !filepath.IsLocal(relative) {
			t.Fatal("native dependency escaped repository")
		}
		relative = filepath.ToSlash(relative)
		if seen[relative] {
			continue // One file may also be selected by go:embed.
		}
		seen[relative] = true
		if !owned(relative) {
			t.Error("unbound native dependency", relative)
		}
		directory := filepath.ToSlash(filepath.Dir(relative))
		if !protected(directory) {
			t.Error("dependency directory can hide behind a symlink", directory)
		}
	}
	if len(seen) == 0 {
		t.Fatal("native dependency discovery was empty")
	}
}
