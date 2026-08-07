package protocol

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationExecutionDeviationExpectationBuildsStrictProductExpectation(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationExecutionArtifacts(t)
	manifestBefore := cloneManifest(t, manifest)
	oracleBefore := cloneSuite(t, oracle)
	expectation := loadMigrationExecutionDeviationExpectation(t)
	approvedManifest := approvedMigrationExecutionManifest(t, manifest)

	effective, product, err := PrepareDeviationExpectation(
		profile,
		approvedManifest,
		oracle,
		expectation,
		migrationExecutionDeviationPolicyForTest(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest, manifestBefore) {
		t.Fatal("preparing a product expectation mutated the locked manifest")
	}
	if !reflect.DeepEqual(oracle, oracleBefore) {
		t.Fatal("preparing a product expectation mutated the locked Django oracle")
	}
	if err := ValidateSuiteAgainst(profile, effective, product); err != nil {
		t.Fatalf("effective product suite does not validate: %v", err)
	}

	deviations := map[string]bool{
		"MIG-018": true,
		"MIG-020": true,
		"MIG-022": true,
		"MIG-024": true,
	}
	for index, contract := range effective.Contracts {
		if deviations[contract.ID] {
			if contract.Status != ContractDeviation {
				t.Fatalf("%s status = %q, want deviation", contract.ID, contract.Status)
			}
			continue
		}
		if contract.Status != ContractPassing {
			t.Fatalf("%s status = %q, want passing", contract.ID, contract.Status)
		}
		if !reflect.DeepEqual(product.Contracts[index], oracle.Contracts[index]) {
			t.Fatalf("passing contract %s product expectation differs from Django", contract.ID)
		}
	}

	for _, contractID := range []string{"MIG-018", "MIG-020", "MIG-022"} {
		observation := migrationExecutionObservation(t, &product, contractID)
		steps := migrationExecutionListField(t, observation.Metrics, "steps")
		for stepIndex := range steps.Items {
			model := migrationExecutionStringField(t, &steps.Items[stepIndex], "transaction_model")
			if model == "none" {
				continue
			}
			if model != "schema_and_record" {
				t.Fatalf("%s step %d transaction_model = %q", contractID, stepIndex, model)
			}
		}
	}

	mig024 := migrationExecutionObservation(t, &product, "MIG-024")
	if mig024.Phase != PhaseRollback {
		t.Fatalf("MIG-024 product phase = %q, want rollback", mig024.Phase)
	}
	if got := effective.Contracts[7].Phase; got != PhaseRollback {
		t.Fatalf("MIG-024 effective manifest phase = %q, want rollback", got)
	}
	if got := approvedManifest.Contracts[7].Phase; got != PhaseCommit {
		t.Fatalf("approved reference manifest phase was rewritten to %q", got)
	}
	after := objectField(t, mig024.DBState, "after")
	columns := migrationExecutionTableColumns(t, after, "godj_exec_alpha")
	if len(columns.Items) != 3 || migrationExecutionStringField(t, &columns.Items[1], "name") != "a2_marker" {
		t.Fatalf("MIG-024 product columns = %#v, want retained a2_marker", columns.Items)
	}
	if got := migrationExecutionStringField(t, migrationExecutionStep(t, mig024, 1), "status"); got != "rolled_back" {
		t.Fatalf("MIG-024 failed step status = %q, want rolled_back", got)
	}

	differences, err := Compare(profile, effective, product, product)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 0 {
		t.Fatalf("exact product expectation differs from itself: %#v", differences)
	}
	mutated := cloneSuite(t, product)
	migrationExecutionSetStepField(t, migrationExecutionObservation(t, &mutated, "MIG-024"), 1, "status", "committed")
	differences, err = Compare(profile, effective, product, mutated)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("product payload mutation produced a false green")
	}
}

func TestMigrationExecutionDeviationExpectationFailsClosed(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationExecutionArtifacts(t)
	baseManifest := approvedMigrationExecutionManifest(t, manifest)
	baseExpectation := loadMigrationExecutionDeviationExpectation(t)
	basePolicy := migrationExecutionDeviationPolicyForTest()

	tests := []struct {
		name      string
		mutate    func(*Manifest, *ObservationSuite, *DeviationExpectation, *DeviationPolicy)
		wantError string
	}{
		{
			name: "locked status is not pre-approved",
			mutate: func(manifest *Manifest, _ *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				for index := range manifest.Contracts {
					manifest.Contracts[index].Status = ContractOracleLocked
				}
			},
			wantError: "not approved for product expectation",
		},
		{
			name: "registered deviation marked passing",
			mutate: func(manifest *Manifest, _ *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				manifest.Contracts[1].Status = ContractPassing
			},
			wantError: "registered deviation is marked passing",
		},
		{
			name: "unregistered deviation",
			mutate: func(manifest *Manifest, _ *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				manifest.Contracts[0].Status = ContractDeviation
			},
			wantError: "unregistered deviation",
		},
		{
			name: "missing decision provenance",
			mutate: func(manifest *Manifest, _ *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				manifest.Contracts[1].Provenance = manifest.Contracts[1].Provenance[:len(manifest.Contracts[1].Provenance)-1]
			},
			wantError: "exactly one decision provenance",
		},
		{
			name: "passing contract carries extra decision",
			mutate: func(manifest *Manifest, _ *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				manifest.Contracts[0].Provenance = append(manifest.Contracts[0].Provenance, Provenance{
					Kind:      "decision",
					Reference: "DEV-9999",
					Derived:   boolPointer(false),
				})
			},
			wantError: "passing contract must not carry decision provenance",
		},
		{
			name: "wrong decision",
			mutate: func(_ *Manifest, _ *ObservationSuite, expectation *DeviationExpectation, _ *DeviationPolicy) {
				expectation.Decision = "DEV-9999"
			},
			wantError: "does not match policy",
		},
		{
			name: "missing deviation contract",
			mutate: func(_ *Manifest, _ *ObservationSuite, expectation *DeviationExpectation, _ *DeviationPolicy) {
				expectation.Contracts = expectation.Contracts[:len(expectation.Contracts)-1]
			},
			wantError: "policy requires 4",
		},
		{
			name: "extra unregistered change",
			mutate: func(_ *Manifest, _ *ObservationSuite, expectation *DeviationExpectation, _ *DeviationPolicy) {
				change := expectation.Contracts[0].Changes[0]
				change.Path = "steps[0].status"
				expectation.Contracts[0].Changes = append(expectation.Contracts[0].Changes, change)
			},
			wantError: "policy requires 3",
		},
		{
			name: "registered selector changed",
			mutate: func(_ *Manifest, _ *ObservationSuite, expectation *DeviationExpectation, _ *DeviationPolicy) {
				expectation.Contracts[0].Changes[0].Path = "steps[0].status"
			},
			wantError: "does not match policy",
		},
		{
			name: "reference value changed",
			mutate: func(_ *Manifest, _ *ObservationSuite, expectation *DeviationExpectation, _ *DeviationPolicy) {
				expectation.Contracts[0].Changes[0].Reference = String("none")
			},
			wantError: "reference does not match locked observation",
		},
		{
			name: "locked oracle phase changed",
			mutate: func(_ *Manifest, oracle *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				oracle.Contracts[7].Phase = PhaseRollback
			},
			wantError: "locked reference suite",
		},
		{
			name: "locked oracle is not observed",
			mutate: func(_ *Manifest, oracle *ObservationSuite, _ *DeviationExpectation, _ *DeviationPolicy) {
				oracle.Contracts[0].Status = StatusNotImplemented
				oracle.Contracts[0].Result = nil
				oracle.Contracts[0].Error = nil
				oracle.Contracts[0].DBState = nil
				oracle.Contracts[0].Metrics = nil
			},
			wantError: "must be observed",
		},
		{
			name: "policy adds relaxed selector",
			mutate: func(_ *Manifest, _ *ObservationSuite, _ *DeviationExpectation, policy *DeviationPolicy) {
				policy.Contracts[0].Changes = append(policy.Contracts[0].Changes, DeviationChangePolicy{
					Dimension: DeviationMetrics,
					Path:      "steps[0].status",
					Operation: DeviationReplace,
				})
			},
			wantError: "policy requires 4",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneManifest(t, baseManifest)
			reference := cloneSuite(t, oracle)
			expectation := cloneDeviationExpectation(t, baseExpectation)
			policy := cloneMigrationExecutionDeviationPolicy(basePolicy)
			test.mutate(&manifest, &reference, &expectation, &policy)
			_, _, err := PrepareDeviationExpectation(profile, manifest, reference, expectation, policy)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func loadMigrationExecutionDeviationExpectation(t *testing.T) DeviationExpectation {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	expectation, err := LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-execution-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	return expectation
}

func approvedMigrationExecutionManifest(t *testing.T, locked Manifest) Manifest {
	t.Helper()
	manifest := cloneManifest(t, locked)
	deviations := map[string]bool{
		"MIG-018": true,
		"MIG-020": true,
		"MIG-022": true,
		"MIG-024": true,
	}
	for index := range manifest.Contracts {
		contract := &manifest.Contracts[index]
		provenance := make([]Provenance, 0, len(contract.Provenance))
		for _, entry := range contract.Provenance {
			if entry.Kind != "decision" {
				provenance = append(provenance, entry)
			}
		}
		contract.Provenance = provenance
		if !deviations[contract.ID] {
			contract.Status = ContractPassing
			continue
		}
		contract.Status = ContractDeviation
		contract.Provenance = append(contract.Provenance, Provenance{
			Kind:      "decision",
			Reference: "DEV-0001",
			Derived:   boolPointer(false),
		})
	}
	return manifest
}

func migrationExecutionDeviationPolicyForTest() DeviationPolicy {
	replace := DeviationReplace
	metrics := DeviationMetrics
	return DeviationPolicy{
		Decision: "DEV-0001",
		Contracts: []DeviationContractPolicy{
			{ID: "MIG-018", Changes: []DeviationChangePolicy{
				{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
				{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
				{Dimension: metrics, Path: "steps[2].transaction_model", Operation: replace},
			}},
			{ID: "MIG-020", Changes: []DeviationChangePolicy{
				{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
				{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
			}},
			{ID: "MIG-022", Changes: []DeviationChangePolicy{
				{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
				{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
			}},
			{ID: "MIG-024", Changes: []DeviationChangePolicy{
				{Dimension: DeviationPhase, Path: "", Operation: replace},
				{Dimension: DeviationDBState, Path: "after.managed_schema[0].columns[1]", Operation: DeviationInsertBefore},
				{Dimension: metrics, Path: "steps[0].transaction_model", Operation: replace},
				{Dimension: metrics, Path: "steps[1].schema_outcome", Operation: replace},
				{Dimension: metrics, Path: "steps[1].status", Operation: replace},
				{Dimension: metrics, Path: "steps[1].transaction_model", Operation: replace},
			}},
		},
	}
}

func cloneDeviationExpectation(t *testing.T, value DeviationExpectation) DeviationExpectation {
	t.Helper()
	encoded, err := MarshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := DecodeDeviationExpectation(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func cloneMigrationExecutionDeviationPolicy(value DeviationPolicy) DeviationPolicy {
	cloned := DeviationPolicy{Decision: value.Decision, Contracts: make([]DeviationContractPolicy, len(value.Contracts))}
	for index, contract := range value.Contracts {
		cloned.Contracts[index] = DeviationContractPolicy{
			ID:      contract.ID,
			Changes: append([]DeviationChangePolicy(nil), contract.Changes...),
		}
	}
	return cloned
}
