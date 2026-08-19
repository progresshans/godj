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
			size:   7792,
			sha256: "dfe021c22931de3383b44068cf5f6e0ecbc86aa5f8ed96cb017c60171dcb569b",
		},
		"conformance/fixtures/godj-migration-relation-not-implemented.json": {
			size:   1846,
			sha256: "f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json": {
			size:   125248,
			sha256: "c742f91abee12708ef635c540578c6757470e34270e6594ad8a618f9b1afde27",
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
		"godj.migration.relation.legacy_abi",
		"godj.migration.relation.profile_dispatch",
		"godj.migration.relation.mixed_digest",
		"godj.migration.relation.state_promotion",
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

func TestMigrationRelationIsReferenceOnlyInMakeTargets(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	packageDirectory := filepath.Join(root, "conformance", "migrationrelation")
	entries, err := os.ReadDir(packageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{
		"backend_candidate_test.go",
		"backend_test.go",
		"fault_candidate_test.go",
		"fault_test.go",
		"lifecycle_candidate_test.go",
		"lifecycle_test.go",
		"preflight_candidate_test.go",
		"preflight_test.go",
		"profile_candidate_test.go",
		"profile_test.go",
		"sqlite_candidate_test.go",
		"sqlite_test.go",
		"state_candidate_test.go",
		"state_test.go",
	}
	if len(entries) != len(wantFiles) {
		t.Fatalf("Phase B feasibility package entries = %d, want exact %d test-only files", len(entries), len(wantFiles))
	}
	for index, entry := range entries {
		if entry.IsDir() || entry.Name() != wantFiles[index] || !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Fatalf("Phase B feasibility entry %d = %q (dir=%t), want test-only %q", index, entry.Name(), entry.IsDir(), wantFiles[index])
		}
		contents, err := os.ReadFile(filepath.Join(packageDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"conformance/contracts/",
			"conformance/oracles/",
			"conformance/fixtures/",
		} {
			if bytes.Contains(contents, []byte(forbidden)) {
				t.Fatalf("Phase B feasibility file %q reads or names checked artifact boundary %q", entry.Name(), forbidden)
			}
		}
	}

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
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 12 {
		t.Fatalf("godj-conformance adapter count = %d, want unchanged 12", got)
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
	if hasPackageToken("./conformance/migrationrelation") {
		t.Fatal("Phase B no-product feasibility package leaked into relation-product inventory")
	}
	sqliteJob := workflow[sqliteStart:]
	requiredFeasibilityFragments := []string{
		"Run GDJ-0035 Phase B no-product feasibility inventory",
		"go test -json -count=1 ./conformance/migrationrelation",
		"assert len(runs) == 75",
		"assert passes == runs",
		"assert skipped == []",
		"assert len(payload) == 9736",
		"48e7beb1994c099a0f550da54d0abdcd5bc08157b74a9db22ae3dd42d42592ec",
		"go test -race -count=1 ./conformance/migrationrelation",
		"CGO_ENABLED=0 go test -count=1 ./conformance/migrationrelation",
		"go vet ./conformance/migrationrelation",
	}
	for _, fragment := range requiredFeasibilityFragments {
		if count := strings.Count(sqliteJob, fragment); count != 1 {
			t.Fatalf("SQLite Phase B feasibility fragment %q count = %d, want 1", fragment, count)
		}
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
			name:       "legacy digest remains byte stable",
			contractID: "MIG-075",
			mutate: func(t *testing.T, observation *Observation) {
				legacy := objectField(t, observation.Result, "legacy")
				*objectField(t, legacy, "canonical_sha256") = String("sha256:changed")
			},
		},
		{
			name:       "hybrid profile rejection code",
			contractID: "MIG-076",
			mutate: func(t *testing.T, observation *Observation) {
				cases := migrationRelationListField(t, observation.Result, "cases")
				errorValue := objectField(t, &cases.Items[2], "error")
				*objectField(t, errorValue, "code") = String("changed_profile_error")
			},
		},
		{
			name:       "mixed digest domain",
			contractID: "MIG-077",
			mutate: func(t *testing.T, observation *Observation) {
				mixed := objectField(t, observation.Result, "mixed")
				*objectField(t, mixed, "digest") = String("sha256:changed")
			},
		},
		{
			name:       "state promotion alias freedom",
			contractID: "MIG-078",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Result, "alias_free") = Boolean(false)
			},
		},
		{
			name:       "structural preflight opens no sessions",
			contractID: "MIG-079",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Metrics, "session_opens") = Integer("1")
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
			name: "proposal reference",
			mutate: func(changed *Manifest) {
				changed.Contracts[1].Provenance[0].Reference = "GDJ-9999"
			},
		},
		{
			name: "proposal derived",
			mutate: func(changed *Manifest) {
				value := true
				changed.Contracts[2].Provenance[1].Derived = &value
			},
		},
		{
			name: "Django source license",
			mutate: func(changed *Manifest) {
				changed.Contracts[5].Provenance[0].License = "changed"
			},
		},
		{
			name: "Django source on decision-only contract",
			mutate: func(changed *Manifest) {
				changed.Contracts[0].Provenance = append(changed.Contracts[0].Provenance, changed.Contracts[5].Provenance[0])
			},
		},
		{
			name: "missing provenance",
			mutate: func(changed *Manifest) {
				changed.Contracts[11].Provenance = nil
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
	proposalIDs := map[string]bool{
		"MIG-076": true,
		"MIG-077": true,
		"MIG-078": true,
		"MIG-079": true,
		"MIG-081": true,
		"MIG-083": true,
		"MIG-085": true,
		"MIG-086": true,
	}
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
		"ADR-0019": true,
		"ADR-0020": true,
	}
	if len(contract.Provenance) == 0 {
		return fmt.Errorf("contract %s has no provenance", contract.ID)
	}
	hasProposal := false
	hasDjango := false
	for index, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			return fmt.Errorf("contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
		}
		switch provenance.Kind {
		case "decision":
			if !allowedDecisions[provenance.Reference] || provenance.License != "" {
				return fmt.Errorf("contract %s decision provenance = %#v", contract.ID, provenance)
			}
		case "proposal":
			hasProposal = true
			if provenance.Reference != "GDJ-0035" || provenance.License != "" {
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
