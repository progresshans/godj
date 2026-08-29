package protocol

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationRelationArtifactBytesAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size   int
		sha256 string
	}
	root := conformanceRepositoryRoot(t)
	wanted := map[string]artifactLock{
		"conformance/contracts/migration-relation-manifest.json": {
			size:   7858,
			sha256: "ec90feaf988e5c014a9cc08d00f6744993af146f2e5d5c4cd86d1ed6e18f25a9",
		},
		"conformance/fixtures/godj-migration-relation-not-implemented.json": {
			size:   1846,
			sha256: "f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json": {
			size:   120502,
			sha256: "5beadac7a80d0903d552e0bf9d5fae85b139ce0754d9163184d907fcf0da5968",
		},
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != want.size {
			t.Fatalf("migration-relation artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want.sha256 {
			t.Fatalf("migration-relation artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
}

func TestMigrationRelationArtifactBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationRelationArtifacts(t)
	wantSlugs := []string{
		"godj.migration.relation.current_abi",
		"godj.migration.relation.current_format_validation",
		"godj.migration.relation.current_digest",
		"godj.migration.relation.current_state",
		"godj.migration.relation.structural_preflight",
		"django.migration.relation.create_lifecycle",
		"django.migration.relation.add_nullable_populated",
		"django.migration.relation.remove_remake",
		"django.migration.relation.physical_fk_policy",
		"django.migration.relation.file_restart",
		"django.migration.relation.precommit_faults",
		"godj.migration.relation.commit_outcomes",
	}
	wantPhases := []Phase{
		PhaseConstruction,
		PhaseEnvironment,
		PhaseConstruction,
		PhaseConstruction,
		PhaseEvaluation,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseRollback,
		PhaseCommit,
	}
	wantComparisons := [][]ComparisonDimension{
		{CompareResult, CompareMetrics},
		{CompareResult, CompareMetrics},
		{CompareResult, CompareMetrics},
		{CompareResult, CompareMetrics},
		{CompareResult, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareMetrics},
	}
	if len(manifest.Contracts) != len(wantSlugs) || len(oracle.Contracts) != len(wantSlugs) || len(baseline.Contracts) != len(wantSlugs) {
		t.Fatalf("migration-relation lengths = manifest %d/oracle %d/static %d, want %d", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts), len(wantSlugs))
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+75)
		if contract.ID != wantID || contract.Scenario != wantSlugs[index] {
			t.Fatalf("contract %d = %s/%s, want %s/%s", index, contract.ID, contract.Scenario, wantID, wantSlugs[index])
		}
		if contract.Status != ContractOracleLocked {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, ContractOracleLocked)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparisons[index]) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparisons[index])
		}
		if err := validateMigrationRelationProvenance(contract); err != nil {
			t.Fatal(err)
		}

		observation := oracle.Contracts[index]
		if observation.ID != contract.ID || observation.Status != StatusObserved || observation.Phase != contract.Phase {
			t.Fatalf("oracle contract %d = %#v, want %s observed/%s", index, observation, contract.ID, contract.Phase)
		}
		assertMigrationRelationObservationDimensions(t, contract, observation)
		static := baseline.Contracts[index]
		if static.ID != contract.ID || static.Status != StatusNotImplemented || static.Phase != contract.Phase {
			t.Fatalf("static contract %d = %#v, want %s not_implemented/%s", index, static, contract.ID, contract.Phase)
		}
		if static.Result != nil || static.Error != nil || static.DBState != nil || static.Metrics != nil {
			t.Fatalf("static contract %s contains payloads: %#v", static.ID, static)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-relation oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-relation static fixture does not validate: %v", err)
	}
	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(manifest.Contracts) {
		t.Fatalf("oracle/static differences = %d, want %d: %#v", len(differences), len(manifest.Contracts), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" {
			t.Fatalf("difference %d = %#v, want ordered status-only mismatch", index, difference)
		}
	}
}

func TestMigrationRelationStaticFixtureExitsOneWithTwelveOrderedMismatches(t *testing.T) {
	root := conformanceRepositoryRoot(t)
	command := exec.Command(
		"go", "run", "./conformance/cmd/observationcmp",
		"-profile", "conformance/profiles/django-6.1-sqlite-darwin-arm64.json",
		"-manifest", "conformance/contracts/migration-relation-manifest.json",
		"-expected", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json",
		"-actual", "conformance/fixtures/godj-migration-relation-not-implemented.json",
	)
	command.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("observationcmp error = %v, want exit 1; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("observationcmp stdout = %q, want empty", stdout.String())
	}
	text := stderr.String()
	if !strings.Contains(text, "observationcmp: 12 difference(s)") {
		t.Fatalf("observationcmp stderr = %q, want 12 differences", text)
	}
	previous := -1
	for number := 75; number <= 86; number++ {
		needle := fmt.Sprintf("MIG-%03d status:", number)
		position := strings.Index(text, needle)
		if position <= previous {
			t.Fatalf("observationcmp stderr does not preserve contract order at %s: %q", needle, text)
		}
		previous = position
	}
}

func TestMigrationRelationReferenceAndProductWiringIsLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	conformanceStart := strings.Index(text, "conformance-check:\n")
	productStart := strings.Index(text, "godj-conformance:\n")
	oracleCheckStart := strings.Index(text, "oracle-check:\n")
	oracleRegenerateStart := strings.Index(text, "oracle-regenerate:\n")
	ciStart := strings.Index(text, "ci:")
	if conformanceStart < 0 || productStart <= conformanceStart || oracleCheckStart <= productStart || oracleRegenerateStart <= oracleCheckStart || ciStart <= oracleRegenerateStart {
		t.Fatal("cannot isolate Makefile conformance targets")
	}
	referenceTarget := text[conformanceStart:productStart]
	productTarget := text[productStart:oracleCheckStart]
	oracleCheckTarget := text[oracleCheckStart:oracleRegenerateStart]
	oracleRegenerateTarget := text[oracleRegenerateStart:ciStart]
	if got := strings.Count(referenceTarget, "$(MIGRATION_RELATION_MANIFEST)"); got != 2 {
		t.Fatalf("reference conformance migration-relation manifest count = %d, want 2", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_RELATION_MANIFEST)"); got != 0 {
		t.Fatalf("product conformance migration-relation manifest count = %d, want 0", got)
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 23 {
		t.Fatalf("godj-conformance adapter count = %d, want 23 with migration-relation still excluded", got)
	}
	if got := strings.Count(oracleCheckTarget, "$(MIGRATION_RELATION_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-check migration-relation manifest count = %d, want 1", got)
	}
	if got := strings.Count(oracleRegenerateTarget, "$(MIGRATION_RELATION_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-regenerate migration-relation manifest count = %d, want 1", got)
	}

	workflowContents, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowContents)
	relationProductStart := strings.Index(workflow, "  relation-product-matrix:\n")
	productProjectStart := strings.Index(workflow, "  product-project-check-matrix:\n")
	sqliteStart := strings.Index(workflow, "  sqlite-matrix:\n")
	if relationProductStart < 0 || productProjectStart <= relationProductStart || sqliteStart <= productProjectStart {
		t.Fatal("cannot isolate hosted relation-product and SQLite jobs")
	}
	relationProduct := workflow[relationProductStart:productProjectStart]
	hasPackageToken := func(want string) bool {
		for _, token := range strings.Fields(relationProduct) {
			if token == want {
				return true
			}
		}
		return false
	}
	if !hasPackageToken("./conformance/migrationrelationproduct") {
		t.Fatal("migration-relation product observer package is missing from relation-product inventory")
	}
}

func TestMigrationRelationDeclaredDimensionsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationRelationArtifacts(t)
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			contract := contract
			dimension := dimension
			t.Run(contract.ID+" "+string(dimension), func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				observation := &actual.Contracts[index]
				var changed bool
				switch dimension {
				case CompareResult:
					changed = mutateMigrationDefinitionSourceValue(observation.Result)
				case CompareDBState:
					changed = mutateMigrationDefinitionSourceValue(observation.DBState)
				case CompareMetrics:
					changed = mutateMigrationDefinitionSourceValue(observation.Metrics)
				case CompareError:
					if observation.Error != nil {
						observation.Error.Code += "_changed"
						changed = true
					}
				}
				if !changed {
					t.Fatalf("contract %s declared %s without a mutable payload", contract.ID, dimension)
				}
				assertMigrationRelationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationRelationSemanticPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationRelationArtifacts(t)
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "current digest remains byte stable",
			contractID: "MIG-075",
			mutate: func(t *testing.T, observation *Observation) {
				current := objectField(t, observation.Result, "current")
				*objectField(t, current, "canonical_sha256") = String("sha256:changed")
			},
		},
		{
			name:       "retired compatibility tuple rejection code",
			contractID: "MIG-076",
			mutate: func(t *testing.T, observation *Observation) {
				cases := migrationRelationListField(t, observation.Result, "cases")
				errorValue := objectField(t, &cases.Items[5], "error")
				*objectField(t, errorValue, "code") = String("changed_format_error")
			},
		},
		{
			name:       "combined current digest",
			contractID: "MIG-077",
			mutate: func(t *testing.T, observation *Observation) {
				combined := objectField(t, observation.Result, "combined")
				*objectField(t, combined, "digest") = String("sha256:changed")
			},
		},
		{
			name:       "current state alias freedom",
			contractID: "MIG-078",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Result, "alias_free") = Boolean(false)
			},
		},
		{
			name:       "static structural preflight has no trace",
			contractID: "MIG-079",
			mutate: func(t *testing.T, observation *Observation) {
				lanes := migrationRelationListField(t, observation.Result, "lanes")
				*objectField(t, &lanes.Items[0], "trace_events") = Integer("1")
			},
		},
		{
			name:       "create lifecycle transition order",
			contractID: "MIG-080",
			mutate: func(t *testing.T, observation *Observation) {
				transitions := migrationRelationListField(t, observation.Result, "transitions")
				*objectField(t, &transitions.Items[0], "label") = String("changed_apply")
			},
		},
		{
			name:       "required populated relation policy",
			contractID: "MIG-081",
			mutate: func(t *testing.T, observation *Observation) {
				policy := objectField(t, observation.Result, "gdj_required_populated_policy")
				errorValue := objectField(t, policy, "error")
				*objectField(t, errorValue, "code") = String("changed_backfill_policy")
			},
		},
		{
			name:       "reverse remake sequence",
			contractID: "MIG-082",
			mutate: func(t *testing.T, observation *Observation) {
				preservation := objectField(t, observation.Result, "preservation")
				sequences := migrationRelationListField(t, preservation, "sequence_after")
				*objectField(t, &sequences.Items[0], "sequence") = Integer("9")
			},
		},
		{
			name:       "physical foreign key action",
			contractID: "MIG-083",
			mutate: func(t *testing.T, observation *Observation) {
				django := objectField(t, observation.Result, "django_observation")
				actions := migrationRelationListField(t, django, "constraint_actions")
				*objectField(t, &actions.Items[0], "on_delete") = String("CASCADE")
			},
		},
		{
			name:       "fresh connection replacement",
			contractID: "MIG-084",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Result, "connection_replaced") = Boolean(false)
			},
		},
		{
			name:       "recorder fault survives fresh reopen",
			contractID: "MIG-085",
			mutate: func(t *testing.T, observation *Observation) {
				faults := migrationRelationListField(t, observation.Result, "django_faults")
				*objectField(t, &faults.Items[1], "fresh_reopen_durable") = Boolean(false)
			},
		},
		{
			name:       "recorder fault durable editor column",
			contractID: "MIG-085",
			mutate: func(t *testing.T, observation *Observation) {
				recorder := objectField(t, observation.DBState, "recorder")
				afterReopen := objectField(t, recorder, "after_reopen")
				tables := migrationRelationListField(t, afterReopen, "tables")
				columns := migrationRelationListField(t, &tables.Items[0], "columns")
				*objectField(t, &columns.Items[len(columns.Items)-1], "name") = String("changed_editor_id")
			},
		},
		{
			name:       "unknown commit outcome remains distinct",
			contractID: "MIG-086",
			mutate: func(t *testing.T, observation *Observation) {
				cases := migrationRelationListField(t, observation.Result, "cases")
				*objectField(t, &cases.Items[2], "outcome") = String("definite_failure")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := migrationRelationObservation(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertMigrationRelationDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationRelationProvenanceMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	_, manifest, _, _ := loadMigrationRelationArtifacts(t)
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{
			name: "decision reference",
			mutate: func(changed *Manifest) {
				changed.Contracts[0].Provenance[0].Reference = "ADR-9999"
			},
		},
		{
			name: "current decision derived",
			mutate: func(changed *Manifest) {
				value := true
				changed.Contracts[2].Provenance[0].Derived = &value
			},
		},
		{
			name: "Django source license",
			mutate: func(changed *Manifest) {
				changed.Contracts[5].Provenance[1].License = "changed"
			},
		},
		{
			name: "Django source on decision-only contract",
			mutate: func(changed *Manifest) {
				changed.Contracts[0].Provenance = append(changed.Contracts[0].Provenance, changed.Contracts[5].Provenance[1])
			},
		},
		{
			name: "missing provenance",
			mutate: func(changed *Manifest) {
				changed.Contracts[11].Provenance = nil
			},
		},
		{
			name: "missing current reset decision",
			mutate: func(changed *Manifest) {
				changed.Contracts[11].Provenance = changed.Contracts[11].Provenance[:1]
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			changed := cloneManifest(t, manifest)
			test.mutate(&changed)
			for _, contract := range changed.Contracts {
				if err := validateMigrationRelationProvenance(contract); err != nil {
					return
				}
			}
			t.Fatal("provenance mutation produced a false green")
		})
	}
}

func TestMigrationRelationReferenceIsDistinctFromLegacyTwelveSets(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, manifest, oracle, _ := loadMigrationRelationArtifacts(t)
	legacy := []migrationDefinitionSourceContractSet{
		loadMigrationDefinitionSourceContractSet(t, root, "read", "manifest.json", "oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "write-migration", "write-migration-manifest.json", "write-migration-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "save-lifecycle", "save-lifecycle-manifest.json", "save-lifecycle-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "query-cache", "query-cache-manifest.json", "query-cache-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-planning", "migration-planning-manifest.json", "migration-planning-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-execution", "migration-execution-manifest.json", "migration-execution-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-restart", "migration-restart-manifest.json", "migration-restart-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-state-reconstruction", "migration-state-reconstruction-manifest.json", "migration-state-reconstruction-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-lifecycle", "migration-lifecycle-manifest.json", "migration-lifecycle-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-definition-source", "migration-definition-source-manifest.json", "migration-definition-source-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-project-check", "migration-project-check-manifest.json", "migration-project-check-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "relation", "relation-manifest.json", "relation-oracle.json"),
	}
	newIDs := make(map[string]struct{}, len(manifest.Contracts))
	newScenarios := make(map[string]struct{}, len(manifest.Contracts))
	for _, contract := range manifest.Contracts {
		newIDs[contract.ID] = struct{}{}
		newScenarios[contract.Scenario] = struct{}{}
	}
	crossBindings := 0
	for _, set := range legacy {
		for _, contract := range set.manifest.Contracts {
			if _, exists := newIDs[contract.ID]; exists {
				t.Fatalf("migration-relation contract ID %q is shared with %s", contract.ID, set.name)
			}
			if _, exists := newScenarios[contract.Scenario]; exists {
				t.Fatalf("migration-relation scenario %q is shared with %s", contract.Scenario, set.name)
			}
		}
		if err := ValidateSuiteAgainst(profile, manifest, set.oracle); err == nil {
			t.Fatalf("migration-relation manifest accepted %s oracle", set.name)
		}
		crossBindings++
		if err := ValidateSuiteAgainst(profile, set.manifest, oracle); err == nil {
			t.Fatalf("%s manifest accepted migration-relation oracle", set.name)
		}
		crossBindings++
	}
	if crossBindings != 24 {
		t.Fatalf("migration-relation isolation checked %d bindings, want 24", crossBindings)
	}
}

func loadMigrationRelationArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-relation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-relation-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-relation-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func validateMigrationRelationProvenance(contract Contract) error {
	proposalIDs := map[string]bool{}
	djangoIDs := map[string]bool{
		"MIG-080": true,
		"MIG-081": true,
		"MIG-082": true,
		"MIG-083": true,
		"MIG-084": true,
		"MIG-085": true,
	}
	allowedDecisions := map[string]bool{
		"ADR-0010": true,
		"ADR-0017": true,
		"ADR-0035": true,
	}
	if len(contract.Provenance) == 0 {
		return fmt.Errorf("contract %s has no provenance", contract.ID)
	}
	hasProposal := false
	hasDjango := false
	hasCurrentDecision := false
	for index, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			return fmt.Errorf("contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
		}
		switch provenance.Kind {
		case "decision":
			if !allowedDecisions[provenance.Reference] || provenance.License != "" {
				return fmt.Errorf("contract %s decision provenance = %#v", contract.ID, provenance)
			}
			hasCurrentDecision = hasCurrentDecision || provenance.Reference == "ADR-0035"
		case "proposal":
			hasProposal = true
			if provenance.Reference != "GDJ-0036" || provenance.License != "" {
				return fmt.Errorf("contract %s proposal provenance = %#v", contract.ID, provenance)
			}
		case "source", "test":
			hasDjango = true
			if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
				return fmt.Errorf("contract %s Django provenance %d reference = %q", contract.ID, index, provenance.Reference)
			}
			if provenance.License != "BSD-3-Clause" {
				return fmt.Errorf("contract %s Django provenance %d license = %q, want BSD-3-Clause", contract.ID, index, provenance.License)
			}
		default:
			return fmt.Errorf("contract %s provenance %d kind = %q", contract.ID, index, provenance.Kind)
		}
	}
	if hasProposal != proposalIDs[contract.ID] {
		return fmt.Errorf("contract %s proposal provenance presence = %t, want %t", contract.ID, hasProposal, proposalIDs[contract.ID])
	}
	if !hasCurrentDecision {
		return fmt.Errorf("contract %s has no ADR-0035 current decision provenance", contract.ID)
	}
	if hasDjango != djangoIDs[contract.ID] {
		return fmt.Errorf("contract %s Django provenance presence = %t, want %t", contract.ID, hasDjango, djangoIDs[contract.ID])
	}
	return nil
}

func assertMigrationRelationObservationDimensions(t *testing.T, contract Contract, observation Observation) {
	t.Helper()
	wantResult := false
	wantError := false
	wantDBState := false
	wantMetrics := false
	for _, dimension := range contract.Comparison {
		switch dimension {
		case CompareResult:
			wantResult = true
		case CompareError:
			wantError = true
		case CompareDBState:
			wantDBState = true
		case CompareMetrics:
			wantMetrics = true
		}
	}
	if (observation.Result != nil) != wantResult || (observation.Error != nil) != wantError ||
		(observation.DBState != nil) != wantDBState || (observation.Metrics != nil) != wantMetrics {
		t.Fatalf(
			"observation %s dimensions result/error/db_state/metrics = %t/%t/%t/%t, want %t/%t/%t/%t",
			contract.ID,
			observation.Result != nil,
			observation.Error != nil,
			observation.DBState != nil,
			observation.Metrics != nil,
			wantResult,
			wantError,
			wantDBState,
			wantMetrics,
		)
	}
}

func assertMigrationRelationDiffers(t *testing.T, profile Profile, manifest Manifest, expected, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("migration-relation artifact mutation produced a false green")
	}
	if differences[0].ContractID != contractID {
		t.Fatalf("mutation reported against %q, want %q: %#v", differences[0].ContractID, contractID, differences)
	}
}

func migrationRelationObservation(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("observation %s is missing", contractID)
	return nil
}

func migrationRelationListField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueList {
		t.Fatalf("field %q = %#v, want list", name, field)
	}
	return field
}
