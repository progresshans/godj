package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	systemstateattestation "github.com/progresshans/godj/conformance/systemstate/attestation"
)

func TestLoadRunnerInputsRequiresEvidenceOnlyForPublishedSYS020(t *testing.T) {
	t.Parallel()

	locked := gdj0046AttestationManifest(protocol.ContractOracleLocked)
	inputs, err := loadRunnerInputs(locked, "", "", "")
	if err != nil || inputs.SystemStatePostgreSQLTwoProcess != nil {
		t.Fatalf("locked SYS-020 inputs = (%#v, %v), want zero/nil", inputs, err)
	}
	if _, err := loadRunnerInputs(locked, "", "unused.json", ""); err == nil || !strings.Contains(err.Error(), "not used") {
		t.Fatalf("locked SYS-020 explicit evidence error = %v", err)
	}

	for _, status := range []protocol.ContractStatus{protocol.ContractPassing, protocol.ContractDeviation} {
		published := gdj0046AttestationManifest(status)
		if _, err := loadRunnerInputs(published, "", "", ""); err == nil || !strings.Contains(err.Error(), "SYS-020 requires") {
			t.Fatalf("%s SYS-020 missing evidence error = %v", status, err)
		}
	}
}

func TestLoadRunnerInputsRejectsInconsistentSYS020Binding(t *testing.T) {
	t.Parallel()

	manifest := gdj0046AttestationManifest(protocol.ContractPassing)
	manifest.Contracts[0].Scenario = "godj.system_state.not_sys020"
	if _, err := loadRunnerInputs(manifest, "", "evidence.json", ""); err == nil || !strings.Contains(err.Error(), "binding is inconsistent") {
		t.Fatalf("inconsistent SYS-020 error = %v", err)
	}

	manifest = gdj0046AttestationManifest(protocol.ContractPassing)
	manifest.Contracts[0].ID = "SYS-999"
	if _, err := loadRunnerInputs(manifest, "", "evidence.json", ""); err == nil || !strings.Contains(err.Error(), "binding is inconsistent") {
		t.Fatalf("inconsistent scenario error = %v", err)
	}
}

func TestAttestationRepositoryRootRequiresExactCheckedPath(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	checked := filepath.Join(
		root,
		"conformance",
		"systemstate",
		"attestations",
		systemstateattestation.FileName,
	)
	manifest := filepath.Join(root, "conformance", "contracts", "system-state-manifest.json")
	got, err := attestationRepositoryRoot(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireSystemStateAttestationPath(got, checked); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("attestation root = %q, want %q", got, want)
	}

	wrong := filepath.Join(root, "conformance", "systemstate", systemstateattestation.FileName)
	if err := requireSystemStateAttestationPath(got, wrong); err == nil || !strings.Contains(err.Error(), "checked current repository path") {
		t.Fatalf("wrong checked path error = %v", err)
	}

	wrongManifest := filepath.Join(root, "conformance", "contracts", "manifest.json")
	if _, err := attestationRepositoryRoot(wrongManifest); err == nil || !strings.Contains(err.Error(), "system-state manifest") {
		t.Fatalf("wrong system-state manifest path error = %v", err)
	}
}

func TestRequireExactResolvedPathRejectsParentDirectorySymlink(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "missing", "evidence.json")
	if err := requireExactResolvedPath(missing, missing); err != nil {
		t.Fatalf("missing exact checked leaf error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "evidence.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "attestations")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	expected := filepath.Join(link, "evidence.json")
	if err := requireExactResolvedPath(expected, expected); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("parent-directory symlink error = %v", err)
	}
}

func gdj0046AttestationManifest(status protocol.ContractStatus) protocol.Manifest {
	return protocol.Manifest{Contracts: []protocol.Contract{{
		ID:       systemstateattestation.Contract,
		Scenario: systemstateattestation.Scenario,
		Status:   status,
	}}}
}
