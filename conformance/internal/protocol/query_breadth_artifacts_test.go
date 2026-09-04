package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQueryBreadthArtifactHashesAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size   int
		sha256 string
	}
	root := conformanceRepositoryRoot(t)
	wanted := map[string]artifactLock{
		"conformance/contracts/query-breadth-manifest.json": {
			size:   11282,
			sha256: "04665808db8f775096c07ac1705e6e10f139ac233f71a10c2892403005245167",
		},
		"conformance/fixtures/godj-query-breadth-not-implemented.json": {
			size:   1867,
			sha256: "f618ca120d38304f8b06064514ac06e4380a492819ad1f3dbb8627183e1eb969",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-breadth-oracle.json": {
			size:   41943,
			sha256: "0236bdab23ad8d6c9fc3c65a810badcb7048ec5b4da6c8ad7fd5387245cccf94",
		},
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != want.size {
			t.Fatalf("query-breadth artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want.sha256 {
			t.Fatalf("query-breadth artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
}

func TestQueryBreadthPassingManifestKeepsExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadQueryBreadthArtifacts(t)
	wantScenarios := []string{
		"django.query.breadth.ordered_projection",
		"django.query.breadth.source_fields_outside_projection",
		"django.query.breadth.projection_cache_independence",
		"django.query.breadth.distinct_projection",
		"django.query.breadth.stable_offset_limit",
		"django.query.breadth.invalid_offset_pre_io",
		"django.query.breadth.cold_count_and_warm_cache",
		"django.query.breadth.sliced_distinct_count",
		"django.query.breadth.empty_count_and_nullable_max",
		"django.query.breadth.filtered_count_and_max",
		"django.query.breadth.terminal_failure_ownership",
		"django.query.breadth.backend_parity_reference",
	}
	wantPhases := []Phase{
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseConstruction,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
	}
	wantComparison := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	if len(manifest.Contracts) != 12 || len(oracle.Contracts) != 12 || len(baseline.Contracts) != 12 {
		t.Fatalf("query-breadth artifact counts = manifest %d/oracle %d/static %d, want 12 each", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django query-breadth oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("query-breadth not-implemented baseline does not validate: %v", err)
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("QRY-%03d", index+22)
		if contract.ID != wantID || oracle.Contracts[index].ID != wantID || baseline.Contracts[index].ID != wantID {
			t.Fatalf("query-breadth contract %d identifiers = %q/%q/%q, want %q", index, contract.ID, oracle.Contracts[index].ID, baseline.Contracts[index].ID, wantID)
		}
		if contract.Scenario != wantScenarios[index] {
			t.Fatalf("contract %s scenario = %q, want %q", contract.ID, contract.Scenario, wantScenarios[index])
		}
		if contract.Phase != wantPhases[index] || oracle.Contracts[index].Phase != wantPhases[index] || baseline.Contracts[index].Phase != wantPhases[index] {
			t.Fatalf("contract %s phases = %q/%q/%q, want %q", contract.ID, contract.Phase, oracle.Contracts[index].Phase, baseline.Contracts[index].Phase, wantPhases[index])
		}
		if contract.Status != ContractPassing {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want %q", contract.ID, baseline.Contracts[index].Status, StatusNotImplemented)
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparison) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparison)
		}
		assertQueryBreadthProvenance(t, contract)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(manifest.Contracts) {
		t.Fatalf("got %d differences, want one for each of %d contracts: %#v", len(differences), len(manifest.Contracts), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" {
			t.Fatalf("difference %d does not preserve manifest order or explicit not-implemented status: %#v", index, difference)
		}
	}
}

func TestPreGDJ0044EighteenReferenceSetsHave201UniqueContractsAndReject306OrderedCrossBindings(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	sets := []migrationDefinitionSourceContractSet{
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
		loadMigrationDefinitionSourceContractSet(t, root, "query-breadth", "query-breadth-manifest.json", "query-breadth-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "query-expression", "query-expression-manifest.json", "query-expression-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "migration-relation", "migration-relation-manifest.json", "migration-relation-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "template-form", "template-form-manifest.json", "template-form-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "auth-session", "auth-session-manifest.json", "auth-session-oracle.json"),
		loadMigrationDefinitionSourceContractSet(t, root, "article-admin", "article-admin-manifest.json", "article-admin-oracle.json"),
	}

	contractIDs := make(map[string]string)
	scenarios := make(map[string]string)
	totalContracts := 0
	for _, set := range sets {
		if err := ValidateSuiteAgainst(profile, set.manifest, set.oracle); err != nil {
			t.Fatalf("%s set does not validate: %v", set.name, err)
		}
		totalContracts += len(set.manifest.Contracts)
		for _, contract := range set.manifest.Contracts {
			if previous, exists := contractIDs[contract.ID]; exists {
				t.Fatalf("contract ID %q is shared by %s and %s", contract.ID, previous, set.name)
			}
			contractIDs[contract.ID] = set.name
			if previous, exists := scenarios[contract.Scenario]; exists {
				t.Fatalf("scenario %q is shared by %s and %s", contract.Scenario, previous, set.name)
			}
			scenarios[contract.Scenario] = set.name
		}
	}
	if totalContracts != 201 || len(contractIDs) != 201 || len(scenarios) != 201 {
		t.Fatalf("pre-GDJ-0044 reference inventory = %d contracts/%d IDs/%d scenarios, want 201 each", totalContracts, len(contractIDs), len(scenarios))
	}

	crossBindings := 0
	for manifestIndex, manifestSet := range sets {
		for suiteIndex, suiteSet := range sets {
			if manifestIndex == suiteIndex {
				continue
			}
			crossBindings++
			if err := ValidateSuiteAgainst(profile, manifestSet.manifest, suiteSet.oracle); err == nil {
				t.Fatalf("%s manifest accepted %s oracle", manifestSet.name, suiteSet.name)
			}
		}
	}
	if crossBindings != 306 {
		t.Fatalf("current ordered cross-set bindings = %d, want 306", crossBindings)
	}
}

func TestCurrentTwentySixProductSetsHave299EligibleContractsAndExcludeZeroOracleLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	manifestNames := []string{
		"manifest.json",
		"write-migration-manifest.json",
		"save-lifecycle-manifest.json",
		"query-cache-manifest.json",
		"migration-planning-manifest.json",
		"migration-execution-manifest.json",
		"migration-restart-manifest.json",
		"migration-state-reconstruction-manifest.json",
		"migration-lifecycle-manifest.json",
		"migration-definition-source-manifest.json",
		"migration-project-check-manifest.json",
		"migration-command-manifest.json",
		"migration-writer-manifest.json",
		"migration-status-manifest.json",
		"migration-target-plan-manifest.json",
		"migration-sql-rendering-manifest.json",
		"relation-manifest.json",
		"query-breadth-manifest.json",
		"query-expression-manifest.json",
		"template-form-manifest.json",
		"auth-session-manifest.json",
		"article-admin-manifest.json",
		"system-state-manifest.json",
		"parameter-routing-manifest.json",
		"article-api-manifest.json",
		"api-authentication-manifest.json",
	}
	passing, deviations, oracleLocked, total := 0, 0, 0, 0
	for _, name := range manifestNames {
		manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, contract := range manifest.Contracts {
			switch contract.Status {
			case ContractPassing:
				passing++
				total++
			case ContractDeviation:
				deviations++
				total++
			case ContractOracleLocked:
				oracleLocked++
			default:
				t.Fatalf("manifest %s contract %s has non-product status %q", name, contract.ID, contract.Status)
			}
		}
	}
	if len(manifestNames) != 26 || total != 299 || passing != 274 || deviations != 25 || oracleLocked != 0 {
		t.Fatalf("current product inventory = %d sets/%d eligible contracts = %d passing + %d deviation with %d oracle_locked excluded, want 26/299 = 274 + 25 with 0 excluded", len(manifestNames), total, passing, deviations, oracleLocked)
	}
}

func TestQueryBreadthProductRemainsInCurrentTwentySixAdapterTarget(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	start := strings.Index(text, "godj-conformance:\n")
	end := strings.Index(text, "\noracle-check:")
	if start < 0 || end <= start {
		t.Fatal("cannot isolate godj-conformance target")
	}
	target := text[start:end]
	if !strings.Contains(target, "QUERY_BREADTH") {
		t.Fatal("query-breadth product set is missing from godj-conformance")
	}
	if got := strings.Count(target, "go run ./conformance/cmd/godjcheck"); got != 26 {
		t.Fatalf("current product adapter count = %d, want 26", got)
	}
}

func TestQueryBreadthPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadQueryBreadthArtifacts(t)
	for index, contract := range manifest.Contracts {
		index, contract := index, contract
		t.Run(contract.ID+" result", func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			if !mutateFirstQueryBreadthScalar(actual.Contracts[index].Result) {
				t.Fatalf("%s result has no mutable scalar", contract.ID)
			}
			assertQueryBreadthMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
		})
		t.Run(contract.ID+" metrics", func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			if !mutateFirstQueryBreadthScalar(actual.Contracts[index].Metrics) {
				t.Fatalf("%s metrics have no mutable scalar", contract.ID)
			}
			assertQueryBreadthMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
		})
	}

	t.Run("database state", func(t *testing.T) {
		actual := cloneSuite(t, oracle)
		if !mutateFirstQueryBreadthScalar(actual.Contracts[0].DBState) {
			t.Fatal("QRY-022 database state has no mutable scalar")
		}
		assertQueryBreadthMutationDiffers(t, profile, manifest, oracle, actual, "QRY-022")
	})
}

func TestQueryBreadthArtifactsRejectOrderPhaseAndProfileMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadQueryBreadthArtifacts(t)
	for _, artifact := range []struct {
		name  string
		suite ObservationSuite
	}{
		{name: "oracle", suite: oracle},
		{name: "not-implemented baseline", suite: baseline},
	} {
		artifact := artifact
		t.Run(artifact.name+" order", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0], changed.Contracts[1] = changed.Contracts[1], changed.Contracts[0]
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-breadth contract reordering produced a false green")
			}
		})
		t.Run(artifact.name+" phase", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Phase = differentPhase(changed.Contracts[0].Phase)
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-breadth phase mutation produced a false green")
			}
		})
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-breadth profile mutation produced a false green")
			}
		})
	}
}

func loadQueryBreadthArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "query-breadth-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-breadth-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-query-breadth-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func assertQueryBreadthProvenance(t *testing.T, contract Contract) {
	t.Helper()
	decisionCount := 0
	djangoCount := 0
	for index, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
		}
		if provenance.Kind == "decision" {
			decisionCount++
			if provenance.Reference != "ADR-0039" || provenance.License != "" {
				t.Fatalf("contract %s decision provenance = %#v", contract.ID, provenance)
			}
			continue
		}
		djangoCount++
		if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
			t.Fatalf("contract %s provenance %d reference = %q", contract.ID, index, provenance.Reference)
		}
		if provenance.License != "BSD-3-Clause" {
			t.Fatalf("contract %s provenance %d license = %q, want BSD-3-Clause", contract.ID, index, provenance.License)
		}
	}
	if decisionCount != 1 || djangoCount == 0 {
		t.Fatalf("contract %s provenance counts = decision %d/Django %d, want 1/at least 1", contract.ID, decisionCount, djangoCount)
	}
}

func assertQueryBreadthMutationDiffers(t *testing.T, profile Profile, manifest Manifest, oracle, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("query-breadth payload mutation produced a false green")
	}
	for _, difference := range differences {
		if difference.ContractID != contractID {
			t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, contractID, differences)
		}
	}
}

func mutateFirstQueryBreadthScalar(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
	case ValueNull:
		*value = String("mutated")
		return true
	case ValueBool:
		changed := !*value.Bool
		value.Bool = &changed
		return true
	case ValueInt:
		changed := "999999"
		if *value.Text == changed {
			changed = "999998"
		}
		value.Text = &changed
		return true
	case ValueString, ValueDecimal, ValueDatetime, ValueUUID, ValueBytes:
		if value.Type != ValueString {
			*value = String("mutated")
			return true
		}
		changed := *value.Text + " changed"
		value.Text = &changed
		return true
	case ValuePK:
		return mutateFirstQueryBreadthScalar(value.Nested)
	case ValueList:
		if len(value.Items) == 0 {
			value.Items = append(value.Items, String("mutated"))
			return true
		}
		for index := range value.Items {
			if mutateFirstQueryBreadthScalar(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstQueryBreadthScalar(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
