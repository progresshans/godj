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
	repository := t.TempDir()
	projectContents := []byte("package project\n")
	harnessContents := []byte("package projectoperatorproduct_test\n")
	// Write reverse lexical order so the expected digest locks sorting too.
	writeTestFile(t, filepath.Join(repository, "project", "project_unix.go"), projectContents, 0o644)
	writeTestFile(t, filepath.Join(repository, "conformance", "projectoperatorproduct", "product_unix_test.go"), harnessContents, 0o644)

	binding, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatalf("compute source binding: %v", err)
	}
	harnessDigest := sha256.Sum256(harnessContents)
	projectDigest := sha256.Sum256(projectContents)
	frames := "conformance/projectoperatorproduct/product_unix_test.go\x00100644\x00" + strconv.Itoa(len(harnessContents)) + "\x00" + hex.EncodeToString(harnessDigest[:]) + "\n" +
		"project/project_unix.go\x00100644\x00" + strconv.Itoa(len(projectContents)) + "\x00" + hex.EncodeToString(projectDigest[:]) + "\n"
	inventoryDigest := sha256.Sum256([]byte(frames))
	wantBytes := int64(len(harnessContents) + len(projectContents))
	if binding.FileCount() != 2 || binding.PayloadBytes() != wantBytes || binding.SHA256() != hex.EncodeToString(inventoryDigest[:]) {
		t.Fatalf("source binding = %d/%d/%s, want 2/%d/%s", binding.FileCount(), binding.PayloadBytes(), binding.SHA256(), wantBytes, hex.EncodeToString(inventoryDigest[:]))
	}
}

func TestComputeSourceBindingStalesOnRequiredFamilyMutation(t *testing.T) {
	paths := []string{
		".github/workflows/ci.yml",
		"Makefile",
		"go.mod",
		"go.sum",
		"cmd/godj/main_unix.go",
		"internal/projectcheck/createsuperuser_run_unix.go",
		"internal/projectgenerate/candidate.go",
		"project/project_unix.go",
		"systemstate/runtime.go",
		"systemstate/testdata/0001_initial.godj.json",
		"examples/article/modeldef/schema.go",
		"examples/article/migrations/0001_initial.godj.json",
		"examples/article/webapp/templates/article_list.html",
		"conformance/projectoperatorproduct/product_unix_test.go",
		"conformance/projectoperatorproduct/schema_snapshot_unix_test.go",
		"conformance/projectoperatorproduct/secret_scan_unix_test.go",
		"conformance/projectoperatorproduct/attestation/codec.go",
		"conformance/runners/godj/gdj0055_operator_backend_scenario.go",
		"conformance/runners/godj/gdj0045_system_state_scenarios.go",
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios.go",
		"conformance/runners/godj/gdj0046_system_state_two_process_execution.go",
		"conformance/runners/godj/inputs.go",
		"conformance/runners/godj/runner.go",
		"conformance/cmd/godjcheck/main.go",
		"conformance/internal/protocol/protocol.go",
		"conformance/contracts/system-state-manifest.json",
	}
	for _, relative := range paths {
		t.Run(relative, func(t *testing.T) {
			repository := seedSourceRepository(t)
			baseline, err := ComputeSourceBinding(repository)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(repository, filepath.FromSlash(relative)), []byte("changed behavioral source\n"), 0o644)
			assertDifferentSourceBinding(t, repository, baseline, "mutation of "+relative)
		})
	}
}

func TestComputeSourceBindingStalesOnOwnedAddRemoveAndMode(t *testing.T) {
	repository := seedSourceRepository(t)
	baseline, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(repository, "conformance", "projectoperatorproduct", "added_unix_test.go")
	writeTestFile(t, added, []byte("package projectoperatorproduct_test\n"), 0o644)
	assertDifferentSourceBinding(t, repository, baseline, "owned add")
	if err := os.Remove(added); err != nil {
		t.Fatal(err)
	}
	restored, err := ComputeSourceBinding(repository)
	if err != nil || !restored.Equal(baseline) {
		t.Fatalf("source binding after add/remove = %v %d/%d/%s", err, restored.FileCount(), restored.PayloadBytes(), restored.SHA256())
	}

	removed := filepath.Join(repository, "internal", "projectcheck", "createsuperuser_run_unix.go")
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	assertDifferentSourceBinding(t, repository, baseline, "owned remove")
	writeTestFile(t, removed, []byte("package projectcheck\n"), 0o644)

	harness := filepath.Join(repository, "conformance", "projectoperatorproduct", "product_unix_test.go")
	writeTestFile(t, harness, []byte("package projectoperatorproduct_test\n"), 0o755)
	assertDifferentSourceBinding(t, repository, baseline, "owned mode change")
}

func TestComputeSourceBindingExcludesDocsCheckedEvidenceFixturesAndOrdinaryTests(t *testing.T) {
	repository := seedSourceRepository(t)
	baseline, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string][]byte{
		"docs/status/TEST_EVIDENCE.md": []byte("changed evidence\n"),
		"work/0055.md":                 []byte("changed work\n"),
		"conformance/projectoperatorproduct/attestations/postgresql-17.10-sqlite-external-operator-v1.json": []byte("checked evidence\n"),
		"conformance/projectoperatorproduct/attestation/checked_size_test.go":                               []byte("package attestation\n"),
		"conformance/oracles/profile/system-state.json":                                                     []byte("oracle\n"),
		"conformance/fixtures/godj-system-state-not-implemented.json":                                       []byte("fixture\n"),
		"systemstate/runtime_test.go":                                                                       []byte("package systemstate\n"),
		"cmd/godj/main_test.go":                                                                             []byte("package main\n"),
		"examples/article/http_e2e_test.go":                                                                 []byte("package article_test\n"),
		"conformance/runners/godj/unrelated_test.go":                                                        []byte("package godj\n"),
	}
	for relative, contents := range mutations {
		writeTestFile(t, filepath.Join(repository, filepath.FromSlash(relative)), contents, 0o644)
		current, err := ComputeSourceBinding(repository)
		if err != nil {
			t.Fatalf("compute after excluded mutation %s: %v", relative, err)
		}
		if !baseline.Equal(current) {
			t.Fatalf("excluded mutation %s changed source binding", relative)
		}
	}
}

func TestSourceScopeOwnsSYS029ProducerConsumerAndPolicyPaths(t *testing.T) {
	tests := map[string]bool{
		".github/workflows/ci.yml": true,
		"Makefile":                 true,
		"go.mod":                   true,
		"go.sum":                   true,
		"cmd/godj/main_unix.go":    true,
		"cmd/godj/main_test.go":    false,
		"internal/projectcheck/createsuperuser_run_unix.go":                        true,
		"internal/projectcheck/createsuperuser_run_unix_test.go":                   false,
		"internal/projectgenerate/candidate.go":                                    true,
		"project/project_unix.go":                                                  true,
		"systemstate/runtime.go":                                                   true,
		"systemstate/runtime_test.go":                                              false,
		"systemstate/testdata/0001_initial.godj.json":                              true,
		"examples/article/modeldef/schema.go":                                      true,
		"examples/article/http_e2e_test.go":                                        false,
		"examples/article/migrations/0001_initial.godj.json":                       true,
		"examples/article/webapp/templates/article_list.html":                      true,
		"conformance/projectoperatorproduct/product_unix_test.go":                  true,
		"conformance/projectoperatorproduct/schema_snapshot_unix_test.go":          true,
		"conformance/projectoperatorproduct/secret_scan_unix_test.go":              true,
		"conformance/projectoperatorproduct/attestation/source_test.go":            false,
		"conformance/projectoperatorproduct/attestations/evidence.go":              false,
		"conformance/runners/godj/gdj0055_operator_scenarios.go":                   true,
		"conformance/runners/godj/gdj0055_operator_scenarios_test.go":              true,
		"conformance/runners/godj/gdj0045_system_state_scenarios.go":               true,
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios.go": true,
		"conformance/runners/godj/gdj0046_system_state_two_process_execution.go":   true,
		"conformance/runners/godj/system_state_unrelated.go":                       false,
		"conformance/cmd/godjcheck/main.go":                                        true,
		"conformance/cmd/godjcheck/main_test.go":                                   false,
		"conformance/internal/protocol/protocol.go":                                true,
		"conformance/internal/protocol/protocol_test.go":                           false,
		"conformance/runners/godj/inputs.go":                                       true,
		"conformance/runners/godj/runner.go":                                       true,
		"conformance/contracts/system-state-manifest.json":                         true,
		"conformance/oracles/profile/system-state.json":                            false,
		"docs/status/CURRENT.md":                                                   false,
	}
	for path, want := range tests {
		if got := sourcePathOwned(path); got != want {
			t.Errorf("sourcePathOwned(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestComputeSourceBindingRejectsSelectedFileSymlink(t *testing.T) {
	repository := seedSourceRepository(t)
	target := filepath.Join(repository, "outside.go")
	writeTestFile(t, target, []byte("package outside\n"), 0o644)
	link := filepath.Join(repository, "systemstate", "linked.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := ComputeSourceBinding(repository); err == nil {
		t.Fatal("ComputeSourceBinding accepted selected file symlink")
	}
}

func TestComputeSourceBindingRejectsDirectorySymlinkThatCanHideOwnedSource(t *testing.T) {
	tests := []struct {
		name     string
		linkPath string
		filePath string
	}{
		{name: "product", linkPath: "systemstate", filePath: "runtime.go"},
		{name: "producer harness", linkPath: "conformance/projectoperatorproduct", filePath: "product_unix_test.go"},
		{name: "consumer handler", linkPath: "conformance/runners/godj", filePath: "gdj0055_operator_scenarios.go"},
		{name: "loader", linkPath: "conformance/cmd/godjcheck", filePath: "main.go"},
		{name: "workflow", linkPath: ".github/workflows", filePath: "ci.yml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			target := t.TempDir()
			writeTestFile(t, filepath.Join(target, filepath.FromSlash(test.filePath)), []byte("owned source\n"), 0o644)
			link := filepath.Join(repository, filepath.FromSlash(test.linkPath))
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
			if _, err := ComputeSourceBinding(repository); err == nil {
				t.Fatal("ComputeSourceBinding accepted directory symlink hiding owned source")
			}
		})
	}
}

func seedSourceRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	files := map[string][]byte{
		".github/workflows/ci.yml": []byte("name: CI\n"),
		"Makefile":                 []byte("test:\n\tgo test ./...\n"),
		"go.mod":                   []byte("module example.com/source\n"),
		"go.sum":                   []byte("dependency checksum\n"),
		"cmd/godj/main_unix.go":    []byte("package main\n"),
		"internal/projectcheck/createsuperuser_run_unix.go":                        []byte("package projectcheck\n"),
		"internal/projectgenerate/candidate.go":                                    []byte("package projectgenerate\n"),
		"project/project_unix.go":                                                  []byte("package project\n"),
		"systemstate/runtime.go":                                                   []byte("package systemstate\n"),
		"systemstate/testdata/0001_initial.godj.json":                              []byte("{}\n"),
		"examples/article/godj.toml":                                               []byte("format_version = 1\n"),
		"examples/article/modeldef/schema.go":                                      []byte("package modeldef\n"),
		"examples/article/migrations/0001_initial.godj.json":                       []byte("{}\n"),
		"examples/article/webapp/templates/article_list.html":                      []byte("article\n"),
		"admin/site_templates/login.html":                                          []byte("login\n"),
		"conformance/projectoperatorproduct/product_unix_test.go":                  []byte("package projectoperatorproduct_test\n"),
		"conformance/projectoperatorproduct/attestation/codec.go":                  []byte("package attestation\n"),
		"conformance/runners/godj/gdj0055_operator_backend_scenario.go":            []byte("package godj\n"),
		"conformance/runners/godj/gdj0045_system_state_scenarios.go":               []byte("package godj\n"),
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios.go": []byte("package godj\n"),
		"conformance/runners/godj/gdj0046_system_state_two_process_execution.go":   []byte("package godj\n"),
		"conformance/runners/godj/inputs.go":                                       []byte("package godj\n"),
		"conformance/runners/godj/runner.go":                                       []byte("package godj\n"),
		"conformance/cmd/godjcheck/main.go":                                        []byte("package main\n"),
		"conformance/internal/protocol/protocol.go":                                []byte("package protocol\n"),
		"conformance/contracts/system-state-manifest.json":                         []byte("{}\n"),
	}
	for relative, contents := range files {
		writeTestFile(t, filepath.Join(repository, filepath.FromSlash(relative)), contents, 0o644)
	}
	return repository
}

func assertDifferentSourceBinding(t *testing.T, repository string, baseline SourceBinding, operation string) {
	t.Helper()
	current, err := ComputeSourceBinding(repository)
	if err != nil {
		t.Fatalf("compute source binding after %s: %v", operation, err)
	}
	if baseline.Equal(current) {
		t.Fatalf("%s did not stale source binding %s", operation, baseline.SHA256())
	}
}
