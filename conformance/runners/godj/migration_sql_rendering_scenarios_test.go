package godj

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0054MigrationSQLRenderingRemainsUnregisteredBeforeProductPublication(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(
		root,
		"conformance",
		"profiles",
		"django-6.1-sqlite-darwin-arm64.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(
		root,
		"conformance",
		"contracts",
		"migration-sql-rendering-manifest.json",
	))
	if err != nil {
		t.Fatal(err)
	}

	required, err := RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 0 {
		t.Fatalf("locked migration SQL required observed IDs = %v, want empty", required)
	}

	suite, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(suite.Contracts) != len(manifest.Contracts) {
		t.Fatalf("migration SQL observations = %d, want %d", len(suite.Contracts), len(manifest.Contracts))
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", 129+index)
		if contract.ID != wantID || contract.Status != protocol.ContractOracleLocked {
			t.Fatalf("migration SQL contract %d = %#v, want %s/oracle_locked", index, contract, wantID)
		}
		if _, registered := lookupScenarioHandler(contract.Scenario); registered {
			t.Fatalf("locked migration SQL scenario %q is registered", contract.Scenario)
		}
		observation := suite.Contracts[index]
		if observation.ID != contract.ID || observation.Phase != contract.Phase ||
			observation.Status != protocol.StatusNotImplemented || observation.Result != nil ||
			observation.Error != nil || observation.DBState != nil || observation.Metrics != nil {
			t.Fatalf("migration SQL observation %d = %#v, want payload-free not_implemented", index, observation)
		}
	}
}
