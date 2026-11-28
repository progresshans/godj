package protocol

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReferenceOracleChecksumCatalogMatchesCurrentArtifacts(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	const previous = "9fc8c46d8750b0b9ad11db34514075e34aadfa0adc84bd77454426fe9c5239bd  migration-execution-oracle.json\n" +
		"dd039a56ee21abf873a3bd2a5f92861bea5b0160bfa742fd47d6c2017a39f53e  migration-lifecycle-oracle.json\n" +
		"9586075f25edca8eb82ab553c09416060d992978f0282d730c9d4232178c6938  migration-planning-oracle.json\n" +
		"430349b6cb9c337d00b97b6fec59a46514bf0a41ae4db414a284a03c794f776f  migration-restart-oracle.json\n" +
		"7c9ba85081bf5680882c2f4e0ac68aa926b5339a4305603a46d86a9a8caef40a  migration-state-reconstruction-oracle.json\n" +
		"ba99ad6b7c1ccf68c92e272caf717d56c6aec43c0ef0f8ee4903e85d2a6b74fd  oracle.json\n" +
		"8887677d4b89cae722313d90542b0916b364bccf9c9031261ecbd71c12978104  query-cache-oracle.json\n" +
		"b1b4a981a83df9705f60e2570847353f3f216e30dedc899b995cac0b76968141  query-breadth-oracle.json\n" +
		"3d2327edc32f83f740f9dd8b5e2f64b7887ad10f07a36e51ca860213be10a4b8  save-lifecycle-oracle.json\n" +
		"04f398468251850059f85aa0550d03cd91d001056a33c2ad41e2d85361ba176f  write-migration-oracle.json\n"
	const definitionSource = "23bb8914f1f2c0d6af519458738c528e603de1d5017ac460a5afeee0b5d4be8c  migration-definition-source-oracle.json\n"
	const projectCheck = "a3c8040e2b80d9a8cbea60ce9fc442057f88f9fcb4b08519132261683dcf6588  migration-project-check-oracle.json\n"
	const migrationCommand = "0c950a5d6a6326351abaa43f950e5a8a7859fcb3771af1ee06d2ebb9bc2988f6  migration-command-oracle.json\n"
	const migrationWriter = "26ff5059c55db565b0babd5f4e62f16d513a0fa8f9f3d04273572b184661b5d1  migration-writer-oracle.json\n"
	const migrationStatus = "38e121058b00b6d28303d91bbf9f0d0a5efcd434c7c0dec6bf0e7d9d462f7d20  migration-status-oracle.json\n"
	const migrationTargetPlan = "0c104b05d7882dc0d740b011b8a4e63de7cb22af6a9b0cce047cd380f2e37473  migration-target-plan-oracle.json\n"
	const migrationSQLRendering = "d59cff1aa58e4f8c485c3a7a087c839396626aabcddda5d9d687d4ad1c977617  migration-sql-rendering-oracle.json\n"
	const relation = "ed5b7b008ecbdd8c2a67c8e13a8503170285aedab33f144f860d32a1c665a821  relation-oracle.json\n"
	const migrationRelation = "8483a93b506b421cac7ad54eb0ffcfd986b4374d22bf267f2f25cc3492e15ebf  migration-relation-oracle.json\n"
	const queryExpression = "463b2b0619b6e0d19030285b2fe268877aec0693ec35ffdfb877adc4dabc634c  query-expression-oracle.json\n"
	const gdj0043 = "ec3fe1dc73f919387b718dd3ee0ffea3b3a2ab7215dda5827a579109b9c2a8ab  template-form-oracle.json\n" +
		"90f8fe223bde62cc3959945cfbf00fd0df02a5d89da4026f28372e703c819d68  auth-session-oracle.json\n" +
		"e718f31177bfa81b4cfe810f7cc13633b5d3465094be33e412c5ba6c3c2a6c2e  article-admin-oracle.json\n"
	const systemState = "fb9370af22749c949e85b5929772144ecd03890653b76e440c56b0e459503959  system-state.json\n"
	if string(contents) != previous+definitionSource+projectCheck+migrationCommand+migrationWriter+migrationStatus+migrationTargetPlan+migrationSQLRendering+relation+migrationRelation+queryExpression+gdj0043+systemState {
		t.Fatal("SHA256SUMS does not match the current migration oracle catalog")
	}
}

func TestMigrationDefinitionSourceArtifactBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-definition-source")
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

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-definition-source")
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

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-definition-source")
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
			name:       "format mismatch stage",
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
			name:       "public execution observed digest",
			contractID: "MIG-064",
			mutate: func(t *testing.T, observation *Observation) {
				execution := objectField(t, observation.Result, "execution")
				*objectField(t, execution, "observed_digest") = String("sha256:changed")
			},
		},
		{
			name:       "public execution final migration records",
			contractID: "MIG-064",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationDefinitionSourceListField(t, after, "migration_records")
				records.Items = records.Items[:1]
			},
		},
		{
			name:       "public execution exactly once metric",
			contractID: "MIG-064",
			mutate: func(t *testing.T, observation *Observation) {
				*objectField(t, observation.Metrics, "session_open_calls") = Integer("2")
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := observationByID(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertMigrationDefinitionSourceDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationDefinitionSourceProvenanceMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	_, manifest, _, _ := loadContractArtifacts(t, "migration-definition-source")
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

func validateMigrationDefinitionSourceProvenance(contract Contract) error {
	decisionCount := 0
	for index, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			return fmt.Errorf("contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
		}
		if provenance.Kind == "decision" {
			decisionCount++
			if provenance.Reference != "ADR-0035" || provenance.License != "" {
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
