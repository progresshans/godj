package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMigrationExecutionArtifactHashesAreLocked(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	wanted := map[string]string{
		"conformance/contracts/migration-execution-manifest.json":                            "f414cd7a495f6e6765df06ca1427485ecc16a8d19c344f190f5f1421dc2a517d",
		"conformance/fixtures/godj-migration-execution-not-implemented.json":                 "6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04",
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json": "641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e",
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want {
			t.Fatalf("migration-execution artifact %s checksum = %q, want immutable baseline %q", name, got, want)
		}
	}
}

func TestMigrationExecutionOracleLockedArtifactsKeepExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationExecutionArtifacts(t)
	if len(manifest.Contracts) != 10 {
		t.Fatalf("migration-execution manifest has %d contracts, want 10", len(manifest.Contracts))
	}

	wantPhases := []Phase{
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseCommit,
		PhaseRollback,
		PhaseRollback,
		PhaseRollback,
		PhaseCommit,
		PhaseEvaluation,
		PhaseCommit,
	}
	resultDimensions := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	errorDimensions := []ComparisonDimension{CompareError, CompareDBState, CompareMetrics}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+17)
		if contract.ID != wantID {
			t.Fatalf("migration-execution contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("manifest contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		if contract.Status != ContractOracleLocked {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractOracleLocked)
		}
		wantDimensions := errorDimensions
		if index < 4 || index == 9 {
			wantDimensions = resultDimensions
		}
		if !reflect.DeepEqual(contract.Comparison, wantDimensions) {
			t.Fatalf("manifest contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantDimensions)
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want %q", contract.ID, baseline.Contracts[index].Status, StatusNotImplemented)
		}
	}

	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django migration-execution oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj migration-execution not-implemented baseline does not validate: %v", err)
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

func TestMigrationExecutionDeclaredPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationExecutionArtifacts(t)
	for index, contract := range manifest.Contracts {
		contract := contract
		observation := &oracle.Contracts[index]
		for _, dimension := range contract.Comparison {
			dimension := dimension
			t.Run(contract.ID+" "+string(dimension), func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				changed := &actual.Contracts[index]
				switch dimension {
				case CompareResult:
					if !mutateFirstMigrationExecutionLeaf(changed.Result) {
						t.Fatalf("%s result has no mutable payload: %#v", contract.ID, observation.Result)
					}
				case CompareError:
					if changed.Error == nil {
						t.Fatalf("%s declares error comparison without an error", contract.ID)
					}
					changed.Error.Code += "_mutation"
				case CompareDBState:
					if !mutateFirstMigrationExecutionLeaf(changed.DBState) {
						t.Fatalf("%s db_state has no mutable payload: %#v", contract.ID, observation.DBState)
					}
				case CompareMetrics:
					if !mutateFirstMigrationExecutionLeaf(changed.Metrics) {
						t.Fatalf("%s metrics has no mutable payload: %#v", contract.ID, observation.Metrics)
					}
				default:
					t.Fatalf("%s has unsupported comparison dimension %q", contract.ID, dimension)
				}
				assertMigrationExecutionMutationDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationExecutionSemanticMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationExecutionArtifacts(t)
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "forward executed step order",
			contractID: "MIG-017",
			mutate: func(t *testing.T, observation *Observation) {
				steps := migrationExecutionListField(t, observation.Metrics, "steps")
				steps.Items[0], steps.Items[1] = steps.Items[1], steps.Items[0]
			},
		},
		{
			name:       "backward executed step direction",
			contractID: "MIG-018",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 0, "direction", "forward")
			},
		},
		{
			name:       "committed step status",
			contractID: "MIG-017",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "status", "rolled_back")
			},
		},
		{
			name:       "forward schema outcome",
			contractID: "MIG-017",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "schema_outcome", "rolled_back")
			},
		},
		{
			name:       "forward recorder outcome",
			contractID: "MIG-017",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "recorder_outcome", "failed")
			},
		},
		{
			name:       "forward transaction model",
			contractID: "MIG-017",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "transaction_model", "none")
			},
		},
		{
			name:       "backward schema then recorder transaction model",
			contractID: "MIG-018",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 0, "transaction_model", "schema_and_record")
			},
		},
		{
			name:       "applied prefix historical state before",
			contractID: "MIG-019",
			mutate: func(t *testing.T, observation *Observation) {
				step := migrationExecutionStep(t, observation, 0)
				stateBefore := objectField(t, step, "historical_state_before")
				models := migrationExecutionListField(t, stateBefore, "models")
				fields := migrationExecutionListField(t, &models.Items[0], "fields")
				fields.Items = fields.Items[1:]
			},
		},
		{
			name:       "applied prefix historical state after",
			contractID: "MIG-019",
			mutate: func(t *testing.T, observation *Observation) {
				step := migrationExecutionStep(t, observation, 0)
				stateAfter := objectField(t, step, "historical_state_after")
				models := migrationExecutionListField(t, stateAfter, "models")
				fields := migrationExecutionListField(t, &models.Items[0], "fields")
				fields.Items = append(fields.Items[:1], fields.Items[2:]...)
			},
		},
		{
			name:       "unrelated branch schema preservation",
			contractID: "MIG-020",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				schema := migrationExecutionListField(t, after, "managed_schema")
				removeMigrationExecutionObject(t, schema, map[string]string{"name": "godj_exec_beta"})
			},
		},
		{
			name:       "unrelated branch record preservation",
			contractID: "MIG-020",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationExecutionListField(t, after, "migration_records")
				removeMigrationExecutionObject(t, records, map[string]string{"app": "beta", "name": "0001_initial"})
			},
		},
		{
			name:       "forward operation failure step",
			contractID: "MIG-021",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "status", "committed")
			},
		},
		{
			name:       "forward failure leaves tail unstarted",
			contractID: "MIG-021",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 2, "status", "committed")
			},
		},
		{
			name:       "backward operation failure step",
			contractID: "MIG-022",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "schema_outcome", "reversed")
			},
		},
		{
			name:       "backward failure leaves earlier step unstarted",
			contractID: "MIG-022",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 2, "status", "committed")
			},
		},
		{
			name:       "forward recorder fault point",
			contractID: "MIG-023",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "fault_point", "after_record_write")
			},
		},
		{
			name:       "forward recorder failure rolls back schema",
			contractID: "MIG-023",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				columns := migrationExecutionTableColumns(t, after, "godj_exec_alpha")
				insertMigrationExecutionColumnBeforeID(t, columns, migrationExecutionA2Column())
			},
		},
		{
			name:       "forward recorder failure rolls back record",
			contractID: "MIG-023",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationExecutionListField(t, after, "migration_records")
				records.Items = append(records.Items, migrationExecutionRecord("alpha", "0002_second"))
			},
		},
		{
			name:       "reverse recorder fault point",
			contractID: "MIG-024",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 1, "fault_point", "after_record_write")
			},
		},
		{
			name:       "reverse recorder failure commits schema removal",
			contractID: "MIG-024",
			mutate: func(t *testing.T, observation *Observation) {
				before := objectField(t, observation.DBState, "before")
				after := objectField(t, observation.DBState, "after")
				beforeColumns := migrationExecutionTableColumns(t, before, "godj_exec_alpha")
				afterColumns := migrationExecutionTableColumns(t, after, "godj_exec_alpha")
				a2 := migrationExecutionObject(t, beforeColumns, map[string]string{"name": "a2_marker"})
				insertMigrationExecutionColumnBeforeID(t, afterColumns, *a2)
			},
		},
		{
			name:       "reverse recorder failure retains record",
			contractID: "MIG-024",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				records := migrationExecutionListField(t, after, "migration_records")
				removeMigrationExecutionObject(t, records, map[string]string{"app": "alpha", "name": "0002_second"})
			},
		},
		{
			name:       "mixed plan error taxonomy",
			contractID: "MIG-025",
			mutate: func(t *testing.T, observation *Observation) {
				observation.Error.Code = "operation_failed"
			},
		},
		{
			name:       "mixed plan preserves domain state",
			contractID: "MIG-025",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				recorder := objectField(t, after, "recorder_present")
				changed := false
				recorder.Bool = &changed
			},
		},
		{
			name:       "mixed plan leaves every step unstarted",
			contractID: "MIG-025",
			mutate: func(t *testing.T, observation *Observation) {
				migrationExecutionSetStepField(t, observation, 0, "status", "committed")
			},
		},
		{
			name:       "empty plan does not create recorder",
			contractID: "MIG-026",
			mutate: func(t *testing.T, observation *Observation) {
				after := objectField(t, observation.DBState, "after")
				recorder := objectField(t, after, "recorder_present")
				changed := true
				recorder.Bool = &changed
			},
		},
		{
			name:       "empty plan executes no steps",
			contractID: "MIG-026",
			mutate: func(t *testing.T, observation *Observation) {
				steps := migrationExecutionListField(t, observation.Metrics, "steps")
				steps.Items = append(steps.Items, migrationExecutionUnexpectedStep())
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := migrationExecutionObservation(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertMigrationExecutionMutationDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationExecutionArtifactsRejectOrderPhaseStatusAndProfileMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadMigrationExecutionArtifacts(t)
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
			changed.Contracts[0].Phase = differentPhase(changed.Contracts[0].Phase)
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("phase mutation produced a false green")
			}
		})
		t.Run(artifact.name+" status", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Status = ObservationStatus("changed_status")
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("status mutation produced a false green")
			}
		})
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("profile mutation produced a false green")
			}
		})
	}
}

func loadMigrationExecutionArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-execution-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-execution-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-execution-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func assertMigrationExecutionMutationDiffers(t *testing.T, profile Profile, manifest Manifest, oracle, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("migration-execution payload mutation produced a false green")
	}
	for _, difference := range differences {
		if difference.ContractID != contractID {
			t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, contractID, differences)
		}
	}
}

func migrationExecutionObservation(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("migration-execution observation %s is missing", contractID)
	return nil
}

func migrationExecutionListField(t *testing.T, value *Value, name string) *Value {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueList {
		t.Fatalf("migration-execution field %q = %#v, want list", name, field)
	}
	return field
}

func migrationExecutionStep(t *testing.T, observation *Observation, index int) *Value {
	t.Helper()
	steps := migrationExecutionListField(t, observation.Metrics, "steps")
	if index < 0 || index >= len(steps.Items) {
		t.Fatalf("migration-execution step index %d is outside %d steps", index, len(steps.Items))
	}
	return &steps.Items[index]
}

func migrationExecutionSetStepField(t *testing.T, observation *Observation, index int, name, changed string) {
	t.Helper()
	field := objectField(t, migrationExecutionStep(t, observation, index), name)
	if field.Type != ValueString || field.Text == nil {
		t.Fatalf("migration-execution step %d field %q = %#v, want string", index, name, field)
	}
	field.Text = &changed
}

func migrationExecutionStringField(t *testing.T, value *Value, name string) string {
	t.Helper()
	field := objectField(t, value, name)
	if field.Type != ValueString || field.Text == nil {
		t.Fatalf("migration-execution field %q = %#v, want string", name, field)
	}
	return *field.Text
}

func migrationExecutionTableColumns(t *testing.T, state *Value, tableName string) *Value {
	t.Helper()
	schema := migrationExecutionListField(t, state, "managed_schema")
	table := migrationExecutionObject(t, schema, map[string]string{"name": tableName})
	return migrationExecutionListField(t, table, "columns")
}

func migrationExecutionObject(t *testing.T, list *Value, fields map[string]string) *Value {
	t.Helper()
	if list.Type != ValueList {
		t.Fatalf("migration-execution selection source = %#v, want list", list)
	}
	for index := range list.Items {
		candidate := &list.Items[index]
		matches := candidate.Type == ValueObject
		for name, want := range fields {
			if !matches || migrationExecutionStringField(t, candidate, name) != want {
				matches = false
				break
			}
		}
		if matches {
			return candidate
		}
	}
	t.Fatalf("migration-execution object matching %#v is missing", fields)
	return nil
}

func removeMigrationExecutionObject(t *testing.T, list *Value, fields map[string]string) {
	t.Helper()
	wanted := migrationExecutionObject(t, list, fields)
	for index := range list.Items {
		if &list.Items[index] == wanted {
			list.Items = append(list.Items[:index], list.Items[index+1:]...)
			return
		}
	}
	t.Fatalf("migration-execution object matching %#v could not be removed", fields)
}

func insertMigrationExecutionColumnBeforeID(t *testing.T, columns *Value, column Value) {
	t.Helper()
	if columns.Type != ValueList || len(columns.Items) == 0 {
		t.Fatalf("migration-execution columns = %#v, want non-empty list", columns)
	}
	insertAt := len(columns.Items) - 1
	columns.Items = append(columns.Items, Value{})
	copy(columns.Items[insertAt+1:], columns.Items[insertAt:])
	columns.Items[insertAt] = column
}

func migrationExecutionA2Column() Value {
	return Object(map[string]Value{
		"name":        String("a2_marker"),
		"nullable":    Boolean(false),
		"primary_key": Boolean(false),
		"type_family": String("boolean"),
	})
}

func migrationExecutionRecord(app, name string) Value {
	return Object(map[string]Value{
		"app":  String(app),
		"name": String(name),
	})
}

func migrationExecutionUnexpectedStep() Value {
	return Object(map[string]Value{
		"app":               String("alpha"),
		"direction":         String("forward"),
		"name":              String("0001_initial"),
		"recorder_outcome":  String("not_started"),
		"schema_outcome":    String("not_started"),
		"status":            String("not_started"),
		"transaction_model": String("none"),
	})
}

func mutateFirstMigrationExecutionLeaf(value *Value) bool {
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
	case ValueInt:
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
	case ValueDecimal:
		changed := "0"
		if *value.Text == changed {
			changed = "1"
		}
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
		return mutateFirstMigrationExecutionLeaf(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateFirstMigrationExecutionLeaf(&value.Items[index]) {
				return true
			}
		}
		value.Items = append(value.Items, String("mutation"))
		return true
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstMigrationExecutionLeaf(&value.Fields[index].Value) {
				return true
			}
		}
		value.Fields = append(value.Fields, NamedValue{Name: "mutation", Value: String("mutation")})
		return true
	default:
		return false
	}
}
