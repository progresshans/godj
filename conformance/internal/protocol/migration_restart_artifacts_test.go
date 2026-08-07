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

func TestMigrationRestartArtifactHashesAreLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]string{
		"conformance/contracts/migration-restart-manifest.json":                            "93e25d02208a765001760f76715ff6e9642451c5823efc62cc40b1d249dbd42b",
		"conformance/fixtures/godj-migration-restart-not-implemented.json":                 "31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json": "90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727",
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("migration-restart artifact %s checksum = %q, want immutable baseline %q", name, got, want)
		}
	}
}

func TestMigrationRestartLockedArtifactsKeepExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationRestartArtifacts(t)
	if len(manifest.Contracts) != 10 {
		t.Fatalf("migration-restart manifest has %d contracts, want 10", len(manifest.Contracts))
	}

	successDimensions := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	errorDimensions := []ComparisonDimension{CompareError, CompareDBState, CompareMetrics}
	wantProvenance := migrationRestartProvenance()
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+27)
		if contract.ID != wantID {
			t.Fatalf("migration-restart contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if !strings.HasPrefix(contract.Scenario, "django.migration.restart.") {
			t.Fatalf("manifest contract %s scenario = %q, want migration-restart namespace", contract.ID, contract.Scenario)
		}
		if contract.Phase != PhaseEvaluation {
			t.Fatalf("manifest contract %s phase = %q, want %q", contract.ID, contract.Phase, PhaseEvaluation)
		}
		if contract.Status != ContractOracleLocked {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractOracleLocked)
		}
		wantDimensions := successDimensions
		if contract.ID == "MIG-035" {
			wantDimensions = errorDimensions
		}
		if !reflect.DeepEqual(contract.Comparison, wantDimensions) {
			t.Fatalf("manifest contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantDimensions)
		}
		wantReferences := wantProvenance[contract.ID]
		if len(contract.Provenance) != len(wantReferences) {
			t.Fatalf("manifest contract %s provenance count = %d, want %d", contract.ID, len(contract.Provenance), len(wantReferences))
		}
		for provenanceIndex, provenance := range contract.Provenance {
			gotReference := provenance.Kind + "|" + provenance.Reference
			if gotReference != wantReferences[provenanceIndex] {
				t.Fatalf("manifest contract %s provenance %d = %q, want %q", contract.ID, provenanceIndex, gotReference, wantReferences[provenanceIndex])
			}
			if provenance.Derived == nil || *provenance.Derived {
				t.Fatalf("manifest contract %s provenance %d derived = %#v, want false", contract.ID, provenanceIndex, provenance.Derived)
			}
			if provenance.License != "BSD-3-Clause" {
				t.Fatalf("manifest contract %s provenance %d license = %q, want BSD-3-Clause", contract.ID, provenanceIndex, provenance.License)
			}
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want %q", contract.ID, baseline.Contracts[index].Status, StatusNotImplemented)
		}
	}

	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django migration-restart oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj migration-restart not-implemented baseline does not validate: %v", err)
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

func migrationRestartProvenance() map[string][]string {
	const revision = "django@fe0a859f537d4238cf49fca39073513206f83122:"
	return map[string][]string{
		"MIG-027": {
			"source|" + revision + "django/db/migrations/recorder.py::MigrationRecorder.applied_migrations",
			"source|" + revision + "django/db/migrations/recorder.py::MigrationRecorder.has_table",
		},
		"MIG-028": {
			"source|" + revision + "django/db/migrations/recorder.py::MigrationRecorder.applied_migrations",
			"test|" + revision + "tests/migrations/test_loader.py::RecorderTests.test_apply",
		},
		"MIG-029": {
			"source|" + revision + "django/db/migrations/recorder.py::MigrationRecorder.record_applied",
			"test|" + revision + "tests/migrations/test_loader.py::RecorderTests.test_apply",
		},
		"MIG-030": {
			"source|" + revision + "django/db/migrations/recorder.py::MigrationRecorder.record_unapplied",
			"test|" + revision + "tests/migrations/test_loader.py::RecorderTests.test_apply",
		},
		"MIG-031": {
			"source|" + revision + "django/db/migrations/recorder.py::MigrationRecorder.migration_qs",
			"test|" + revision + "tests/migrations/test_loader.py::RecorderTests.test_apply",
		},
		"MIG-032": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.build_graph",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_run",
		},
		"MIG-033": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.build_graph",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_empty_plan",
		},
		"MIG-034": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.check_consistent_history",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
		},
		"MIG-035": {
			"source|" + revision + "django/core/management/commands/migrate.py::Command.handle",
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.check_consistent_history",
			"test|" + revision + "tests/migrations/test_loader.py::LoaderTests.test_check_consistent_history",
		},
		"MIG-036": {
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._migrate_all_forwards",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.apply_migration",
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.build_graph",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
			"test|" + revision + "tests/migrations/test_operations.py::OperationTests.test_run_python_atomic",
		},
	}
}

func TestMigrationRestartDeclaredPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationRestartArtifacts(t)
	for index, contract := range manifest.Contracts {
		contract := contract
		observation := oracle.Contracts[index]
		for _, dimension := range contract.Comparison {
			dimension := dimension
			t.Run(contract.ID+" "+string(dimension), func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				changed := &actual.Contracts[index]
				switch dimension {
				case CompareResult:
					if !mutateFirstMigrationRestartLeaf(changed.Result) {
						t.Fatalf("%s result has no mutable payload: %#v", contract.ID, observation.Result)
					}
				case CompareError:
					if changed.Error == nil {
						t.Fatalf("%s declares error comparison without an error", contract.ID)
					}
					changed.Error.Category = "changed_category"
				case CompareDBState:
					if !mutateFirstMigrationRestartLeaf(changed.DBState) {
						t.Fatalf("%s db_state has no mutable payload: %#v", contract.ID, observation.DBState)
					}
				case CompareMetrics:
					if !mutateFirstMigrationRestartLeaf(changed.Metrics) {
						t.Fatalf("%s metrics has no mutable payload: %#v", contract.ID, observation.Metrics)
					}
				default:
					t.Fatalf("%s has unsupported comparison dimension %q", contract.ID, dimension)
				}
				assertMigrationRestartMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
		if observation.Error != nil {
			t.Run(contract.ID+" error code", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				actual.Contracts[index].Error.Code = "changed_code"
				assertMigrationRestartMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationRestartSemanticMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationRestartArtifacts(t)
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "absent recorder read cannot create table",
			contractID: "MIG-027",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				migrationRestartSetBoolField(t, after, "recorder_present", true)
			},
		},
		{
			name:       "fresh reader applied identity",
			contractID: "MIG-029",
			mutate: func(t *testing.T, observation *Observation) {
				applied := migrationRestartListField(t, observation.Result, "applied_migrations")
				migrationRestartSetStringField(t, &applied.Items[0], "name", "9999_changed")
			},
		},
		{
			name:       "record setup fact remains explicit",
			contractID: "MIG-029",
			mutate: func(t *testing.T, observation *Observation) {
				setup := objectField(t, observation.Metrics, "setup")
				migrationRestartSetStringField(t, setup, "transition", "recorded_then_unrecorded")
			},
		},
		{
			name:       "record read uses a fresh recorder boundary",
			contractID: "MIG-029",
			mutate: func(t *testing.T, observation *Observation) {
				migrationRestartSetStringField(t, observation.Metrics, "restart_boundary", "fresh_executor")
			},
		},
		{
			name:       "empty recorder table remains present",
			contractID: "MIG-028",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				migrationRestartSetBoolField(t, after, "recorder_present", false)
			},
		},
		{
			name:       "empty recorder returns no applied keys",
			contractID: "MIG-028",
			mutate: func(t *testing.T, observation *Observation) {
				applied := migrationRestartListField(t, observation.Result, "applied_migrations")
				applied.Items = append(applied.Items, migrationRestartKey("alpha", "0001_initial"))
			},
		},
		{
			name:       "unrecorded key stays absent for fresh reader",
			contractID: "MIG-030",
			mutate: func(t *testing.T, observation *Observation) {
				applied := migrationRestartListField(t, observation.Result, "applied_migrations")
				applied.Items = append(applied.Items, migrationRestartKey("alpha", "0001_initial"))
			},
		},
		{
			name:       "unrecord setup fact remains explicit",
			contractID: "MIG-030",
			mutate: func(t *testing.T, observation *Observation) {
				setup := objectField(t, observation.Metrics, "setup")
				migrationRestartSetStringField(t, setup, "transition", "recorded")
			},
		},
		{
			name:       "database alias partition",
			contractID: "MIG-031",
			mutate: func(t *testing.T, observation *Observation) {
				databases := migrationRestartListField(t, observation.Result, "databases")
				first := objectField(t, &databases.Items[0], "applied_migrations")
				second := objectField(t, &databases.Items[1], "applied_migrations")
				*first, *second = *second, *first
			},
		},
		{
			name:       "remaining plan order",
			contractID: "MIG-032",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationRestartListField(t, observation.Result, "plan")
				plan.Items[0], plan.Items[1] = plan.Items[1], plan.Items[0]
			},
		},
		{
			name:       "remaining plan direction",
			contractID: "MIG-032",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationRestartListField(t, observation.Result, "plan")
				migrationRestartSetStringField(t, &plan.Items[0], "direction", "backward")
			},
		},
		{
			name:       "planning uses a fresh executor boundary",
			contractID: "MIG-032",
			mutate: func(t *testing.T, observation *Observation) {
				migrationRestartSetStringField(t, observation.Metrics, "restart_boundary", "fresh_recorder")
			},
		},
		{
			name:       "fully applied plan remains empty",
			contractID: "MIG-033",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationRestartListField(t, observation.Result, "plan")
				plan.Items = append(plan.Items, migrationRestartPlanStep("alpha", "0003_third", "forward"))
			},
		},
		{
			name:       "unknown legacy record remains outside known graph",
			contractID: "MIG-034",
			mutate: func(t *testing.T, observation *Observation) {
				known := migrationRestartListField(t, observation.Result, "known_applied")
				unknown := migrationRestartListField(t, observation.Result, "unknown_applied")
				known.Items = append(known.Items, unknown.Items[0])
				unknown.Items = unknown.Items[:0]
			},
		},
		{
			name:       "inconsistent history error taxonomy",
			contractID: "MIG-035",
			mutate: func(t *testing.T, observation *Observation) {
				observation.Error.Code = "changed_history_error"
			},
		},
		{
			name:       "inconsistent history stops before planning",
			contractID: "MIG-035",
			mutate: func(t *testing.T, observation *Observation) {
				request := objectField(t, observation.Metrics, "request")
				migrationRestartSetBoolField(t, request, "plan_invoked", true)
			},
		},
		{
			name:       "failed execution durable prefix",
			contractID: "MIG-036",
			mutate: func(t *testing.T, observation *Observation) {
				before := objectField(t, observation.DBState, "before")
				applied := migrationRestartListField(t, before, "applied_migrations")
				migrationRestartSetStringField(t, &applied.Items[0], "name", "0002_second")
			},
		},
		{
			name:       "fresh restart includes failed step before tail",
			contractID: "MIG-036",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationRestartListField(t, observation.Result, "plan")
				plan.Items = plan.Items[1:]
			},
		},
		{
			name:       "fresh restart identifies the failed migration",
			contractID: "MIG-036",
			mutate: func(t *testing.T, observation *Observation) {
				failed := objectField(t, observation.Result, "failed_migration")
				migrationRestartSetStringField(t, failed, "name", "0003_third")
			},
		},
		{
			name:       "restart capture executes no non-select statements",
			contractID: "MIG-027",
			mutate: func(t *testing.T, observation *Observation) {
				migrationRestartSetIntField(t, observation.Metrics, "non_select_statement_count", "1")
			},
		},
		{
			name:       "restart capture executes no ddl statements",
			contractID: "MIG-027",
			mutate: func(t *testing.T, observation *Observation) {
				migrationRestartSetIntField(t, observation.Metrics, "ddl_statement_count", "1")
			},
		},
		{
			name:       "restart capture executes no write statements",
			contractID: "MIG-027",
			mutate: func(t *testing.T, observation *Observation) {
				migrationRestartSetIntField(t, observation.Metrics, "write_statement_count", "1")
			},
		},
		{
			name:       "restart capture preserves state",
			contractID: "MIG-027",
			mutate: func(t *testing.T, observation *Observation) {
				migrationRestartSetBoolField(t, observation.Metrics, "state_unchanged", false)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := migrationRestartObservation(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertMigrationRestartMutationDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationRestartArtifactsRejectOrderPhaseProfileAndStatusMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationRestartArtifacts(t)
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
			changed.Contracts[0].Phase = PhaseCommit
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
}

func TestSevenCheckedInContractSetsAreGloballyDistinctAndReject42OrderedCrossBindings(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	sets := []migrationRestartContractSet{
		loadMigrationRestartContractSet(t, root, "read", "manifest.json", "oracle.json"),
		loadMigrationRestartContractSet(t, root, "write-migration", "write-migration-manifest.json", "write-migration-oracle.json"),
		loadMigrationRestartContractSet(t, root, "save-lifecycle", "save-lifecycle-manifest.json", "save-lifecycle-oracle.json"),
		loadMigrationRestartContractSet(t, root, "query-cache", "query-cache-manifest.json", "query-cache-oracle.json"),
		loadMigrationRestartContractSet(t, root, "migration-planning", "migration-planning-manifest.json", "migration-planning-oracle.json"),
		loadMigrationRestartContractSet(t, root, "migration-execution", "migration-execution-manifest.json", "migration-execution-oracle.json"),
		loadMigrationRestartContractSet(t, root, "migration-restart", "migration-restart-manifest.json", "migration-restart-oracle.json"),
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
	if totalContracts != 77 {
		t.Fatalf("seven-set reference contract count = %d, want 77", totalContracts)
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
	if crossBindings != 42 {
		t.Fatalf("checked %d ordered cross-set bindings, want 42", crossBindings)
	}
}

type migrationRestartContractSet struct {
	name     string
	manifest Manifest
	oracle   ObservationSuite
}

func loadMigrationRestartContractSet(t *testing.T, root, name, manifestName, oracleName string) migrationRestartContractSet {
	t.Helper()
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", manifestName))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", oracleName))
	if err != nil {
		t.Fatal(err)
	}
	return migrationRestartContractSet{name: name, manifest: manifest, oracle: oracle}
}

func loadMigrationRestartArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-restart-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-restart-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-restart-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func assertMigrationRestartMutationDiffers(t *testing.T, profile Profile, manifest Manifest, oracle, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("migration-restart payload mutation produced a false green")
	}
	for _, difference := range differences {
		if difference.ContractID != contractID {
			t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, contractID, differences)
		}
	}
}

func migrationRestartObservation(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("migration-restart observation %s is missing", contractID)
	return nil
}

func migrationRestartListField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueList {
		t.Fatalf("migration-restart field %q = %#v, want list", name, field)
	}
	return field
}

func migrationRestartSetStringField(t *testing.T, value *Value, name, changed string) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueString || field.Text == nil {
		t.Fatalf("migration-restart field %q = %#v, want string", name, field)
	}
	field.Text = &changed
}

func migrationRestartSetBoolField(t *testing.T, value *Value, name string, changed bool) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueBool || field.Bool == nil {
		t.Fatalf("migration-restart field %q = %#v, want bool", name, field)
	}
	field.Bool = &changed
}

func migrationRestartSetIntField(t *testing.T, value *Value, name, changed string) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueInt || field.Text == nil {
		t.Fatalf("migration-restart field %q = %#v, want int", name, field)
	}
	field.Text = &changed
}

func migrationRestartPlanStep(app, name, direction string) Value {
	return Object(map[string]Value{
		"app":       String(app),
		"direction": String(direction),
		"name":      String(name),
	})
}

func migrationRestartKey(app, name string) Value {
	return Object(map[string]Value{
		"app":  String(app),
		"name": String(name),
	})
}

func mutateFirstMigrationRestartLeaf(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
	case ValueNull:
		*value = String("mutation")
		return true
	case ValueBool:
		changed := !*value.Bool
		value.Bool = &changed
		return true
	case ValueInt, ValueDecimal:
		changed := "0"
		if *value.Text == changed {
			changed = "1"
		}
		value.Text = &changed
		return true
	case ValueString:
		changed := *value.Text + "_mutation"
		value.Text = &changed
		return true
	case ValueDatetime:
		changed := "2000-01-01T00:00:00Z"
		if *value.Text == changed {
			changed = "2000-01-02T00:00:00Z"
		}
		value.Text = &changed
		return true
	case ValueUUID:
		changed := "00000000-0000-0000-0000-000000000000"
		if *value.Text == changed {
			changed = "00000000-0000-0000-0000-000000000001"
		}
		value.Text = &changed
		return true
	case ValueBytes:
		changed := "AA=="
		if *value.Text == changed {
			changed = "AQ=="
		}
		value.Text = &changed
		return true
	case ValuePK:
		return mutateFirstMigrationRestartLeaf(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateFirstMigrationRestartLeaf(&value.Items[index]) {
				return true
			}
		}
		value.Items = append(value.Items, String("mutation"))
		return true
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstMigrationRestartLeaf(&value.Fields[index].Value) {
				return true
			}
		}
		value.Fields = append(value.Fields, NamedValue{Name: "mutation", Value: String("mutation")})
		return true
	default:
		return false
	}
}
