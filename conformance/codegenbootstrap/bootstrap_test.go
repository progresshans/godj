package codegenbootstrap_test

import (
	"bytes"
	"crypto/sha256"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	generatedFile = "models/zz_godj_generated.go"
	schemaFile    = "modeldef/schema.go"
	methodsFile   = "models/model_methods.go"
)

func TestBootstrapRenameWithBrokenTarget(t *testing.T) {
	project := newFixtureProject(t)

	runGo(t, project, true, "test", "./models")
	installPhaseFile(t, project, "rename/schema.go.txt", schemaFile)
	installPhaseFile(t, project, "rename/model_methods.go.txt", methodsFile)

	broken := runGo(t, project, false, "test", "./models")
	assertContains(t, broken, "undefined: Article")

	runGo(t, project, true, "run", "./cmd/spikegen")
	generated := readProjectFile(t, project, generatedFile)
	assertContains(t, string(generated), "type Article struct")
	assertContains(t, string(generated), "Headline string")
	assertNotContains(t, string(generated), "type Post struct")
	runGo(t, project, true, "test", "./models")
}

func TestBootstrapDeletePreservesLastGoodOutput(t *testing.T) {
	project := newFixtureProject(t)
	establishRenamedProject(t, project)

	before := readProjectFile(t, project, generatedFile)
	beforeHash := sha256.Sum256(before)
	installPhaseFile(t, project, "delete/schema.go.txt", schemaFile)

	// The stale generated field hides the user-method problem before generation.
	runGo(t, project, true, "test", "./models")
	failed := runGo(t, project, false, "run", "./cmd/spikegen")
	assertContains(t, failed, "candidate package validation failed")
	assertContains(t, failed, "Headline undefined")

	after := readProjectFile(t, project, generatedFile)
	afterHash := sha256.Sum256(after)
	if !bytes.Equal(before, after) || beforeHash != afterHash {
		t.Fatalf("failed generation changed last-good output: before=%x after=%x", beforeHash, afterHash)
	}

	// Preservation is observable: the old package still compiles after failure.
	runGo(t, project, true, "test", "./models")
}

func TestBootstrapRepairAndIdempotency(t *testing.T) {
	project := newFixtureProject(t)
	establishRenamedProject(t, project)
	installPhaseFile(t, project, "delete/schema.go.txt", schemaFile)
	installPhaseFile(t, project, "repair/model_methods.go.txt", methodsFile)

	runGo(t, project, true, "run", "./cmd/spikegen")
	first := readProjectFile(t, project, generatedFile)
	assertContains(t, string(first), "type Article struct")
	assertNotContains(t, string(first), "Headline")
	runGo(t, project, true, "test", "./models")

	runGo(t, project, true, "run", "./cmd/spikegen")
	second := readProjectFile(t, project, generatedFile)
	if !bytes.Equal(first, second) {
		t.Fatalf("same schema generated different bytes\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestBootstrapMalformedSchemaAndOutputPreserveLastGoodOutput(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		wantError string
	}{
		{
			name:      "invalid schema",
			phase:     "malformed/invalid_schema.go.txt",
			wantError: "model name is not a Go identifier",
		},
		{
			name:      "unformattable output",
			phase:     "malformed/unformattable_output.go.txt",
			wantError: "format generated output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := newFixtureProject(t)
			before := readProjectFile(t, project, generatedFile)
			installPhaseFile(t, project, tt.phase, schemaFile)

			failed := runGo(t, project, false, "run", "./cmd/spikegen")
			assertContains(t, failed, tt.wantError)
			after := readProjectFile(t, project, generatedFile)
			if !bytes.Equal(before, after) {
				t.Fatalf("failed generation changed last-good output\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestGeneratorBuildGraphExcludesGeneratedTarget(t *testing.T) {
	project := newFixtureProject(t)
	installPhaseFile(t, project, "rename/schema.go.txt", schemaFile)
	installPhaseFile(t, project, "rename/model_methods.go.txt", methodsFile)

	// Prove the target really is broken before examining the generator graph.
	runGo(t, project, false, "test", "./models")
	deps := runGo(t, project, true, "list", "-deps", "./cmd/spikegen")

	lines := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(deps), "\n") {
		lines[strings.TrimSpace(line)] = true
	}
	for _, required := range []string{
		"example.com/codegenbootstrapfixture/modeldef",
		"example.com/codegenbootstrapfixture/spikecodegen",
	} {
		if !lines[required] {
			t.Fatalf("generator dependency graph is missing %q\n%s", required, deps)
		}
	}
	if lines["example.com/codegenbootstrapfixture/models"] {
		t.Fatalf("generator dependency graph unexpectedly contains generated target package\n%s", deps)
	}
}

func establishRenamedProject(t *testing.T, project string) {
	t.Helper()
	installPhaseFile(t, project, "rename/schema.go.txt", schemaFile)
	installPhaseFile(t, project, "rename/model_methods.go.txt", methodsFile)
	runGo(t, project, true, "run", "./cmd/spikegen")
	runGo(t, project, true, "test", "./models")
}

func newFixtureProject(t *testing.T) string {
	t.Helper()
	source := filepath.Join("testdata", "project")
	destination := filepath.Join(t.TempDir(), "project")

	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture project: %v", err)
	}
	return destination
}

func installPhaseFile(t *testing.T, project, phaseFile, targetFile string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "phases", phaseFile))
	if err != nil {
		t.Fatalf("read phase file %s: %v", phaseFile, err)
	}
	target := filepath.Join(project, filepath.FromSlash(targetFile))
	if err := os.WriteFile(target, contents, 0o644); err != nil {
		t.Fatalf("install phase file %s as %s: %v", phaseFile, targetFile, err)
	}
}

func readProjectFile(t *testing.T, project, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(project, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read project file %s: %v", name, err)
	}
	return contents
}

func runGo(t *testing.T, directory string, wantSuccess bool, args ...string) string {
	t.Helper()
	command := exec.Command("go", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	text := string(output)
	if wantSuccess && err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, text)
	}
	if !wantSuccess && err == nil {
		t.Fatalf("go %s unexpectedly succeeded\n%s", strings.Join(args, " "), text)
	}
	return text
}

func assertContains(t *testing.T, value, substring string) {
	t.Helper()
	if !strings.Contains(value, substring) {
		t.Fatalf("expected output to contain %q\n%s", substring, value)
	}
}

func assertNotContains(t *testing.T, value, substring string) {
	t.Helper()
	if strings.Contains(value, substring) {
		t.Fatalf("expected output not to contain %q\n%s", substring, value)
	}
}
