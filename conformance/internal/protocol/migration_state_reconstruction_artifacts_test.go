package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationStateReconstructionPassingManifestKeepsExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-state-reconstruction")
	if len(manifest.Contracts) != 10 {
		t.Fatalf("migration-state-reconstruction manifest has %d contracts, want 10", len(manifest.Contracts))
	}
	wantComparison := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	wantProvenance := migrationStateReconstructionProvenance()
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+37)
		if contract.ID != wantID {
			t.Fatalf("migration-state-reconstruction contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if !strings.HasPrefix(contract.Scenario, "django.migration.state_reconstruction.") {
			t.Fatalf("manifest contract %s scenario = %q, want migration-state-reconstruction namespace", contract.ID, contract.Scenario)
		}
		if contract.Phase != PhaseEvaluation {
			t.Fatalf("manifest contract %s phase = %q, want %q", contract.ID, contract.Phase, PhaseEvaluation)
		}
		if contract.Status != ContractPassing {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparison) {
			t.Fatalf("manifest contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparison)
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
		t.Fatalf("Django migration-state-reconstruction oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj migration-state-reconstruction not-implemented baseline does not validate: %v", err)
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

func migrationStateReconstructionProvenance() map[string][]string {
	const revision = "django@fe0a859f537d4238cf49fca39073513206f83122:"
	return map[string][]string{
		"MIG-037": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.make_state",
		},
		"MIG-038": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.make_state",
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph._generate_plan",
		},
		"MIG-039": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.make_state",
			"source|" + revision + "django/db/migrations/migration.py::Migration.mutate_state",
		},
		"MIG-040": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph._generate_plan",
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.project_state",
			"test|" + revision + "tests/migrations/test_loader.py::LoaderTests.test_load",
		},
		"MIG-041": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.make_state",
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph._generate_plan",
		},
		"MIG-042": {
			"source|" + revision + "django/db/migrations/loader.py::MigrationLoader.project_state",
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.make_state",
		},
		"MIG-043": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph._generate_plan",
			"test|" + revision + "tests/migrations/test_loader.py::LoaderTests.test_plan_handles_repeated_migrations",
		},
		"MIG-044": {
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.make_state",
			"source|" + revision + "django/db/migrations/graph.py::MigrationGraph.leaf_nodes",
		},
		"MIG-045": {
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._create_project_state",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan",
		},
		"MIG-046": {
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor.migrate",
			"source|" + revision + "django/db/migrations/executor.py::MigrationExecutor._create_project_state",
			"test|" + revision + "tests/migrations/test_executor.py::ExecutorTests.test_unrelated_applied_migrations_mutate_state",
		},
	}
}

func TestMigrationStateReconstructionDeclaredPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-state-reconstruction")
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
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationStateReconstructionSemanticPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-state-reconstruction")
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *ObservationSuite)
	}{
		{
			name:       "empty state app set",
			contractID: "MIG-037",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-037"))
				apps := migrationStateReconstructionListField(t, state, "apps")
				apps.Items = append(apps.Items, Object(map[string]Value{
					"label":  String("invented"),
					"models": List(),
				}))
			},
		},
		{
			name:       "first before excludes target state",
			contractID: "MIG-038",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				beforeState := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-038"))
				afterState := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039"))
				beforeApps := migrationStateReconstructionListField(t, beforeState, "apps")
				afterApps := migrationStateReconstructionListField(t, afterState, "apps")
				beforeApps.Items = append(beforeApps.Items, afterApps.Items[0])
			},
		},
		{
			name:       "state format version",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039"))
				migrationStateReconstructionSetIntField(t, state, "format_version", "2")
			},
		},
		{
			name:       "state app label",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				app := migrationStateReconstructionApp(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-042")), "beta")
				migrationStateReconstructionSetStringField(t, app, "label", "beta_changed")
			},
		},
		{
			name:       "state model name",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-042")), "beta", "audit")
				migrationStateReconstructionSetStringField(t, model, "name", "audit_changed")
			},
		},
		{
			name:       "state model table",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-042")), "beta", "audit")
				migrationStateReconstructionSetStringField(t, model, "db_table", "godj_state_beta_changed")
			},
		},
		{
			name:       "state models are sorted",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039"))
				models := migrationStateReconstructionListField(t, migrationStateReconstructionApp(t, state, "alpha"), "models")
				models.Items[0], models.Items[1] = models.Items[1], models.Items[0]
			},
		},
		{
			name:       "second root model is preserved",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039"))
				models := migrationStateReconstructionListField(t, migrationStateReconstructionApp(t, state, "alpha"), "models")
				models.Items = models.Items[:1]
			},
		},
		{
			name:       "field declaration order",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry")
				fields := migrationStateReconstructionListField(t, model, "fields")
				fields.Items[0], fields.Items[1] = fields.Items[1], fields.Items[0]
			},
		},
		{
			name:       "field name",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, field, "name", "headline_changed")
			},
		},
		{
			name:       "field column",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, field, "column", "headline_changed")
			},
		},
		{
			name:       "field kind",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, field, "kind", "boolean")
			},
		},
		{
			name:       "field primary key",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "id")
				migrationStateReconstructionSetBoolField(t, field, "primary_key", false)
			},
		},
		{
			name:       "field nullable",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetBoolField(t, field, "nullable", true)
			},
		},
		{
			name:       "field max length",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetIntField(t, field, "max_length", "65")
			},
		},
		{
			name:       "field default presence",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetBoolField(t, objectField(t, field, "default"), "present", false)
			},
		},
		{
			name:       "field default type tag",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, objectField(t, field, "default"), "type", "changed")
			},
		},
		{
			name:       "field default tagged value",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, objectField(t, field, "default"), "value", "changed")
			},
		},
		{
			name:       "boolean false default remains present",
			contractID: "MIG-040",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-040")), "alpha", "entry"), "published")
				migrationStateReconstructionSetBoolField(t, objectField(t, field, "default"), "value", true)
			},
		},
		{
			name:       "absent default tagged null",
			contractID: "MIG-044",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-044")), "alpha", "entry"), "summary")
				value := objectField(t, objectField(t, field, "default"), "value")
				*value = String("invented_default")
			},
		},
		{
			name:       "middle after includes target operation",
			contractID: "MIG-040",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-040")), "alpha", "entry")
				fields := migrationStateReconstructionListField(t, model, "fields")
				fields.Items = fields.Items[:len(fields.Items)-1]
			},
		},
		{
			name:       "middle before excludes target operation",
			contractID: "MIG-041",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				beforeModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-041")), "alpha", "entry")
				afterModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-040")), "alpha", "entry")
				beforeFields := migrationStateReconstructionListField(t, beforeModel, "fields")
				afterFields := migrationStateReconstructionListField(t, afterModel, "fields")
				beforeFields.Items = append(beforeFields.Items, afterFields.Items[len(afterFields.Items)-1])
			},
		},
		{
			name:       "cross app dependency state",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-042"))
				migrationStateReconstructionRemoveApp(t, state, "alpha")
			},
		},
		{
			name:       "shared dependency deduplicated",
			contractID: "MIG-043",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-043"))
				apps := migrationStateReconstructionListField(t, state, "apps")
				apps.Items = append(apps.Items, *migrationStateReconstructionApp(t, state, "alpha"))
			},
		},
		{
			name:       "latest leaves include independent app",
			contractID: "MIG-044",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-044"))
				migrationStateReconstructionRemoveApp(t, state, "delta")
			},
		},
		{
			name:       "request mode",
			contractID: "MIG-037",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				request := objectField(t, observationByID(t, suite, "MIG-037").Metrics, "request")
				migrationStateReconstructionSetStringField(t, request, "mode", "latest")
			},
		},
		{
			name:       "request position",
			contractID: "MIG-038",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				request := objectField(t, observationByID(t, suite, "MIG-038").Metrics, "request")
				migrationStateReconstructionSetStringField(t, request, "position", "after")
			},
		},
		{
			name:       "request target",
			contractID: "MIG-040",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				request := objectField(t, observationByID(t, suite, "MIG-040").Metrics, "request")
				targets := migrationStateReconstructionListField(t, request, "targets")
				migrationStateReconstructionSetStringField(t, &targets.Items[0], "name", "9999_changed")
			},
		},
		{
			name:       "definition graph node",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				graph := objectField(t, observationByID(t, suite, "MIG-042").Metrics, "graph")
				nodes := migrationStateReconstructionListField(t, graph, "nodes")
				migrationStateReconstructionSetStringField(t, &nodes.Items[0], "name", "9999_changed")
			},
		},
		{
			name:       "definition graph dependency",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				graph := objectField(t, observationByID(t, suite, "MIG-042").Metrics, "graph")
				dependencies := migrationStateReconstructionListField(t, graph, "dependencies")
				parent := objectField(t, &dependencies.Items[0], "parent")
				migrationStateReconstructionSetStringField(t, parent, "name", "9999_changed")
			},
		},
		{
			name:       "applied identities are ordered",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				applied := migrationStateReconstructionListField(t, observationByID(t, suite, "MIG-045").Result, "applied_migrations")
				applied.Items[0], applied.Items[1] = applied.Items[1], applied.Items[0]
			},
		},
		{
			name:       "known applied membership",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				known := migrationStateReconstructionListField(t, observationByID(t, suite, "MIG-045").Result, "known_applied_migrations")
				known.Items = known.Items[:1]
			},
		},
		{
			name:       "applied prefix includes applied field",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-045")), "alpha", "entry")
				fields := migrationStateReconstructionListField(t, model, "fields")
				fields.Items = fields.Items[:len(fields.Items)-1]
			},
		},
		{
			name:       "applied prefix excludes unapplied descendant",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				prefixModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-045")), "alpha", "entry")
				latestModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, observationByID(t, suite, "MIG-044")), "alpha", "entry")
				prefixFields := migrationStateReconstructionListField(t, prefixModel, "fields")
				latestFields := migrationStateReconstructionListField(t, latestModel, "fields")
				prefixFields.Items = append(prefixFields.Items, latestFields.Items[len(latestFields.Items)-1])
			},
		},
		{
			name:       "unknown applied membership",
			contractID: "MIG-046",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				unknown := migrationStateReconstructionListField(t, observationByID(t, suite, "MIG-046").Result, "unknown_applied_migrations")
				migrationStateReconstructionSetStringField(t, &unknown.Items[0], "name", "0098_changed")
			},
		},
		{
			name:       "unrelated known applied branch is included",
			contractID: "MIG-046",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-046"))
				migrationStateReconstructionRemoveApp(t, state, "delta")
			},
		},
		{
			name:       "unknown applied identity is not materialized",
			contractID: "MIG-046",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, observationByID(t, suite, "MIG-046"))
				apps := migrationStateReconstructionListField(t, state, "apps")
				apps.Items = append(apps.Items, Object(map[string]Value{
					"label":  String("legacy"),
					"models": List(),
				}))
			},
		},
		{
			name:       "database before divergent table",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				before := objectField(t, observationByID(t, suite, "MIG-039").DBState, "before")
				managed := migrationStateReconstructionListField(t, before, "managed_schema")
				migrationStateReconstructionSetStringField(t, &managed.Items[0], "name", "godj_state_alpha_entry")
			},
		},
		{
			name:       "database after divergent column",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				after := objectField(t, observationByID(t, suite, "MIG-039").DBState, "after")
				managed := migrationStateReconstructionListField(t, after, "managed_schema")
				columns := migrationStateReconstructionListField(t, &managed.Items[0], "columns")
				migrationStateReconstructionSetStringField(t, &columns.Items[0], "name", "headline_text")
			},
		},
		{
			name:       "database before applied history",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				before := objectField(t, observationByID(t, suite, "MIG-045").DBState, "before")
				applied := migrationStateReconstructionListField(t, before, "applied_migrations")
				applied.Items = applied.Items[:1]
			},
		},
		{
			name:       "database before recorder presence",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				before := objectField(t, observationByID(t, suite, "MIG-045").DBState, "before")
				migrationStateReconstructionSetBoolField(t, before, "recorder_present", false)
			},
		},
		{
			name:       "database after recorder presence",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				after := objectField(t, observationByID(t, suite, "MIG-045").DBState, "after")
				migrationStateReconstructionSetBoolField(t, after, "recorder_present", false)
			},
		},
		{
			name:       "fresh capture boundary",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetStringField(t, observationByID(t, suite, "MIG-045").Metrics, "capture_boundary", "reused_executor")
			},
		},
		{
			name:       "loaded definition replay source",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetStringField(t, observationByID(t, suite, "MIG-039").Metrics, "replay_source", "live_schema")
			},
		},
		{
			name:       "ddl statement count",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetIntField(t, observationByID(t, suite, "MIG-039").Metrics, "ddl_statement_count", "1")
			},
		},
		{
			name:       "non select statement count",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetIntField(t, observationByID(t, suite, "MIG-039").Metrics, "non_select_statement_count", "1")
			},
		},
		{
			name:       "write statement count",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetIntField(t, observationByID(t, suite, "MIG-039").Metrics, "write_statement_count", "1")
			},
		},
		{
			name:       "database state unchanged",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetBoolField(t, observationByID(t, suite, "MIG-039").Metrics, "state_unchanged", false)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			test.mutate(t, &actual)
			assertOnlyContractDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationStateReconstructionArtifactsRejectOrderPhaseProfileStatusAndComparisonMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-state-reconstruction")
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
	t.Run("manifest comparison omission", func(t *testing.T) {
		changed := cloneManifest(t, manifest)
		changed.Contracts[0].Comparison = changed.Contracts[0].Comparison[:2]
		if err := ValidateSuiteAgainst(profile, changed, oracle); err == nil || !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("comparison omission produced a false green: %v", err)
		}
	})
}

func migrationStateReconstructionState(t *testing.T, observation *Observation) *Value {
	t.Helper()
	return objectField(t, observation.Result, "state")
}

func migrationStateReconstructionListField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueList {
		t.Fatalf("migration-state-reconstruction field %q = %#v, want list", name, field)
	}
	return field
}

func migrationStateReconstructionApp(t *testing.T, state *Value, label string) *Value {
	t.Helper()
	apps := migrationStateReconstructionListField(t, state, "apps")
	for index := range apps.Items {
		appLabel := objectField(t, &apps.Items[index], "label")
		if appLabel.Type == ValueString && appLabel.Text != nil && *appLabel.Text == label {
			return &apps.Items[index]
		}
	}
	t.Fatalf("migration-state-reconstruction app %q is missing", label)
	return nil
}

func migrationStateReconstructionRemoveApp(t *testing.T, state *Value, label string) {
	t.Helper()
	apps := migrationStateReconstructionListField(t, state, "apps")
	for index := range apps.Items {
		appLabel := objectField(t, &apps.Items[index], "label")
		if appLabel.Type == ValueString && appLabel.Text != nil && *appLabel.Text == label {
			apps.Items = append(apps.Items[:index], apps.Items[index+1:]...)
			return
		}
	}
	t.Fatalf("migration-state-reconstruction app %q is missing", label)
}

func migrationStateReconstructionModel(t *testing.T, state *Value, appLabel, modelName string) *Value {
	t.Helper()
	app := migrationStateReconstructionApp(t, state, appLabel)
	models := migrationStateReconstructionListField(t, app, "models")
	for index := range models.Items {
		name := objectField(t, &models.Items[index], "name")
		if name.Type == ValueString && name.Text != nil && *name.Text == modelName {
			return &models.Items[index]
		}
	}
	t.Fatalf("migration-state-reconstruction model %s.%s is missing", appLabel, modelName)
	return nil
}

func migrationStateReconstructionField(t *testing.T, model *Value, fieldName string) *Value {
	t.Helper()
	fields := migrationStateReconstructionListField(t, model, "fields")
	for index := range fields.Items {
		name := objectField(t, &fields.Items[index], "name")
		if name.Type == ValueString && name.Text != nil && *name.Text == fieldName {
			return &fields.Items[index]
		}
	}
	t.Fatalf("migration-state-reconstruction field %q is missing", fieldName)
	return nil
}

func migrationStateReconstructionSetStringField(t *testing.T, value *Value, name, changed string) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueString || field.Text == nil {
		t.Fatalf("migration-state-reconstruction field %q = %#v, want string", name, field)
	}
	field.Text = &changed
}

func migrationStateReconstructionSetBoolField(t *testing.T, value *Value, name string, changed bool) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueBool || field.Bool == nil {
		t.Fatalf("migration-state-reconstruction field %q = %#v, want bool", name, field)
	}
	field.Bool = &changed
}

func migrationStateReconstructionSetIntField(t *testing.T, value *Value, name, changed string) {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueInt || field.Text == nil {
		t.Fatalf("migration-state-reconstruction field %q = %#v, want int", name, field)
	}
	field.Text = &changed
}
