package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGDJ0050DeviationPolicyAndFixtureOwnExactlyNineteenResultLeaves(t *testing.T) {
	t.Parallel()

	policy, err := deviationPolicyForDecision("DEV-0010")
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := map[string][]string{
		"MIG-103": {
			"cases[0].migrations[0].operations[1].fields[3].on_delete",
			"cases[1].migrations[1].operations[0].fields[3].on_delete",
		},
		"MIG-104": {"migrations[0].name"},
		"MIG-105": {"files_before", "files_after", "output"},
		"MIG-106": {
			"cases[0].files_before", "cases[0].files_after", "cases[0].output",
			"cases[1].files_before", "cases[1].files_after", "cases[1].output",
		},
		"MIG-107": {
			"cases[0].code", "cases[1].code", "cases[2].code", "cases[3].code",
			"cases[4].code", "cases[5].code", "cases[6].code",
		},
	}
	if policy.Decision != "DEV-0010" || len(policy.Contracts) != len(wantPaths) {
		t.Fatalf("DEV-0010 policy = %#v", policy)
	}
	root := filepath.Join("..", "..", "..")
	expectation, err := protocol.LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-writer-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if expectation.Decision != policy.Decision || len(expectation.Contracts) != len(policy.Contracts) {
		t.Fatalf("DEV-0010 fixture/policy shape = %#v / %#v", expectation, policy)
	}
	changes := 0
	for index, contract := range policy.Contracts {
		paths, ok := wantPaths[contract.ID]
		if !ok || len(contract.Changes) != len(paths) {
			t.Fatalf("DEV-0010 policy contract %d = %#v", index, contract)
		}
		fixtureContract := expectation.Contracts[index]
		if fixtureContract.ID != contract.ID || len(fixtureContract.Changes) != len(contract.Changes) {
			t.Fatalf("DEV-0010 fixture contract %d = %#v", index, fixtureContract)
		}
		for changeIndex, change := range contract.Changes {
			if change.Dimension != protocol.DeviationResult || change.Operation != protocol.DeviationReplace || change.Path != paths[changeIndex] {
				t.Fatalf("DEV-0010 policy selector %s[%d] = %#v", contract.ID, changeIndex, change)
			}
			fixtureChange := fixtureContract.Changes[changeIndex]
			if fixtureChange.Dimension != change.Dimension || fixtureChange.Operation != change.Operation || fixtureChange.Path != change.Path {
				t.Fatalf("DEV-0010 fixture selector %s[%d] = %#v", contract.ID, changeIndex, fixtureChange)
			}
			changes++
		}
	}
	if changes != 19 {
		t.Fatalf("DEV-0010 selector count = %d, want 19", changes)
	}
}

func TestRunGDJ0050StrictProductExpectationWritesTwelveActuals(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	actualPath := filepath.Join(t.TempDir(), "migration-writer-actual.json")
	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", filepath.Join(root, "conformance", "contracts", "migration-writer-manifest.json"),
		"-expected", filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-writer-oracle.json"),
		"-deviation-expected", filepath.Join(root, "conformance", "fixtures", "godj-migration-writer-deviation-expected.json"),
		"-actual-output", actualPath,
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if want := "GoDj observations match the reviewed product expectation for 12 contracts under DEV-0010"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	document, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"migration-writer-oracle.json", "godj-migration-writer-not-implemented.json", "godj-migration-writer-deviation-expected.json"} {
		if bytes.Contains(document, []byte(forbidden)) {
			t.Fatalf("GDJ-0050 actual artifact contains expected-artifact boundary %q", forbidden)
		}
	}
	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != 12 || actual.Contracts[0].ID != "MIG-099" || actual.Contracts[11].ID != "MIG-110" {
		t.Fatalf("GDJ-0050 actual contracts = %#v", actual.Contracts)
	}
	for _, observation := range actual.Contracts {
		if observation.Status != protocol.StatusObserved {
			t.Fatalf("%s actual status = %q, want observed", observation.ID, observation.Status)
		}
	}
}
