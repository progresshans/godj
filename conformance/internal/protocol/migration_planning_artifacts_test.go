package protocol

import (
	"fmt"
	"reflect"
	"testing"
)

func TestMigrationPlanningPassingArtifactsKeepExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-planning")
	if len(manifest.Contracts) != 12 {
		t.Fatalf("migration-planning manifest has %d contracts, want 12", len(manifest.Contracts))
	}

	successDimensions := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	errorDimensions := []ComparisonDimension{CompareError, CompareDBState, CompareMetrics}
	errorContracts := map[string]struct{}{
		"MIG-007": {},
		"MIG-014": {},
		"MIG-015": {},
		"MIG-016": {},
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+5)
		if contract.ID != wantID {
			t.Fatalf("migration-planning contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		wantPhase := PhaseEvaluation
		if contract.ID == "MIG-015" || contract.ID == "MIG-016" {
			wantPhase = PhaseConstruction
		}
		if contract.Phase != wantPhase {
			t.Fatalf("manifest contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhase)
		}
		if contract.Status != ContractPassing {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		wantDimensions := successDimensions
		if _, isErrorContract := errorContracts[contract.ID]; isErrorContract {
			wantDimensions = errorDimensions
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
		t.Fatalf("Django migration-planning oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("GoDj migration-planning not-implemented baseline does not validate: %v", err)
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

func TestMigrationPlanningDeclaredPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-planning")
	for _, contract := range manifest.Contracts {
		contract := contract
		observation := observationByID(t, &oracle, contract.ID)
		if observation.Result != nil {
			t.Run(contract.ID+" result", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				value := observationByID(t, &actual, contract.ID).Result
				if !mutateFirstMigrationPlanningLeaf(value) {
					t.Fatalf("%s result has no mutable leaf: %#v", contract.ID, value)
				}
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
		if observation.Error != nil {
			t.Run(contract.ID+" error category", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				observationByID(t, &actual, contract.ID).Error.Category = "changed_category"
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
			t.Run(contract.ID+" error code", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				observationByID(t, &actual, contract.ID).Error.Code = "changed_code"
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
		if observation.DBState != nil {
			t.Run(contract.ID+" database state", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				value := observationByID(t, &actual, contract.ID).DBState
				if !mutateFirstMigrationPlanningLeaf(value) {
					t.Fatalf("%s db_state has no mutable leaf: %#v", contract.ID, value)
				}
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
		if observation.Metrics != nil {
			t.Run(contract.ID+" metrics", func(t *testing.T) {
				actual := cloneSuite(t, oracle)
				value := observationByID(t, &actual, contract.ID).Metrics
				if !mutateFirstMigrationPlanningLeaf(value) {
					t.Fatalf("%s metrics has no mutable leaf: %#v", contract.ID, value)
				}
				assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
			})
		}
	}
}

func TestMigrationPlanningSemanticMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-planning")
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *Observation)
	}{
		{
			name:       "plan order",
			contractID: "MIG-005",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationPlanningCaseList(t, observation.Result, 0, "plan")
				plan.Items[0], plan.Items[1] = plan.Items[1], plan.Items[0]
			},
		},
		{
			name:       "plan direction",
			contractID: "MIG-005",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationPlanningCaseList(t, observation.Result, 0, "plan")
				direction := objectField(t, &plan.Items[0], "direction")
				changed := "backward"
				direction.Text = &changed
			},
		},
		{
			name:       "requested target",
			contractID: "MIG-005",
			mutate: func(t *testing.T, observation *Observation) {
				targets := migrationPlanningCaseList(t, observation.Result, 0, "targets")
				name := objectField(t, &targets.Items[0], "name")
				changed := "9999_changed"
				name.Text = &changed
			},
		},
		{
			name:       "applied prefix",
			contractID: "MIG-006",
			mutate: func(t *testing.T, observation *Observation) {
				applied := migrationPlanningCaseList(t, observation.Result, 0, "applied")
				name := objectField(t, &applied.Items[0], "name")
				changed := "0009_changed"
				name.Text = &changed
			},
		},
		{
			name:       "shared dependency deduplication",
			contractID: "MIG-012",
			mutate: func(t *testing.T, observation *Observation) {
				plan := migrationPlanningCaseList(t, observation.Result, 0, "plan")
				plan.Items = append(plan.Items, plan.Items[0])
			},
		},
		{
			name:       "retained branch applied state",
			contractID: "MIG-013",
			mutate: func(t *testing.T, observation *Observation) {
				cases := objectField(t, observation.DBState, "cases")
				before := objectField(t, &cases.Items[0], "before")
				applied := objectField(t, before, "applied_migrations")
				applied.Items = applied.Items[:len(applied.Items)-1]
			},
		},
		{
			name:       "missing target request facts",
			contractID: "MIG-007",
			mutate: func(t *testing.T, observation *Observation) {
				request := objectField(t, observation.Metrics, "request")
				targets := objectField(t, request, "targets")
				name := objectField(t, &targets.Items[0], "name")
				changed := "0001_existing"
				name.Text = &changed
			},
		},
		{
			name:       "missing dependency graph facts",
			contractID: "MIG-015",
			mutate: func(t *testing.T, observation *Observation) {
				graph := objectField(t, observation.Metrics, "graph")
				dependencies := objectField(t, graph, "dependencies")
				parent := objectField(t, &dependencies.Items[0], "parent")
				name := objectField(t, parent, "name")
				changed := "0001_existing"
				name.Text = &changed
			},
		},
		{
			name:       "zero mutation state",
			contractID: "MIG-005",
			mutate: func(t *testing.T, observation *Observation) {
				cases := objectField(t, observation.Metrics, "cases")
				unchanged := objectField(t, &cases.Items[0], "state_unchanged")
				changed := false
				unchanged.Bool = &changed
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			observation := observationByID(t, &actual, test.contractID)
			test.mutate(t, observation)
			assertOnlyContractDiffers(t, profile, manifest, oracle, actual, test.contractID)
		})
	}
}

func TestMigrationPlanningArtifactsRejectOrderPhaseAndProfileMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-planning")
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
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("profile mutation produced a false green")
			}
		})
	}
}

func migrationPlanningCaseList(t *testing.T, root *Value, caseIndex int, field string) *Value {
	t.Helper()
	cases := objectField(t, root, "cases")
	if cases.Type != ValueList || caseIndex < 0 || caseIndex >= len(cases.Items) {
		t.Fatalf("migration-planning cases = %#v, cannot select index %d", cases, caseIndex)
	}
	selected := objectField(t, &cases.Items[caseIndex], field)
	if selected.Type != ValueList {
		t.Fatalf("migration-planning case field %q = %#v, want list", field, selected)
	}
	return selected
}

func mutateFirstMigrationPlanningLeaf(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
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
	case ValueString:
		changed := *value.Text + "-mutated"
		value.Text = &changed
		return true
	case ValueDecimal:
		changed := "999999.5"
		value.Text = &changed
		return true
	case ValueDatetime:
		changed := "2099-01-01T00:00:00Z"
		value.Text = &changed
		return true
	case ValueUUID:
		changed := "00000000-0000-0000-0000-000000000001"
		value.Text = &changed
		return true
	case ValueBytes:
		changed := "bXV0YXRlZA=="
		value.Text = &changed
		return true
	case ValuePK:
		return mutateFirstMigrationPlanningLeaf(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateFirstMigrationPlanningLeaf(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstMigrationPlanningLeaf(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
