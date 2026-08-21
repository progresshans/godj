//go:build darwin || linux

package compiletest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
)

// TestProjectBundleCandidateExternalSurface keeps the orchestration contract
// honest from a package outside projectgenerate: a virtual new package must
// compile before any generated target exists, then the exact committed bytes
// must pass the read-only check.
func TestProjectBundleCandidateExternalSurface(t *testing.T) {
	bundle, err := codegen.GenerateProject(codegen.ProjectSpec{Project: codegen.PackageSpec{
		PackageName: "project",
		ImportPath:  "example.com/godj-project-bundle/project",
		Directory:   "project",
	}})
	if err != nil {
		t.Fatalf("GenerateProject() error = %v", err)
	}
	root := t.TempDir()
	stage := t.TempDir()
	goModTemplate, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "compiletest", "testdata", "project_bundle", "go.mod.txt"))
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "compiletest", "testdata", "project_bundle", "consumer.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeProjectBundleCompileFile(t, root, "go.mod", []byte(fmt.Sprintf(string(goModTemplate), filepath.ToSlash(repositoryRoot(t)))))
	writeProjectBundleCompileFile(t, root, "consumer/consumer.go", consumer)
	writeProjectBundleCompileBundle(t, stage, bundle)

	verifier, err := projectgenerate.NewGoCandidateVerifier(root, bundle)
	if err != nil {
		t.Fatalf("NewGoCandidateVerifier() error = %v", err)
	}
	if err := verifier.Verify(context.Background(), stage); err != nil {
		t.Fatalf("Verify(virtual bundle) error = %v", err)
	}
	for _, file := range bundle.Files() {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); !os.IsNotExist(err) {
			t.Fatalf("Verify published target %q", file.Path)
		}
	}

	writeProjectBundleCompileBundle(t, root, bundle)
	report, err := projectgenerate.Check(context.Background(), root, bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("Check(committed bundle) report=%#v error=%v", report, err)
	}
}

func writeProjectBundleCompileBundle(t *testing.T, root string, bundle codegen.GeneratedBundle) {
	t.Helper()
	for _, file := range bundle.Files() {
		writeProjectBundleCompileFile(t, root, file.Path, file.Source())
	}
	writeProjectBundleCompileFile(t, root, codegen.GeneratedManifestPath, bundle.Manifest())
}

func writeProjectBundleCompileFile(t *testing.T, root, relative string, contents []byte) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}
