package godj

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0043PhaseAContractsRemainUnregisteredAndOracleLocked(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifestNames := []string{
		"template-form-manifest.json",
		"auth-session-manifest.json",
		"article-admin-manifest.json",
	}
	for _, name := range manifestNames {
		manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", name))
		if err != nil {
			t.Fatal(err)
		}
		required, err := RequiredObservedContractIDs(manifest)
		if err != nil {
			t.Fatalf("%s registry: %v", name, err)
		}
		if len(required) != 0 {
			t.Fatalf("%s required observed IDs = %#v, want none during Phase A", name, required)
		}
		mutated := manifest
		mutated.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
		mutated.Contracts[0].Status = protocol.ContractPassing
		if _, err := RequiredObservedContractIDs(mutated); err == nil || !strings.Contains(err.Error(), "unregistered scenario") {
			t.Fatalf("%s false-green registry error = %v", name, err)
		}
	}

	templateManifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "template-form-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	web027 := templateManifest.Contracts[6]
	if web027.ID != "WEB-027" || web027.Status != protocol.ContractOracleLocked {
		t.Fatalf("WEB-027 phase-A registration boundary = %#v", web027)
	}
}
