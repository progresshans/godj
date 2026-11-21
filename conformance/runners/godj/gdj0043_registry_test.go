package godj

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0043ProductContractsAreRegisteredAndReviewed(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	sets := []struct {
		manifestName string
		deviationIDs []string
		decision     string
	}{
		{manifestName: "template-form-manifest.json", deviationIDs: []string{"WEB-022", "WEB-027"}, decision: "DEV-0003"},
		{manifestName: "auth-session-manifest.json", deviationIDs: []string{"AUT-004", "AUT-005"}, decision: "DEV-0004"},
		{manifestName: "article-admin-manifest.json", deviationIDs: []string{"ADM-002"}, decision: "DEV-0005"},
	}
	for _, set := range sets {
		set := set
		t.Run(set.manifestName, func(t *testing.T) {
			t.Parallel()

			manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifestName))
			if err != nil {
				t.Fatal(err)
			}
			required, err := RequiredObservedContractIDs(manifest)
			if err != nil {
				t.Fatalf("registry: %v", err)
			}
			if len(required) != len(manifest.Contracts) {
				t.Fatalf("required observed IDs = %d, want %d", len(required), len(manifest.Contracts))
			}
			deviations := 0
			for index, contract := range manifest.Contracts {
				if required[index] != contract.ID {
					t.Fatalf("required observed ID %d = %q, want %q", index, required[index], contract.ID)
				}
				if contract.Status != protocol.ContractDeviation {
					continue
				}
				deviations++
				if !stringSliceContains(set.deviationIDs, contract.ID) || decisionProvenanceCount(contract, set.decision) != 1 {
					t.Fatalf("deviation contract = %#v, want one of %#v under %s", contract, set.deviationIDs, set.decision)
				}
			}
			if deviations != len(set.deviationIDs) {
				t.Fatalf("deviation count = %d, want %d", deviations, len(set.deviationIDs))
			}

			mutated := manifest
			mutated.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
			mutated.Contracts[0].Status = protocol.ContractOracleLocked
			if _, err := RequiredObservedContractIDs(mutated); err == nil || !strings.Contains(err.Error(), "registered scenario") {
				t.Fatalf("registered false-negative error = %v", err)
			}
		})
	}
}

func stringSliceContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestGDJ0043UnknownProductScenarioRemainsFailClosed(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "template-form-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
	manifest.Contracts[0].Scenario = "django.template_form.unregistered_sentinel"
	if _, err := RequiredObservedContractIDs(manifest); err == nil || !strings.Contains(err.Error(), "unregistered scenario") {
		t.Fatalf("unregistered false-green error = %v", err)
	}
}

func decisionProvenanceCount(contract protocol.Contract, decision string) int {
	count := 0
	for _, provenance := range contract.Provenance {
		if provenance.Kind == "decision" && provenance.Reference == decision {
			count++
		}
	}
	return count
}
