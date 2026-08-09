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

func TestMigrationDefinitionSourceArtifactHashesAreLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]string{
		"conformance/contracts/migration-definition-source-manifest.json":                            "688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488",
		"conformance/fixtures/godj-migration-definition-source-not-implemented.json":                 "41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json": "efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f",
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("migration-definition-source artifact %s checksum = %q, want locked %q", name, got, want)
		}
	}
}

func TestMigrationDefinitionSourceChecksumIsAppendedAfterUnchangedNineLines(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	const previous = "641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e  migration-execution-oracle.json\n" +
		"7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc  migration-lifecycle-oracle.json\n" +
		"7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474  migration-planning-oracle.json\n" +
		"90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727  migration-restart-oracle.json\n" +
		"bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9  migration-state-reconstruction-oracle.json\n" +
		"e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869  oracle.json\n" +
		"d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682  query-cache-oracle.json\n" +
		"05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb  save-lifecycle-oracle.json\n" +
		"35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac  write-migration-oracle.json\n"
	const appended = "efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f  migration-definition-source-oracle.json\n"
	if string(contents) != previous+appended {
		t.Fatal("SHA256SUMS did not preserve the previous nine lines and append exactly one new oracle")
	}
}

func TestMigrationDefinitionSourceArtifactBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationDefinitionSourceArtifacts(t)
	if len(manifest.Contracts) != 8 {
		t.Fatalf("migration-definition-source manifest has %d contracts, want 8", len(manifest.Contracts))
	}
	wantPhases := []Phase{
		PhaseConstruction,
		PhaseConstruction,
		PhaseConstruction,
		PhaseEnvironment,
		PhaseConstruction,
		PhaseConstruction,
		PhaseConstruction,
		PhaseCommit,
	}
	wantComparisons := [][]ComparisonDimension{
		{CompareResult, CompareMetrics},
		{CompareResult, CompareMetrics},
		{CompareResult, CompareMetrics},
		{CompareError, CompareMetrics},
		{CompareError, CompareMetrics},
		{CompareError, CompareMetrics},
		{CompareError, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+57)
		if contract.ID != wantID {
			t.Fatalf("contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if contract.Status != ContractPassing {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparisons[index]) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparisons[index])
		}
		if err := validateMigrationDefinitionSourceProvenance(contract); err != nil {
			t.Fatal(err)
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want observed", contract.ID, oracle.Contracts[index].Status)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want not_implemented", contract.ID, baseline.Contracts[index].Status)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-definition-source oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-definition-source static fixture does not validate: %v", err)
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

func TestMigrationDefinitionSourceDeclaredDimensionsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationDefinitionSourceArtifacts(t)
	for index, contract := range manifest.Contracts {
		index := index
		contract := contract
		for _, dimension := range contract.Comparison {
			dimension := dimension
			t.Run(contract.ID+" "+string(dimension), func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				observation := &actual.Contracts[index]
				switch dimension {
				case CompareResult:
					if !mutateMigrationDefinitionSourceValue(observation.Result) {
						t.Fatalf("contract %s result has no mutable payload", contract.ID)
					}
				case CompareError:
					if observation.Error == nil {
						t.Fatalf("contract %s declares error comparison without an error", contract.ID)
					}
					observation.Error.Category = "changed_category"
				case CompareDBState:
					if !mutateMigrationDefinitionSourceValue(observation.DBState) {
						t.Fatalf("contract %s db_state has no mutable payload", contract.ID)
					}
				case CompareMetrics:
					if !mutateMigrationDefinitionSourceValue(observation.Metrics) {
						t.Fatalf("contract %s metrics has no mutable payload", contract.ID)
					}
				default:
					t.Fatalf("contract %s has unsupported comparison dimension %q", contract.ID, dimension)
				}
				assertMigrationDefinitionSourceDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
		if oracle.Contracts[index].Error != nil {
			t.Run(contract.ID+" error code", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				actual.Contracts[index].Error.Code = "changed_code"
				assertMigrationDefinitionSourceDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationDefinitionSourceSemanticPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationDefinitionSourceArtifacts(t)
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "canonical definition digest",
			contractID: "MIG-057",
			mutate: func(t *testing.T, observation *Observation) {
				definitionSet := objectField(t, observation.Result, "definition_set")
				*objectField(t, definitionSet, "digest") = String("sha256:changed")
			},
		},
		{
			name:       "source inventory order",
			contractID: "MIG-057",
			mutate: func(t *testing.T, observation *Observation) {
				sources := migrationDefinitionSourceListField(t, observation.Result, "sources")
				sources.Items[0], sources.Items[1] = sources.Items[1], sources.Items[0]
			},
		},
		{
			name:       "operation order remains semantic",
			contractID: "MIG-057",
			mutate: func(t *testing.T, observation *Observation) {
				definitionSet := objectField(t, observation.Result, "definition_set")
				definitions := migrationDefinitionSourceListField(t, definitionSet, "definitions")
				operations := migrationDefinitionSourceListField(t, &definitions.Items[1], "operations")
				operations.Items[0], operations.Items[1] = operations.Items[1], operations.Items[0]
			},
		},
		{
			name:       "empty source publishes one set",
			contractID: "MIG-058",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Metrics, "definition_sets_published") = Integer("0")
			},
		},
		{
			name:       "canonicality records operation significance",
			contractID: "MIG-059",
			mutate: func(t *testing.T, observation *Observation) {
				canonicality := objectField(t, observation.Result, "canonicality")
				*objectField(t, canonicality, "operation_order_is_semantic") = Boolean(false)
			},
		},
		{
			name:       "tuple mismatch stage",
			contractID: "MIG-060",
			mutate: func(t *testing.T, observation *Observation) {
				failure := objectField(t, observation.Metrics, "failure")
				*objectField(t, failure, "stage") = String("semantic")
			},
		},
		{
			name:       "duplicate key pointer",
			contractID: "MIG-061",
			mutate: func(t *testing.T, observation *Observation) {
				failure := objectField(t, observation.Metrics, "failure")
				*objectField(t, failure, "json_pointer") = String("/migration/app")
			},
		},
		{
			name:       "duplicate node primary identity",
			contractID: "MIG-062",
			mutate: func(t *testing.T, observation *Observation) {
				failure := objectField(t, observation.Metrics, "failure")
				*objectField(t, failure, "name") = String("9999_changed")
			},
		},
		{
			name:       "unsupported operation index",
			contractID: "MIG-063",
			mutate: func(t *testing.T, observation *Observation) {
				failure := objectField(t, observation.Metrics, "failure")
				*objectField(t, failure, "operation_index") = Integer("1")
			},
		},
		{
			name:       "public handoff observed digest",
			contractID: "MIG-064",
			mutate: func(t *testing.T, observation *Observation) {
				handoff := objectField(t, observation.Result, "handoff")
				*objectField(t, handoff, "observed_digest") = String("sha256:changed")
			},
		},
		{
			name:       "public handoff final migration records",
			contractID: "MIG-064",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationDefinitionSourceListField(t, after, "migration_records")
				records.Items = records.Items[:1]
			},
		},
		{
			name:       "public handoff exactly once metric",
			contractID: "MIG-064",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Metrics, "handoff_calls") = Integer("2")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := migrationDefinitionSourceObservation(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertMigrationDefinitionSourceDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationDefinitionSourceProvenanceMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	_, manifest, _, _ := loadMigrationDefinitionSourceArtifacts(t)
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
			name: "decision derived",
			mutate: func(changed *Manifest) {
				value := true
				changed.Contracts[1].Provenance[0].Derived = &value
			},
		},
		{
			name: "decision missing",
			mutate: func(changed *Manifest) {
				changed.Contracts[2].Provenance = nil
			},
		},
		{
			name: "Django provenance on decision-only contract",
			mutate: func(changed *Manifest) {
				changed.Contracts[3].Provenance = append(changed.Contracts[3].Provenance, changed.Contracts[0].Provenance[1])
			},
		},
		{
			name: "Django provenance license",
			mutate: func(changed *Manifest) {
				changed.Contracts[7].Provenance[1].License = "changed"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			changed := cloneManifest(t, manifest)
			test.mutate(&changed)
			for _, contract := range changed.Contracts {
				if err := validateMigrationDefinitionSourceProvenance(contract); err != nil {
					return
				}
			}
			t.Fatal("provenance mutation produced a false green")
		})
	}
}

func TestTenReferenceSetsHave105UniqueContractsAndReject90OrderedCrossBindings(t *testing.T) {
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
	if totalContracts != 105 {
		t.Fatalf("ten-set reference contract count = %d, want 105", totalContracts)
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
	if crossBindings != 90 {
		t.Fatalf("checked %d ordered cross-set bindings, want 90", crossBindings)
	}
}

func TestMigrationDefinitionSourceEntersTenAdapterProductTarget(t *testing.T) {
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
	if got := strings.Count(referenceTarget, "$(MIGRATION_DEFINITION_SOURCE_MANIFEST)"); got != 2 {
		t.Fatalf("reference conformance migration-definition-source manifest count = %d, want 2", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_DEFINITION_SOURCE_MANIFEST)"); got != 1 {
		t.Fatalf("product conformance migration-definition-source manifest count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 10 {
		t.Fatalf("godj-conformance adapter count = %d, want 10", got)
	}
	if got := strings.Count(oracleCheckTarget, "$(MIGRATION_DEFINITION_SOURCE_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-check migration-definition-source manifest count = %d, want 1", got)
	}
	if got := strings.Count(oracleRegenerateTarget, "$(MIGRATION_DEFINITION_SOURCE_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-regenerate migration-definition-source manifest count = %d, want 1", got)
	}
}

func TestMigrationDefinitionSourcePreservesPreviousNineArtifactSetBytes(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]string{
		"conformance/contracts/manifest.json":                                                           "e395fc862d357b7d45f94fa7d2d15f5a5dfdf8c353db958adc280fd64870b874",
		"conformance/contracts/migration-execution-manifest.json":                                       "1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9",
		"conformance/contracts/migration-lifecycle-manifest.json":                                       "5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0",
		"conformance/contracts/migration-planning-manifest.json":                                        "f51d737bd68eafae32f7942669b467e3457372873ec536a13491ded60ef27ca6",
		"conformance/contracts/migration-restart-manifest.json":                                         "79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290",
		"conformance/contracts/migration-state-reconstruction-manifest.json":                            "85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c",
		"conformance/contracts/query-cache-manifest.json":                                               "35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2",
		"conformance/contracts/save-lifecycle-manifest.json":                                            "6f215f6aee153954dee84d0571cc28529c2d50ee31ee2b9755733db3f9762905",
		"conformance/contracts/write-migration-manifest.json":                                           "b0ba235cb8b83e9b595b2ad3230ea7440d8b6ea74789de27c8a1f6625ecd05bb",
		"conformance/fixtures/godj-migration-execution-deviation-expected.json":                         "568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547",
		"conformance/fixtures/godj-migration-execution-not-implemented.json":                            "6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04",
		"conformance/fixtures/godj-migration-lifecycle-deviation-expected.json":                         "58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b",
		"conformance/fixtures/godj-migration-lifecycle-not-implemented.json":                            "b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722",
		"conformance/fixtures/godj-migration-planning-not-implemented.json":                             "a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13",
		"conformance/fixtures/godj-migration-restart-not-implemented.json":                              "31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55",
		"conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json":                 "9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1",
		"conformance/fixtures/godj-not-implemented.json":                                                "f02ea4e01e0ffcc9195d56d69129c5def0591cbcdcb5b07a62d2ec7395fa7874",
		"conformance/fixtures/godj-query-cache-not-implemented.json":                                    "5cdec6cbd5440527529b08774673136c079895ab834fe2821a1626000d611d87",
		"conformance/fixtures/godj-save-lifecycle-not-implemented.json":                                 "5ece667fe6babef5d01059ba4166e1243946176f9672119ae45f4c39c440c726",
		"conformance/fixtures/godj-write-migration-not-implemented.json":                                "c565c877278032637b75f99c9490c5e7e02169c8730628069533f16da6d8e707",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json":            "641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json":            "7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json":             "7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json":              "90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json": "bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json":                                "e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json":                    "d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json":                 "05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json":                "35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac",
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("previous artifact %s checksum = %q, want immutable baseline %q", name, got, want)
		}
	}
}

type migrationDefinitionSourceContractSet struct {
	name     string
	manifest Manifest
	oracle   ObservationSuite
}

func loadMigrationDefinitionSourceArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-definition-source-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-definition-source-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-definition-source-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func loadMigrationDefinitionSourceContractSet(t *testing.T, root, name, manifestName, oracleName string) migrationDefinitionSourceContractSet {
	t.Helper()
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", manifestName))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", oracleName))
	if err != nil {
		t.Fatal(err)
	}
	return migrationDefinitionSourceContractSet{name: name, manifest: manifest, oracle: oracle}
}

func validateMigrationDefinitionSourceProvenance(contract Contract) error {
	decisionCount := 0
	for index, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			return fmt.Errorf("contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
		}
		if provenance.Kind == "decision" {
			decisionCount++
			if provenance.Reference != "ADR-0019" || provenance.License != "" {
				return fmt.Errorf("contract %s decision provenance = %#v", contract.ID, provenance)
			}
			continue
		}
		if contract.ID != "MIG-057" && contract.ID != "MIG-064" {
			return fmt.Errorf("contract %s unexpectedly claims Django provenance %#v", contract.ID, provenance)
		}
		if provenance.Kind != "source" && provenance.Kind != "test" {
			return fmt.Errorf("contract %s provenance %d kind = %q", contract.ID, index, provenance.Kind)
		}
		if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
			return fmt.Errorf("contract %s provenance %d reference = %q", contract.ID, index, provenance.Reference)
		}
		if provenance.License != "BSD-3-Clause" {
			return fmt.Errorf("contract %s provenance %d license = %q, want BSD-3-Clause", contract.ID, index, provenance.License)
		}
	}
	if decisionCount != 1 {
		return fmt.Errorf("contract %s decision provenance count = %d, want 1", contract.ID, decisionCount)
	}
	return nil
}

func assertMigrationDefinitionSourceDiffers(t *testing.T, profile Profile, manifest Manifest, expected, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("artifact mutation produced a false green")
	}
	if differences[0].ContractID != contractID {
		t.Fatalf("mutation reported against %q, want %q: %#v", differences[0].ContractID, contractID, differences)
	}
}

func migrationDefinitionSourceObservation(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("observation %s is missing", contractID)
	return nil
}

func migrationDefinitionSourceListField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueList {
		t.Fatalf("field %q = %#v, want list", name, field)
	}
	return field
}

func mutateMigrationDefinitionSourceValue(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
	case ValueNull:
		*value = String("changed")
		return true
	case ValueBool:
		changed := !*value.Bool
		value.Bool = &changed
		return true
	case ValueInt:
		changed := "1"
		if *value.Text == changed {
			changed = "2"
		}
		value.Text = &changed
		return true
	case ValueString:
		changed := *value.Text + "_changed"
		value.Text = &changed
		return true
	case ValueDecimal:
		changed := "1.5"
		value.Text = &changed
		return true
	case ValueDatetime:
		changed := "2000-01-01T00:00:00Z"
		value.Text = &changed
		return true
	case ValueUUID:
		changed := "00000000-0000-0000-0000-000000000000"
		value.Text = &changed
		return true
	case ValueBytes:
		changed := ""
		value.Text = &changed
		return true
	case ValuePK:
		return mutateMigrationDefinitionSourceValue(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateMigrationDefinitionSourceValue(&value.Items[index]) {
				return true
			}
		}
		value.Items = append(value.Items, String("changed"))
		return true
	case ValueObject:
		for index := range value.Fields {
			if mutateMigrationDefinitionSourceValue(&value.Fields[index].Value) {
				return true
			}
		}
		value.Fields = append(value.Fields, NamedValue{Name: "changed", Value: String("changed")})
		return true
	default:
		return false
	}
}
