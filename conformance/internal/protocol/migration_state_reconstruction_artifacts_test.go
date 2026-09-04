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

func TestMigrationStateReconstructionArtifactHashesAreLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]string{
		"conformance/contracts/migration-state-reconstruction-manifest.json":                            "85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c",
		"conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json":                 "9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json": "bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9",
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("migration-state-reconstruction artifact %s checksum = %q, want immutable baseline %q", name, got, want)
		}
	}
}

func TestPreviousSevenContractArtifactSetsRemainBytePinned(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]string{
		"conformance/contracts/manifest.json":                                                "e395fc862d357b7d45f94fa7d2d15f5a5dfdf8c353db958adc280fd64870b874",
		"conformance/contracts/migration-execution-manifest.json":                            "1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9",
		"conformance/contracts/migration-planning-manifest.json":                             "f51d737bd68eafae32f7942669b467e3457372873ec536a13491ded60ef27ca6",
		"conformance/contracts/migration-restart-manifest.json":                              "79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290",
		"conformance/contracts/query-cache-manifest.json":                                    "35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2",
		"conformance/contracts/save-lifecycle-manifest.json":                                 "6f215f6aee153954dee84d0571cc28529c2d50ee31ee2b9755733db3f9762905",
		"conformance/contracts/write-migration-manifest.json":                                "b0ba235cb8b83e9b595b2ad3230ea7440d8b6ea74789de27c8a1f6625ecd05bb",
		"conformance/fixtures/godj-migration-execution-deviation-expected.json":              "568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547",
		"conformance/fixtures/godj-migration-execution-not-implemented.json":                 "6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04",
		"conformance/fixtures/godj-migration-planning-not-implemented.json":                  "a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13",
		"conformance/fixtures/godj-migration-restart-not-implemented.json":                   "31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55",
		"conformance/fixtures/godj-not-implemented.json":                                     "f02ea4e01e0ffcc9195d56d69129c5def0591cbcdcb5b07a62d2ec7395fa7874",
		"conformance/fixtures/godj-query-cache-not-implemented.json":                         "5cdec6cbd5440527529b08774673136c079895ab834fe2821a1626000d611d87",
		"conformance/fixtures/godj-save-lifecycle-not-implemented.json":                      "5ece667fe6babef5d01059ba4166e1243946176f9672119ae45f4c39c440c726",
		"conformance/fixtures/godj-write-migration-not-implemented.json":                     "c565c877278032637b75f99c9490c5e7e02169c8730628069533f16da6d8e707",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json": "641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json":  "7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json":   "90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json":                     "e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json":         "d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json":      "05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json":     "35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac",
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("existing artifact %s checksum changed to %q, want immutable baseline %q", name, got, want)
		}
	}
}

func TestMigrationStateReconstructionRemainsInCurrentProductConformanceTarget(t *testing.T) {
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
	if !strings.Contains(target, "MIGRATION_STATE_RECONSTRUCTION") {
		t.Fatal("migration-state-reconstruction product set is missing from the product conformance target")
	}
	if got := strings.Count(target, "go run ./conformance/cmd/godjcheck"); got != 26 {
		t.Fatalf("godj-conformance product adapter count = %d, want 26", got)
	}
}

func TestPreviousEightProductSetsRemain83PassingAnd4Deviation(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	productManifests := []string{
		"manifest.json",
		"write-migration-manifest.json",
		"save-lifecycle-manifest.json",
		"query-cache-manifest.json",
		"migration-planning-manifest.json",
		"migration-execution-manifest.json",
		"migration-restart-manifest.json",
		"migration-state-reconstruction-manifest.json",
	}
	passing := 0
	deviations := 0
	for _, name := range productManifests {
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
	if passing != 83 || deviations != 4 {
		t.Fatalf("previous eight-set classification = %d passing + %d deviation, want historical 83 + 4", passing, deviations)
	}
}

func TestMigrationStateReconstructionPassingManifestKeepsExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationStateReconstructionArtifacts(t)
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

	profile, manifest, oracle, _ := loadMigrationStateReconstructionArtifacts(t)
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
				assertMigrationStateReconstructionMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationStateReconstructionSemanticPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationStateReconstructionArtifacts(t)
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *ObservationSuite)
	}{
		{
			name:       "empty state app set",
			contractID: "MIG-037",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-037"))
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
				beforeState := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-038"))
				afterState := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039"))
				beforeApps := migrationStateReconstructionListField(t, beforeState, "apps")
				afterApps := migrationStateReconstructionListField(t, afterState, "apps")
				beforeApps.Items = append(beforeApps.Items, afterApps.Items[0])
			},
		},
		{
			name:       "state format version",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039"))
				migrationStateReconstructionSetIntField(t, state, "format_version", "2")
			},
		},
		{
			name:       "state app label",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				app := migrationStateReconstructionApp(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-042")), "beta")
				migrationStateReconstructionSetStringField(t, app, "label", "beta_changed")
			},
		},
		{
			name:       "state model name",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-042")), "beta", "audit")
				migrationStateReconstructionSetStringField(t, model, "name", "audit_changed")
			},
		},
		{
			name:       "state model table",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-042")), "beta", "audit")
				migrationStateReconstructionSetStringField(t, model, "db_table", "godj_state_beta_changed")
			},
		},
		{
			name:       "state models are sorted",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039"))
				models := migrationStateReconstructionListField(t, migrationStateReconstructionApp(t, state, "alpha"), "models")
				models.Items[0], models.Items[1] = models.Items[1], models.Items[0]
			},
		},
		{
			name:       "second root model is preserved",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039"))
				models := migrationStateReconstructionListField(t, migrationStateReconstructionApp(t, state, "alpha"), "models")
				models.Items = models.Items[:1]
			},
		},
		{
			name:       "field declaration order",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry")
				fields := migrationStateReconstructionListField(t, model, "fields")
				fields.Items[0], fields.Items[1] = fields.Items[1], fields.Items[0]
			},
		},
		{
			name:       "field name",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, field, "name", "headline_changed")
			},
		},
		{
			name:       "field column",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, field, "column", "headline_changed")
			},
		},
		{
			name:       "field kind",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, field, "kind", "boolean")
			},
		},
		{
			name:       "field primary key",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "id")
				migrationStateReconstructionSetBoolField(t, field, "primary_key", false)
			},
		},
		{
			name:       "field nullable",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetBoolField(t, field, "nullable", true)
			},
		},
		{
			name:       "field max length",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetIntField(t, field, "max_length", "65")
			},
		},
		{
			name:       "field default presence",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetBoolField(t, objectField(t, field, "default"), "present", false)
			},
		},
		{
			name:       "field default type tag",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, objectField(t, field, "default"), "type", "changed")
			},
		},
		{
			name:       "field default tagged value",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-039")), "alpha", "entry"), "headline")
				migrationStateReconstructionSetStringField(t, objectField(t, field, "default"), "value", "changed")
			},
		},
		{
			name:       "boolean false default remains present",
			contractID: "MIG-040",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-040")), "alpha", "entry"), "published")
				migrationStateReconstructionSetBoolField(t, objectField(t, field, "default"), "value", true)
			},
		},
		{
			name:       "absent default tagged null",
			contractID: "MIG-044",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				field := migrationStateReconstructionField(t, migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-044")), "alpha", "entry"), "summary")
				value := objectField(t, objectField(t, field, "default"), "value")
				*value = String("invented_default")
			},
		},
		{
			name:       "middle after includes target operation",
			contractID: "MIG-040",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-040")), "alpha", "entry")
				fields := migrationStateReconstructionListField(t, model, "fields")
				fields.Items = fields.Items[:len(fields.Items)-1]
			},
		},
		{
			name:       "middle before excludes target operation",
			contractID: "MIG-041",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				beforeModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-041")), "alpha", "entry")
				afterModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-040")), "alpha", "entry")
				beforeFields := migrationStateReconstructionListField(t, beforeModel, "fields")
				afterFields := migrationStateReconstructionListField(t, afterModel, "fields")
				beforeFields.Items = append(beforeFields.Items, afterFields.Items[len(afterFields.Items)-1])
			},
		},
		{
			name:       "cross app dependency state",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-042"))
				migrationStateReconstructionRemoveApp(t, state, "alpha")
			},
		},
		{
			name:       "shared dependency deduplicated",
			contractID: "MIG-043",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-043"))
				apps := migrationStateReconstructionListField(t, state, "apps")
				apps.Items = append(apps.Items, *migrationStateReconstructionApp(t, state, "alpha"))
			},
		},
		{
			name:       "latest leaves include independent app",
			contractID: "MIG-044",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-044"))
				migrationStateReconstructionRemoveApp(t, state, "delta")
			},
		},
		{
			name:       "request mode",
			contractID: "MIG-037",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				request := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-037").Metrics, "request")
				migrationStateReconstructionSetStringField(t, request, "mode", "latest")
			},
		},
		{
			name:       "request position",
			contractID: "MIG-038",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				request := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-038").Metrics, "request")
				migrationStateReconstructionSetStringField(t, request, "position", "after")
			},
		},
		{
			name:       "request target",
			contractID: "MIG-040",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				request := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-040").Metrics, "request")
				targets := migrationStateReconstructionListField(t, request, "targets")
				migrationStateReconstructionSetStringField(t, &targets.Items[0], "name", "9999_changed")
			},
		},
		{
			name:       "definition graph node",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				graph := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-042").Metrics, "graph")
				nodes := migrationStateReconstructionListField(t, graph, "nodes")
				migrationStateReconstructionSetStringField(t, &nodes.Items[0], "name", "9999_changed")
			},
		},
		{
			name:       "definition graph dependency",
			contractID: "MIG-042",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				graph := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-042").Metrics, "graph")
				dependencies := migrationStateReconstructionListField(t, graph, "dependencies")
				parent := objectField(t, &dependencies.Items[0], "parent")
				migrationStateReconstructionSetStringField(t, parent, "name", "9999_changed")
			},
		},
		{
			name:       "applied identities are ordered",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				applied := migrationStateReconstructionListField(t, migrationStateReconstructionObservation(t, suite, "MIG-045").Result, "applied_migrations")
				applied.Items[0], applied.Items[1] = applied.Items[1], applied.Items[0]
			},
		},
		{
			name:       "known applied membership",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				known := migrationStateReconstructionListField(t, migrationStateReconstructionObservation(t, suite, "MIG-045").Result, "known_applied_migrations")
				known.Items = known.Items[:1]
			},
		},
		{
			name:       "applied prefix includes applied field",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				model := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-045")), "alpha", "entry")
				fields := migrationStateReconstructionListField(t, model, "fields")
				fields.Items = fields.Items[:len(fields.Items)-1]
			},
		},
		{
			name:       "applied prefix excludes unapplied descendant",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				prefixModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-045")), "alpha", "entry")
				latestModel := migrationStateReconstructionModel(t, migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-044")), "alpha", "entry")
				prefixFields := migrationStateReconstructionListField(t, prefixModel, "fields")
				latestFields := migrationStateReconstructionListField(t, latestModel, "fields")
				prefixFields.Items = append(prefixFields.Items, latestFields.Items[len(latestFields.Items)-1])
			},
		},
		{
			name:       "unknown applied membership",
			contractID: "MIG-046",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				unknown := migrationStateReconstructionListField(t, migrationStateReconstructionObservation(t, suite, "MIG-046").Result, "unknown_applied_migrations")
				migrationStateReconstructionSetStringField(t, &unknown.Items[0], "name", "0098_changed")
			},
		},
		{
			name:       "unrelated known applied branch is included",
			contractID: "MIG-046",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-046"))
				migrationStateReconstructionRemoveApp(t, state, "delta")
			},
		},
		{
			name:       "unknown applied identity is not materialized",
			contractID: "MIG-046",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				state := migrationStateReconstructionState(t, migrationStateReconstructionObservation(t, suite, "MIG-046"))
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
				before := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").DBState, "before")
				managed := migrationStateReconstructionListField(t, before, "managed_schema")
				migrationStateReconstructionSetStringField(t, &managed.Items[0], "name", "godj_state_alpha_entry")
			},
		},
		{
			name:       "database after divergent column",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				after := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").DBState, "after")
				managed := migrationStateReconstructionListField(t, after, "managed_schema")
				columns := migrationStateReconstructionListField(t, &managed.Items[0], "columns")
				migrationStateReconstructionSetStringField(t, &columns.Items[0], "name", "headline_text")
			},
		},
		{
			name:       "database before applied history",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				before := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-045").DBState, "before")
				applied := migrationStateReconstructionListField(t, before, "applied_migrations")
				applied.Items = applied.Items[:1]
			},
		},
		{
			name:       "database before recorder presence",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				before := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-045").DBState, "before")
				migrationStateReconstructionSetBoolField(t, before, "recorder_present", false)
			},
		},
		{
			name:       "database after recorder presence",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				after := objectField(t, migrationStateReconstructionObservation(t, suite, "MIG-045").DBState, "after")
				migrationStateReconstructionSetBoolField(t, after, "recorder_present", false)
			},
		},
		{
			name:       "fresh capture boundary",
			contractID: "MIG-045",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetStringField(t, migrationStateReconstructionObservation(t, suite, "MIG-045").Metrics, "capture_boundary", "reused_executor")
			},
		},
		{
			name:       "loaded definition replay source",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetStringField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").Metrics, "replay_source", "live_schema")
			},
		},
		{
			name:       "ddl statement count",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetIntField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").Metrics, "ddl_statement_count", "1")
			},
		},
		{
			name:       "non select statement count",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetIntField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").Metrics, "non_select_statement_count", "1")
			},
		},
		{
			name:       "write statement count",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetIntField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").Metrics, "write_statement_count", "1")
			},
		},
		{
			name:       "database state unchanged",
			contractID: "MIG-039",
			mutate: func(t *testing.T, suite *ObservationSuite) {
				migrationStateReconstructionSetBoolField(t, migrationStateReconstructionObservation(t, suite, "MIG-039").Metrics, "state_unchanged", false)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			test.mutate(t, &actual)
			assertMigrationStateReconstructionMutationDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationStateReconstructionArtifactsRejectOrderPhaseProfileStatusAndComparisonMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationStateReconstructionArtifacts(t)
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

func TestEightCheckedInContractSetsAreGloballyDistinctAndReject56OrderedCrossBindings(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	sets := []migrationStateReconstructionContractSet{
		loadMigrationStateReconstructionContractSet(t, root, "read", "manifest.json", "oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "write-migration", "write-migration-manifest.json", "write-migration-oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "save-lifecycle", "save-lifecycle-manifest.json", "save-lifecycle-oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "query-cache", "query-cache-manifest.json", "query-cache-oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "migration-planning", "migration-planning-manifest.json", "migration-planning-oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "migration-execution", "migration-execution-manifest.json", "migration-execution-oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "migration-restart", "migration-restart-manifest.json", "migration-restart-oracle.json"),
		loadMigrationStateReconstructionContractSet(t, root, "migration-state-reconstruction", "migration-state-reconstruction-manifest.json", "migration-state-reconstruction-oracle.json"),
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
	if totalContracts != 87 {
		t.Fatalf("eight-set reference contract count = %d, want 87", totalContracts)
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
	if crossBindings != 56 {
		t.Fatalf("checked %d ordered cross-set bindings, want 56", crossBindings)
	}
}

type migrationStateReconstructionContractSet struct {
	name     string
	manifest Manifest
	oracle   ObservationSuite
}

func loadMigrationStateReconstructionContractSet(t *testing.T, root, name, manifestName, oracleName string) migrationStateReconstructionContractSet {
	t.Helper()
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", manifestName))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", oracleName))
	if err != nil {
		t.Fatal(err)
	}
	return migrationStateReconstructionContractSet{name: name, manifest: manifest, oracle: oracle}
}

func loadMigrationStateReconstructionArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-state-reconstruction-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-state-reconstruction-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-state-reconstruction-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func assertMigrationStateReconstructionMutationDiffers(t *testing.T, profile Profile, manifest Manifest, oracle, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("migration-state-reconstruction payload mutation produced a false green")
	}
	for _, difference := range differences {
		if difference.ContractID != contractID {
			t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, contractID, differences)
		}
	}
}

func migrationStateReconstructionObservation(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("migration-state-reconstruction observation %s is missing", contractID)
	return nil
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
