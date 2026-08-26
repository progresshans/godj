package attestation

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestComputeSourceBindingUsesExactSortedFrames(t *testing.T) {
	repositoryRoot := t.TempDir()
	runtimeContents := []byte("package systemstate\n")
	queryContents := []byte("package query\n")
	// Write reverse lexical order so the expected digest also locks sorting.
	writeTestFile(t, filepath.Join(repositoryRoot, "systemstate", "runtime.go"), runtimeContents, 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "query", "plan.go"), queryContents, 0o644)

	binding, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	queryDigest := sha256.Sum256(queryContents)
	runtimeDigest := sha256.Sum256(runtimeContents)
	frames := "query/plan.go\x00100644\x00" + strconv.Itoa(len(queryContents)) + "\x00" + hex.EncodeToString(queryDigest[:]) + "\n" +
		"systemstate/runtime.go\x00100644\x00" + strconv.Itoa(len(runtimeContents)) + "\x00" + hex.EncodeToString(runtimeDigest[:]) + "\n"
	inventoryDigest := sha256.Sum256([]byte(frames))
	payloadBytes := len(queryContents) + len(runtimeContents)
	if binding.FileCount() != 2 || binding.PayloadBytes() != int64(payloadBytes) || binding.SHA256() != hex.EncodeToString(inventoryDigest[:]) {
		t.Fatalf("source binding = %d/%d/%s, want 2/%d/%s", binding.FileCount(), binding.PayloadBytes(), binding.SHA256(), payloadBytes, hex.EncodeToString(inventoryDigest[:]))
	}
}

func TestComputeSourceBindingStalesOnOwnedAddRemoveMutationAndMode(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	baseline, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute baseline source binding: %v", err)
	}

	addedPath := filepath.Join(repositoryRoot, "systemstate", "added.go")
	writeTestFile(t, addedPath, []byte("package systemstate\n"), 0o644)
	assertDifferentSourceBinding(t, repositoryRoot, baseline, "owned add")
	if err := os.Remove(addedPath); err != nil {
		t.Fatalf("remove added source: %v", err)
	}
	restored, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute restored source binding: %v", err)
	}
	if !baseline.Equal(restored) {
		t.Fatalf("binding after add/remove = %d/%d/%s, want baseline %d/%d/%s", restored.FileCount(), restored.PayloadBytes(), restored.SHA256(), baseline.FileCount(), baseline.PayloadBytes(), baseline.SHA256())
	}
	removedPath := filepath.Join(repositoryRoot, "query", "plan.go")
	if err := os.Remove(removedPath); err != nil {
		t.Fatalf("remove owned source: %v", err)
	}
	assertDifferentSourceBinding(t, repositoryRoot, baseline, "owned remove")
	writeTestFile(t, removedPath, []byte("package query\n"), 0o644)
	embeddedAsset := filepath.Join(repositoryRoot, "admin", "site_templates", "added.html")
	writeTestFile(t, embeddedAsset, []byte("added template\n"), 0o644)
	assertDifferentSourceBinding(t, repositoryRoot, baseline, "embedded asset add")
	if err := os.Remove(embeddedAsset); err != nil {
		t.Fatalf("remove added embedded asset: %v", err)
	}

	runtimePath := filepath.Join(repositoryRoot, "systemstate", "runtime.go")
	writeTestFile(t, runtimePath, []byte("package systemstate\n\nconst changed = true\n"), 0o644)
	assertDifferentSourceBinding(t, repositoryRoot, baseline, "owned mutation")
	writeTestFile(t, runtimePath, []byte("package systemstate\n"), 0o755)
	assertDifferentSourceBinding(t, repositoryRoot, baseline, "owned mode change")
}

func TestComputeSourceBindingStalesOnMigrationQueryAndSchemaMutation(t *testing.T) {
	paths := []string{
		"migrations/executor.go",
		"query/plan.go",
		"schema/ir/types.go",
		"systemstate/testdata/0001_initial.godj.json",
	}
	for _, relative := range paths {
		t.Run(relative, func(t *testing.T) {
			repositoryRoot := seedSourceRepository(t)
			baseline, err := ComputeSourceBinding(repositoryRoot)
			if err != nil {
				t.Fatalf("compute baseline source binding: %v", err)
			}
			writeTestFile(t, filepath.Join(repositoryRoot, filepath.FromSlash(relative)), []byte("package changed\n"), 0o644)
			assertDifferentSourceBinding(t, repositoryRoot, baseline, relative+" mutation")
		})
	}
}

func TestComputeSourceBindingExcludesDocsCheckedEvidenceOraclesAndFixtures(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	baseline, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute baseline source binding: %v", err)
	}
	mutations := map[string][]byte{
		"docs/status/TEST_EVIDENCE.md": []byte("changed evidence\n"),
		"work/0046.md":                 []byte("changed work\n"),
		"conformance/systemstate/attestations/postgresql-17.10-two-process-v1.json": []byte("changed checked evidence\n"),
		"conformance/oracles/profile/system-state.json":                             []byte("changed oracle\n"),
		"conformance/fixtures/godj-system-state-not-implemented.json":               []byte("changed fixture\n"),
		"systemstate/runtime_test.go":                                               []byte("package systemstate\n"),
	}
	for relative, contents := range mutations {
		writeTestFile(t, filepath.Join(repositoryRoot, filepath.FromSlash(relative)), contents, 0o644)
		current, err := ComputeSourceBinding(repositoryRoot)
		if err != nil {
			t.Fatalf("compute source binding after excluded mutation %s: %v", relative, err)
		}
		if !baseline.Equal(current) {
			t.Fatalf("excluded mutation %s changed binding from %s to %s", relative, baseline.SHA256(), current.SHA256())
		}
	}
}

func TestSourceScopeIncludesLiveRestartTestsButExcludesOrdinaryTests(t *testing.T) {
	tests := map[string]bool{
		"conformance/systemstate/restart/restart_unix_test.go": true,
		"conformance/systemstate/product/product_test.go":      false,
		"systemstate/runtime_test.go":                          false,
		"conformance/systemstate/worker/worker.go":             true,
		"conformance/systemstate/attestation/codec.go":         true,
		"migrations/executor.go":                               true,
		"query/plan.go":                                        true,
		"schema/ir/types.go":                                   true,
		"systemstate/testdata/0001_initial.godj.json":          true,
		"admin/site_templates/extra.html":                      true,
		"conformance/systemstate/attestations/evidence.go":     false,
		"conformance/oracles/profile/oracle.go":                false,
		"conformance/fixtures/fixture.go":                      false,
	}
	for path, want := range tests {
		if got := sourcePathOwned(path); got != want {
			t.Errorf("sourcePathOwned(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestComputeSourceBindingRejectsSelectedSymlink(t *testing.T) {
	repositoryRoot := seedSourceRepository(t)
	target := filepath.Join(repositoryRoot, "outside.go")
	writeTestFile(t, target, []byte("package outside\n"), 0o644)
	link := filepath.Join(repositoryRoot, "systemstate", "linked.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if _, err := ComputeSourceBinding(repositoryRoot); err == nil {
		t.Fatal("ComputeSourceBinding accepted a selected symbolic link")
	}
}

func TestComputeSourceBindingRejectsDirectorySymlinkThatCanHideOwnedSource(t *testing.T) {
	tests := []struct {
		name     string
		linkPath string
		filePath string
	}{
		{
			name:     "product prefix root",
			linkPath: "systemstate",
			filePath: "runtime.go",
		},
		{
			name:     "conformance prefix descendant",
			linkPath: "conformance/systemstate/multiruntimeworker",
			filePath: "worker.go",
		},
		{
			name:     "exact path ancestor",
			linkPath: ".github/workflows",
			filePath: "ci.yml",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositoryRoot := t.TempDir()
			target := t.TempDir()
			writeTestFile(t, filepath.Join(target, filepath.FromSlash(test.filePath)), []byte("owned source\n"), 0o644)
			link := filepath.Join(repositoryRoot, filepath.FromSlash(test.linkPath))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatalf("create selected directory symlink parent: %v", err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symbolic links are unavailable: %v", err)
			}
			if _, err := ComputeSourceBinding(repositoryRoot); err == nil {
				t.Fatal("ComputeSourceBinding accepted a directory symlink that omitted owned source")
			}
		})
	}
}

func seedSourceRepository(t *testing.T) string {
	t.Helper()
	repositoryRoot := t.TempDir()
	writeTestFile(t, filepath.Join(repositoryRoot, "systemstate", "runtime.go"), []byte("package systemstate\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "migrations", "executor.go"), []byte("package migrations\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "query", "plan.go"), []byte("package query\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "schema", "ir", "types.go"), []byte("package ir\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "systemstate", "testdata", "0001_initial.godj.json"), []byte("{}\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "conformance", "systemstate", "restart", "restart_unix_test.go"), []byte("package restart\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "docs", "status", "TEST_EVIDENCE.md"), []byte("evidence\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "conformance", "systemstate", "attestations", FileName), []byte("checked evidence\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "conformance", "oracles", "profile", "system-state.json"), []byte("oracle\n"), 0o644)
	writeTestFile(t, filepath.Join(repositoryRoot, "conformance", "fixtures", "fixture.json"), []byte("fixture\n"), 0o644)
	return repositoryRoot
}

func assertDifferentSourceBinding(t *testing.T, repositoryRoot string, baseline SourceBinding, operation string) {
	t.Helper()
	current, err := ComputeSourceBinding(repositoryRoot)
	if err != nil {
		t.Fatalf("compute source binding after %s: %v", operation, err)
	}
	if baseline.Equal(current) {
		t.Fatalf("%s did not stale source binding %s", operation, baseline.SHA256())
	}
}
