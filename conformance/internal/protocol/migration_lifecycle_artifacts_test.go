package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationLifecycleEntersProductTargetAt92PassingAnd5ReviewedDeviations(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	start := strings.Index(text, "godj-conformance:")
	end := strings.Index(text, "\noracle-check:")
	if start < 0 || end <= start {
		t.Fatal("cannot isolate godj-conformance target")
	}
	productTarget := text[start:end]
	if !strings.Contains(productTarget, "MIGRATION_LIFECYCLE_DEVIATION_EXPECTED") {
		t.Fatal("migration-lifecycle product adapter is missing its reviewed deviation expectation")
	}

	previousProductManifests := []string{
		"manifest.json",
		"write-migration-manifest.json",
		"save-lifecycle-manifest.json",
		"query-cache-manifest.json",
		"migration-planning-manifest.json",
		"migration-execution-manifest.json",
		"migration-restart-manifest.json",
		"migration-state-reconstruction-manifest.json",
	}
	countStatuses := func(names []string) (int, int) {
		t.Helper()
		passing := 0
		deviations := 0
		for _, name := range names {
			manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", name))
			if err != nil {
				t.Fatal(err)
			}
			for _, contract := range manifest.Contracts {
				switch contract.Status {
				case ContractPassing:
					passing++
				case ContractDeviation:
					deviations++
				default:
					t.Fatalf("product manifest %s contract %s status = %q", name, contract.ID, contract.Status)
				}
			}
		}
		return passing, deviations
	}
	passing, deviations := countStatuses(previousProductManifests)
	if passing != 83 || deviations != 4 {
		t.Fatalf("previous eight-set product classification = %d passing + %d deviation, want 83 + 4", passing, deviations)
	}
	productManifests := append(append([]string(nil), previousProductManifests...), "migration-lifecycle-manifest.json")
	passing, deviations = countStatuses(productManifests)
	if passing != 92 || deviations != 5 {
		t.Fatalf("nine-set product classification = %d passing + %d deviation, want 92 + 5", passing, deviations)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-lifecycle-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range manifest.Contracts {
		want := ContractPassing
		if contract.ID == "MIG-052" {
			want = ContractDeviation
		}
		if contract.Status != want {
			t.Fatalf("migration-lifecycle contract %s status = %q, want %q", contract.ID, contract.Status, want)
		}
	}
}

func TestMigrationLifecycleProductManifestKeepsExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-lifecycle")
	if len(manifest.Contracts) != 10 {
		t.Fatalf("migration-lifecycle manifest has %d contracts, want 10", len(manifest.Contracts))
	}

	successDimensions := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	errorDimensions := []ComparisonDimension{CompareError, CompareDBState, CompareMetrics}
	wantPhases := []Phase{
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseEvaluation,
		PhaseRollback,
		PhaseCommit,
	}
	if err := validateMigrationLifecycleProvenance(manifest); err != nil {
		t.Fatal(err)
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+47)
		if contract.ID != wantID {
			t.Fatalf("migration-lifecycle contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if !strings.HasPrefix(contract.Scenario, "django.migration.lifecycle.") {
			t.Fatalf("manifest contract %s scenario = %q, want migration-lifecycle namespace", contract.ID, contract.Scenario)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("manifest contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		wantStatus := ContractPassing
		if contract.ID == "MIG-052" {
			wantStatus = ContractDeviation
		}
		if contract.Status != wantStatus {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, wantStatus)
		}
		wantDimensions := successDimensions
		if contract.ID == "MIG-054" || contract.ID == "MIG-055" {
			wantDimensions = errorDimensions
		}
		if !reflect.DeepEqual(contract.Comparison, wantDimensions) {
			t.Fatalf("manifest contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantDimensions)
		}
		if len(contract.Provenance) == 0 {
			t.Fatalf("manifest contract %s has no provenance", contract.ID)
		}
		seenReferences := make(map[string]bool)
		decisionCount := 0
		for provenanceIndex, provenance := range contract.Provenance {
			if provenance.Kind == "decision" {
				decisionCount++
				if contract.ID != "MIG-052" || provenance.Reference != "DEV-0002" || provenance.License != "" {
					t.Fatalf("manifest contract %s decision provenance = %#v", contract.ID, provenance)
				}
				if provenance.Derived == nil || *provenance.Derived {
					t.Fatalf("manifest contract %s decision provenance derived = %#v, want false", contract.ID, provenance.Derived)
				}
				continue
			}
			if provenance.Kind != "documentation" && provenance.Kind != "source" && provenance.Kind != "test" {
				t.Fatalf("manifest contract %s provenance %d kind = %q", contract.ID, provenanceIndex, provenance.Kind)
			}
			if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
				t.Fatalf("manifest contract %s provenance %d reference = %q", contract.ID, provenanceIndex, provenance.Reference)
			}
			if seenReferences[provenance.Kind+"|"+provenance.Reference] {
				t.Fatalf("manifest contract %s has duplicate provenance %q", contract.ID, provenance.Reference)
			}
			seenReferences[provenance.Kind+"|"+provenance.Reference] = true
			if provenance.Derived == nil || *provenance.Derived {
				t.Fatalf("manifest contract %s provenance %d derived = %#v, want false", contract.ID, provenanceIndex, provenance.Derived)
			}
			if provenance.License != "BSD-3-Clause" {
				t.Fatalf("manifest contract %s provenance %d license = %q, want BSD-3-Clause", contract.ID, provenanceIndex, provenance.License)
			}
		}
		wantDecisionCount := 0
		if contract.ID == "MIG-052" {
			wantDecisionCount = 1
		}
		if decisionCount != wantDecisionCount {
			t.Fatalf("manifest contract %s decision provenance count = %d, want %d", contract.ID, decisionCount, wantDecisionCount)
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want %q", contract.ID, baseline.Contracts[index].Status, StatusNotImplemented)
		}
	}

	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django migration-lifecycle oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj migration-lifecycle not-implemented baseline does not validate: %v", err)
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

func TestMigrationLifecycleDeclaredPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-lifecycle")
	for index, contract := range manifest.Contracts {
		contract := contract
		for _, dimension := range contract.Comparison {
			dimension := dimension
			t.Run(contract.ID+" "+string(dimension), func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				changed := &actual.Contracts[index]
				switch dimension {
				case CompareResult:
					if !mutateFirstMigrationRestartLeaf(changed.Result) {
						t.Fatalf("%s result has no mutable payload", contract.ID)
					}
				case CompareError:
					if changed.Error == nil {
						t.Fatalf("%s declares error comparison without an error", contract.ID)
					}
					changed.Error.Category = "changed_category"
				case CompareDBState:
					if !mutateFirstMigrationRestartLeaf(changed.DBState) {
						t.Fatalf("%s db_state has no mutable payload", contract.ID)
					}
				case CompareMetrics:
					if !mutateFirstMigrationRestartLeaf(changed.Metrics) {
						t.Fatalf("%s metrics has no mutable payload", contract.ID)
					}
				default:
					t.Fatalf("%s has unsupported comparison dimension %q", contract.ID, dimension)
				}
				assertMigrationLifecycleMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
		if oracle.Contracts[index].Error != nil {
			t.Run(contract.ID+" error code", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				actual.Contracts[index].Error.Code = "changed_code"
				assertMigrationLifecycleMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationLifecycleSemanticPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-lifecycle")
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "fresh latest plan order",
			contractID: "MIG-047",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationLifecycleListField(t, observation.Result, "plan")
				if len(plan.Items) < 2 {
					t.Fatalf("fresh latest plan = %#v, want at least two steps", plan)
				}
				plan.Items[0], plan.Items[1] = plan.Items[1], plan.Items[0]
			},
		},
		{
			name:       "fresh latest committed step outcome",
			contractID: "MIG-047",
			mutate: func(t *testing.T, observation *Observation) {
				steps := migrationLifecycleListField(t, observation.Metrics, "steps")
				migrationLifecycleSetStringField(t, &steps.Items[0], "outcome", "not_started")
			},
		},
		{
			name:       "applied prefix is preserved before tail",
			contractID: "MIG-048",
			mutate: func(t *testing.T, observation *Observation) {
				before := objectField(t, observation.DBState, "before")
				records := migrationLifecycleListField(t, before, "migration_records")
				migrationLifecycleSetStringField(t, &records.Items[0], "name", "0002_second")
			},
		},
		{
			name:       "applied prefix tail starts at second migration",
			contractID: "MIG-048",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationLifecycleListField(t, observation.Result, "plan")
				migrationLifecycleSetStringField(t, &plan.Items[0], "name", "0001_initial")
			},
		},
		{
			name:       "fully applied latest plan remains empty",
			contractID: "MIG-049",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationLifecycleListField(t, observation.Result, "plan")
				plan.Items = append(plan.Items, migrationRestartPlanStep("alpha", "0003_third", "forward"))
			},
		},
		{
			name:       "fully applied latest has no transaction effect",
			contractID: "MIG-049",
			mutate: func(t *testing.T, observation *Observation) {
				effects := objectField(t, observation.Metrics, "effects")
				migrationLifecycleSetBoolField(t, effects, "transaction_observed", true)
			},
		},
		{
			name:       "named forward target remains explicit",
			contractID: "MIG-050",
			mutate: func(t *testing.T, observation *Observation) {
				request := objectField(t, observation.Metrics, "request")
				targets := migrationLifecycleListField(t, request, "targets")
				migrationLifecycleSetStringField(t, &targets.Items[0], "name", "0003_third")
			},
		},
		{
			name:       "named reverse preserves unrelated branch record",
			contractID: "MIG-051",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationLifecycleListField(t, after, "migration_records")
				migrationLifecycleRemoveIdentity(t, records, "beta", "0001_initial")
			},
		},
		{
			name:       "named reverse abstract step direction",
			contractID: "MIG-051",
			mutate: func(t *testing.T, observation *Observation) {
				steps := migrationLifecycleListField(t, observation.Metrics, "steps")
				migrationLifecycleSetStringField(t, &steps.Items[0], "direction", "forward")
			},
		},
		{
			name:       "zero target remains distinct from a named target",
			contractID: "MIG-052",
			mutate: func(t *testing.T, observation *Observation) {
				request := objectField(t, observation.Metrics, "request")
				targets := migrationLifecycleListField(t, request, "targets")
				name := objectField(t, &targets.Items[0], "name")
				*name = String("0001_initial")
			},
		},
		{
			name:       "unknown legacy identity survives known tail",
			contractID: "MIG-053",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationLifecycleListField(t, after, "migration_records")
				migrationLifecycleRemoveIdentity(t, records, "legacy", "0099_retired")
			},
		},
		{
			name:       "inconsistent history stops before planning",
			contractID: "MIG-054",
			mutate: func(t *testing.T, observation *Observation) {
				preflight := objectField(t, observation.Metrics, "history_preflight")
				migrationLifecycleSetBoolField(t, preflight, "plan_invoked", true)
			},
		},
		{
			name:       "inconsistent history has no durable effect",
			contractID: "MIG-054",
			mutate: func(t *testing.T, observation *Observation) {
				effects := objectField(t, observation.Metrics, "effects")
				migrationLifecycleSetBoolField(t, effects, "database_state_changed", true)
			},
		},
		{
			name:       "middle failure keeps first migration durable",
			contractID: "MIG-055",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationLifecycleListField(t, after, "migration_records")
				migrationLifecycleSetStringField(t, &records.Items[0], "name", "0002_second")
			},
		},
		{
			name:       "middle failure rolls back failed step",
			contractID: "MIG-055",
			mutate: func(t *testing.T, observation *Observation) {
				steps := migrationLifecycleListField(t, observation.Metrics, "steps")
				migrationLifecycleSetStringField(t, &steps.Items[1], "outcome", "committed")
			},
		},
		{
			name:       "middle failure leaves tail unstarted",
			contractID: "MIG-055",
			mutate: func(t *testing.T, observation *Observation) {
				steps := migrationLifecycleListField(t, observation.Metrics, "steps")
				migrationLifecycleSetStringField(t, &steps.Items[2], "outcome", "committed")
			},
		},
		{
			name:       "restart reopens temporary file connection",
			contractID: "MIG-056",
			mutate: func(t *testing.T, observation *Observation) {
				restart := objectField(t, observation.Metrics, "restart")
				migrationLifecycleSetBoolField(t, restart, "connection_reopened", false)
			},
		},
		{
			name:       "restart plan resumes at failed second migration",
			contractID: "MIG-056",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationLifecycleListField(t, observation.Result, "plan")
				migrationLifecycleSetStringField(t, &plan.Items[0], "name", "0001_initial")
			},
		},
		{
			name:       "restart setup records original durable prefix",
			contractID: "MIG-056",
			mutate: func(t *testing.T, observation *Observation) {
				restart := objectField(t, observation.Metrics, "restart")
				setup := objectField(t, restart, "setup")
				prefix := migrationLifecycleListField(t, setup, "durable_prefix")
				migrationLifecycleSetStringField(t, &prefix.Items[0], "name", "0002_second")
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := observationByID(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertMigrationLifecycleMutationDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationLifecycleArtifactsRejectOrderPhaseProfileStatusAndManifestMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-lifecycle")
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
				t.Fatal("contract reordering produced a false green")
			}
		})
		t.Run(artifact.name+" phase", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Phase = PhaseEvaluation
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("phase mutation produced a false green")
			}
		})
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("profile mutation produced a false green")
			}
		})
		t.Run(artifact.name+" status", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Status = ObservationStatus("changed_status")
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("status mutation produced a false green")
			}
		})
	}

	t.Run("manifest draft status", func(t *testing.T) {
		changed := cloneManifest(t, manifest)
		changed.Contracts[0].Status = ContractDraft
		if err := ValidateSuiteAgainst(profile, changed, oracle); err == nil || !strings.Contains(err.Error(), "locked-or-later") {
			t.Fatalf("draft manifest status produced a false green: %v", err)
		}
	})
	t.Run("manifest contract ID", func(t *testing.T) {
		changed := cloneManifest(t, manifest)
		changed.Contracts[0].ID = "MIG-999"
		if err := ValidateSuiteAgainst(profile, changed, oracle); err == nil {
			t.Fatal("manifest contract ID mutation produced a false green")
		}
	})
}

func TestMigrationLifecycleProvenanceMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	_, manifest, _, _ := loadContractArtifacts(t, "migration-lifecycle")
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{
			name: "reference",
			mutate: func(changed *Manifest) {
				changed.Contracts[0].Provenance[0].Reference += ".changed"
			},
		},
		{
			name: "kind",
			mutate: func(changed *Manifest) {
				changed.Contracts[3].Provenance[0].Kind = "source"
			},
		},
		{
			name: "order",
			mutate: func(changed *Manifest) {
				provenance := changed.Contracts[0].Provenance
				provenance[0], provenance[1] = provenance[1], provenance[0]
			},
		},
		{
			name: "derived",
			mutate: func(changed *Manifest) {
				value := true
				changed.Contracts[0].Provenance[0].Derived = &value
			},
		},
		{
			name: "license",
			mutate: func(changed *Manifest) {
				changed.Contracts[0].Provenance[0].License = "changed-license"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			changed := cloneManifest(t, manifest)
			test.mutate(&changed)
			if err := validateMigrationLifecycleProvenance(changed); err == nil {
				t.Fatal("provenance mutation produced a false green")
			}
		})
	}
}

func TestNineCheckedInContractSetsAreGloballyDistinctAndReject72OrderedCrossBindings(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	sets := []migrationLifecycleContractSet{
		loadMigrationLifecycleContractSet(t, root, "read", "manifest.json", "oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "write-migration", "write-migration-manifest.json", "write-migration-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "save-lifecycle", "save-lifecycle-manifest.json", "save-lifecycle-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "query-cache", "query-cache-manifest.json", "query-cache-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "migration-planning", "migration-planning-manifest.json", "migration-planning-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "migration-execution", "migration-execution-manifest.json", "migration-execution-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "migration-restart", "migration-restart-manifest.json", "migration-restart-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "migration-state-reconstruction", "migration-state-reconstruction-manifest.json", "migration-state-reconstruction-oracle.json"),
		loadMigrationLifecycleContractSet(t, root, "migration-lifecycle", "migration-lifecycle-manifest.json", "migration-lifecycle-oracle.json"),
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
	if totalContracts != 97 {
		t.Fatalf("nine-set reference contract count = %d, want 97", totalContracts)
	}

	crossBindings := 0
	for manifestIndex, manifestSet := range sets {
		for suiteIndex, suiteSet := range sets {
			if manifestIndex == suiteIndex {
				continue
			}
			crossBindings++
			t.Run(manifestSet.name+" manifest rejects "+suiteSet.name+" oracle", func(t *testing.T) {
				if err := ValidateSuiteAgainst(profile, manifestSet.manifest, suiteSet.oracle); err == nil {
					t.Fatal("checked-in cross-set binding produced a false green")
				}
			})
		}
	}
	if crossBindings != 72 {
		t.Fatalf("checked %d ordered cross-set bindings, want 72", crossBindings)
	}
}

type migrationLifecycleContractSet struct {
	name     string
	manifest Manifest
	oracle   ObservationSuite
}

func validateMigrationLifecycleProvenance(manifest Manifest) error {
	wanted := migrationLifecycleProvenance()
	if len(manifest.Contracts) != len(wanted) {
		return fmt.Errorf("migration-lifecycle manifest has %d contracts, want %d provenance entries", len(manifest.Contracts), len(wanted))
	}
	for _, contract := range manifest.Contracts {
		want, ok := wanted[contract.ID]
		if !ok {
			return fmt.Errorf("migration-lifecycle contract %s has no locked provenance", contract.ID)
		}
		if len(contract.Provenance) != len(want) {
			return fmt.Errorf("migration-lifecycle contract %s provenance count = %d, want %d", contract.ID, len(contract.Provenance), len(want))
		}
		for index, provenance := range contract.Provenance {
			got := provenance.Kind + "|" + provenance.Reference
			if got != want[index] {
				return fmt.Errorf("migration-lifecycle contract %s provenance %d = %q, want %q", contract.ID, index, got, want[index])
			}
			if provenance.Derived == nil || *provenance.Derived {
				return fmt.Errorf("migration-lifecycle contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
			}
			wantLicense := "BSD-3-Clause"
			if provenance.Kind == "decision" {
				wantLicense = ""
			}
			if provenance.License != wantLicense {
				return fmt.Errorf("migration-lifecycle contract %s provenance %d license = %q, want %q", contract.ID, index, provenance.License, wantLicense)
			}
		}
	}
	return nil
}

func migrationLifecycleProvenance() map[string][]string {
	const revision = "django@fe0a859f537d4238cf49fca39073513206f83122:"
	return map[string][]string{
		"MIG-047": {
			"source|" + revision + "django/core/management/commands/migrate.py::Command.handle",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._migrate_all_forwards",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_run",
		},
		"MIG-048": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.build_graph",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._create_project_state",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_run",
		},
		"MIG-049": {
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_empty_plan",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_migrate_skips_schema_creation",
		},
		"MIG-050": {
			"documentation|" + revision + "docs/ref/django-admin.txt#migrate",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_run",
		},
		"MIG-051": {
			"documentation|" + revision + "docs/ref/django-admin.txt#migrate",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._migrate_all_backwards",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorUnitTests.test_minimize_rollbacks",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_unrelated_applied_migrations_mutate_state",
		},
		"MIG-052": {
			"documentation|" + revision + "docs/ref/django-admin.txt#migrate",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._migrate_all_backwards",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_run",
			"decision|DEV-0002",
		},
		"MIG-053": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.build_graph",
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.check_consistent_history",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
		},
		"MIG-054": {
			"source|" + revision + "django/core/management/commands/migrate.py::Command.handle",
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.check_consistent_history",
			"test|" + revision + "tests/migrations/test_commands.py::MigrateTests.test_migrate_inconsistent_history",
			"test|" + revision + "tests/migrations/test_loader.py::LoaderTests.test_check_consistent_history",
		},
		"MIG-055": {
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._migrate_all_forwards",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.apply_migration",
			"source|" + revision + "django/db/migrations/migration.py::Migration.apply",
			"test|" + revision + "tests/migrations/test_operations.py::OperationTests.test_run_python_atomic",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_migrations_applied_and_recorded_atomically",
		},
		"MIG-056": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.build_graph",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_run",
		},
	}
}

func loadMigrationLifecycleContractSet(t *testing.T, root, name, manifestName, oracleName string) migrationLifecycleContractSet {
	t.Helper()
	manifest := requireArtifact(t, filepath.Join(root, "conformance", "contracts", manifestName), LoadManifest)
	oracle := requireArtifact(t, filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", oracleName), LoadObservationSuite)
	return migrationLifecycleContractSet{name: name, manifest: manifest, oracle: oracle}
}

func assertMigrationLifecycleMutationDiffers(
	t *testing.T,
	profile Profile,
	manifest Manifest,
	oracle ObservationSuite,
	actual ObservationSuite,
	contractID string,
) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatalf("%s mutation produced a false green", contractID)
	}
	if differences[0].ContractID != contractID {
		t.Fatalf("mutation reported against %q, want %q: %#v", differences[0].ContractID, contractID, differences)
	}
}

func migrationLifecycleListField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueList {
		t.Fatalf("migration-lifecycle field %q = %#v, want list", name, field)
	}
	return field
}

func migrationLifecycleSetStringField(t *testing.T, value *Value, name, changed string) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueString || field.Text == nil {
		t.Fatalf("migration-lifecycle field %q = %#v, want string", name, field)
	}
	field.Text = &changed
}

func migrationLifecycleSetBoolField(t *testing.T, value *Value, name string, changed bool) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueBool || field.Bool == nil {
		t.Fatalf("migration-lifecycle field %q = %#v, want bool", name, field)
	}
	field.Bool = &changed
}

func migrationLifecycleRemoveIdentity(t *testing.T, list *Value, app, name string) {
	t.Helper()
	if list.Type != ValueList {
		t.Fatalf("migration-lifecycle identity collection = %#v, want list", list)
	}
	for index := range list.Items {
		itemApp := objectField(t, &list.Items[index], "app")
		itemName := objectField(t, &list.Items[index], "name")
		if itemApp.Type != ValueString || itemApp.Text == nil || itemName.Type != ValueString || itemName.Text == nil {
			t.Fatalf("migration-lifecycle identity = %#v, want string app/name", list.Items[index])
		}
		if *itemApp.Text == app && *itemName.Text == name {
			list.Items = append(list.Items[:index], list.Items[index+1:]...)
			return
		}
	}
	t.Fatalf("migration-lifecycle identity %s.%s not found", app, name)
}
