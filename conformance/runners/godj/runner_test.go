package godj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	relationauthors "github.com/progresshans/godj/conformance/relationproduct/authors"
	relationblog "github.com/progresshans/godj/conformance/relationproduct/blog"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestRequiredObservedContractIDsUsesHandlerRegistryInManifestOrder(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	identifiers, err := RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(identifiers) != len(manifest.Contracts) {
		t.Fatalf("required observed count = %d, want %d", len(identifiers), len(manifest.Contracts))
	}
	for index, contract := range manifest.Contracts {
		if identifiers[index] != contract.ID {
			t.Fatalf("required observed %d = %q, want %q", index, identifiers[index], contract.ID)
		}
	}
}

func TestRequiredObservedContractIDsRejectsRegistryStatusMismatch(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	registeredLocked := manifest
	registeredLocked.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
	registeredLocked.Contracts[0].Status = protocol.ContractOracleLocked
	if _, err := RequiredObservedContractIDs(registeredLocked); err == nil || !strings.Contains(err.Error(), "registered scenario") {
		t.Fatalf("registered oracle-locked error = %v", err)
	}

	unregisteredPassing := manifest
	unregisteredPassing.Contracts = append([]protocol.Contract(nil), manifest.Contracts...)
	unregisteredPassing.Contracts[0].Scenario = "django.query.cache.unregistered_registry_sentinel"
	if _, err := RequiredObservedContractIDs(unregisteredPassing); err == nil || !strings.Contains(err.Error(), "unregistered scenario") {
		t.Fatalf("unregistered passing error = %v", err)
	}
}

func TestGenerateEmitsPayloadFreeNotImplementedForUnregisteredOracleLocked(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Contracts[0].Scenario = "django.query.cache.unregistered_registry_sentinel"
	manifest.Contracts[0].Status = protocol.ContractOracleLocked
	suite, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatal(err)
	}
	got := suite.Contracts[0]
	if got.ID != manifest.Contracts[0].ID || got.Status != protocol.StatusNotImplemented || got.Phase != manifest.Contracts[0].Phase ||
		got.Result != nil || got.Error != nil || got.DBState != nil || got.Metrics != nil {
		t.Fatalf("unregistered locked observation = %#v", got)
	}
}

func TestRelationProductGeneratesTwoObservedAndTenLockedContracts(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadRelationProductInputs(t)
	required, err := RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(required, []string{"REL-001", "REL-004"}) {
		t.Fatalf("required observed IDs = %#v, want REL-001 and REL-004", required)
	}
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if differences, err := protocol.CompareProduct(profile, manifest, expected, actual, required); err != nil || len(differences) != 0 {
		t.Fatalf("CompareProduct differences=%#v error=%v", differences, err)
	}
	strictDifferences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(strictDifferences) != 10 {
		t.Fatalf("strict relation differences = %d, want 10 not-implemented mismatches", len(strictDifferences))
	}
	wantLocked := []string{"REL-002", "REL-003", "REL-005", "REL-006", "REL-007", "REL-008", "REL-009", "REL-010", "REL-011", "REL-012"}
	for index, difference := range strictDifferences {
		if difference.ContractID != wantLocked[index] || difference.Path != "status" {
			t.Fatalf("strict difference %d = %#v", index, difference)
		}
	}
}

func TestRelationMetadataObservationChangesForEveryOwnedEdgeMutation(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadRelationProductInputs(t)
	base, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		mutate       func(*ir.Schema)
		bindingFails bool
	}{
		{
			name: "source field",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Name = "writer"
			},
		},
		{
			name: "column",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Column = "writer_key"
			},
		},
		{
			name: "target",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Relation.Target.ModelName = "missing"
			},
			bindingFails: true,
		},
		{
			name: "reverse",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Relation.Reverse.Name = "written_posts"
			},
		},
		{
			name: "nullability",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[1].Nullable = true
			},
		},
		{
			name: "delete policy",
			mutate: func(schema *ir.Schema) {
				schema.Models[0].Fields[2].Relation.OnDelete = ir.DeleteProtect
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authors := relationauthors.GoDjRelationSchema()
			blog := relationblog.GoDjRelationSchema()
			test.mutate(&blog)
			binding, err := orm.BindProject(authors, blog)
			if test.bindingFails {
				if err == nil {
					t.Fatal("mutated target unexpectedly bound")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			observation, err := relationMetadataObservation(manifest.Contracts[0], binding)
			if err != nil {
				t.Fatal(err)
			}
			actual := cloneObservationSuite(t, base)
			actual.Contracts[0] = observation
			differences, err := protocol.CompareProduct(profile, manifest, expected, actual, []string{"REL-001", "REL-004"})
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) == 0 {
				t.Fatal("owned relation edge mutation produced a false green")
			}
		})
	}
}

func TestRelationAdapterDoesNotImportDBOrReferenceArtifacts(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("relation_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, forbidden := range []string{"database/sql", "/db", "sqlite", "oracles", "fixtures", "relation-oracle", "not-implemented.json"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("relation adapter contains forbidden product dependency %q", forbidden)
		}
	}
}

type metricsProbeMutator struct {
	calls []string
}

type metricsProbeQueryer struct {
	calls []query.Plan
	err   error
}

func (backend *metricsProbeQueryer) Query(_ context.Context, plan query.Plan) (db.Rows, error) {
	backend.calls = append(backend.calls, plan)
	return nil, backend.err
}

func (mutator *metricsProbeMutator) Insert(context.Context, query.InsertPlan) (int64, error) {
	mutator.calls = append(mutator.calls, "INSERT")
	return 73, nil
}

func (mutator *metricsProbeMutator) Update(context.Context, query.UpdatePlan) (int64, error) {
	mutator.calls = append(mutator.calls, "UPDATE")
	return 4, nil
}

func (mutator *metricsProbeMutator) Delete(context.Context, query.DeletePlan) (int64, error) {
	mutator.calls = append(mutator.calls, "DELETE")
	return 2, nil
}

func TestGenerateMatchesLockedDjangoOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadLockedInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateMatchesLockedWriteMigrationOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadWriteMigrationInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj write/migration suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateMatchesLockedSaveLifecycleOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadSaveLifecycleInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj save lifecycle suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateMatchesLockedQueryCacheOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadQueryCacheInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj query-cache suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateMatchesLockedMigrationPlanningOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationPlanningInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj migration-planning suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateMatchesLockedMigrationRestartOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationRestartInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj migration-restart suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestMigrationRestartRegistryMatchesManifestScenarios(t *testing.T) {
	t.Parallel()

	_, manifest, _ := loadMigrationRestartInputs(t)
	if len(migrationRestartFixtures) != len(manifest.Contracts) {
		t.Fatalf("migration restart registry has %d scenarios, manifest has %d", len(migrationRestartFixtures), len(manifest.Contracts))
	}
	for _, contract := range manifest.Contracts {
		if _, ok := migrationRestartFixtures[contract.Scenario]; !ok {
			t.Fatalf("migration restart scenario %q is not registered", contract.Scenario)
		}
	}
}

func TestGenerateMatchesLockedMigrationStateReconstructionOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationStateReconstructionInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj migration-state-reconstruction suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestMigrationStateReconstructionRegistryMatchesManifestScenarios(t *testing.T) {
	t.Parallel()

	_, manifest, _ := loadMigrationStateReconstructionInputs(t)
	if len(migrationStateReconstructionFixtures) != len(manifest.Contracts) {
		t.Fatalf("migration state reconstruction registry has %d scenarios, manifest has %d", len(migrationStateReconstructionFixtures), len(manifest.Contracts))
	}
	for _, contract := range manifest.Contracts {
		if _, ok := migrationStateReconstructionFixtures[contract.Scenario]; !ok {
			t.Fatalf("migration state reconstruction scenario %q is not registered", contract.Scenario)
		}
	}
}

func TestGenerateMatchesReviewedMigrationLifecycleExpectation(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, expectation := loadMigrationLifecycleInputs(t)
	effective, product, err := protocol.PrepareDeviationExpectation(
		profile,
		manifest,
		oracle,
		expectation,
		migrationLifecycleDeviationPolicyForRunnerTest(),
	)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := Generate(context.Background(), profile, effective)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, effective, product, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj migration-lifecycle suite differs from reviewed product expectation in %d place(s)", len(differences))
	}
}

func TestMigrationLifecycleRegistryMatchesManifestScenarios(t *testing.T) {
	t.Parallel()

	_, manifest, _, _ := loadMigrationLifecycleInputs(t)
	if len(migrationLifecycleFixtures) != len(manifest.Contracts) {
		t.Fatalf("migration lifecycle registry has %d scenarios, manifest has %d", len(migrationLifecycleFixtures), len(manifest.Contracts))
	}
	for _, contract := range manifest.Contracts {
		if _, ok := migrationLifecycleFixtures[contract.Scenario]; !ok {
			t.Fatalf("migration lifecycle scenario %q is not registered", contract.Scenario)
		}
	}
}

func TestGenerateMatchesLockedMigrationProjectCheckOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationProjectCheckInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj migration project-check suite differs from locked decision oracle in %d place(s)", len(differences))
	}
}

func TestMigrationProjectCheckRegistryMatchesManifestScenarios(t *testing.T) {
	t.Parallel()

	_, manifest, _ := loadMigrationProjectCheckInputs(t)
	if len(migrationProjectCheckFixtures) != len(manifest.Contracts) {
		t.Fatalf("migration project-check registry has %d scenarios, manifest has %d", len(migrationProjectCheckFixtures), len(manifest.Contracts))
	}
	for _, contract := range manifest.Contracts {
		if _, ok := migrationProjectCheckFixtures[contract.Scenario]; !ok {
			t.Fatalf("migration project-check scenario %q is not registered", contract.Scenario)
		}
	}
}

func TestGenerateMigrationProjectCheckIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadMigrationProjectCheckInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent migration project-check actual outputs differ")
	}
}

func TestMigrationProjectCheckAdapterUsesActualProductEntryPointsWithoutExpectedReplay(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("migration_project_check_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, forbidden := range []string{
		"conformance/oracles",
		"conformance/fixtures",
		"conformance/projectcheck",
		"LoadObservationSuite",
		"MIG-065",
		"sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("migration project-check adapter contains forbidden expected replay fragment %q", forbidden)
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), "migration_project_check_scenarios.go", contents, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		calls[identifier.Name+"."+selector.Sel.Name]++
		return true
	})
	if calls["productcheck.Run"] != 1 || calls["linked.Run"] != 1 {
		t.Fatalf("migration project-check adapter product calls = global %d/linked %d, want exactly 1 callsite each", calls["productcheck.Run"], calls["linked.Run"])
	}
	if calls["definition.Load"] != 0 {
		t.Fatalf("migration project-check adapter direct definition.Load callsites = %d, want linked report ownership", calls["definition.Load"])
	}
}

func TestMigrationProjectCheckProductInputMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationProjectCheckInputs(t)
	tests := []struct {
		name       string
		contractID string
		mutate     func(*testing.T, *migrationProjectCheckFixture)
	}{
		{
			name:       "descriptor compatibility",
			contractID: "MIG-065",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				root := filepath.Dir(filepath.Dir(filepath.Dir(fixture.cwd)))
				if err := os.WriteFile(filepath.Join(root, "godj.toml"), []byte("format_version = 2\n[project]\npackage = \"./cmd/mysite\"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "catalog membership",
			contractID: "MIG-068",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				if err := os.Remove(filepath.Join(fixture.cwd, "migrations", "a", "0002_fields.godj.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "matching symlink becomes regular source",
			contractID: "MIG-069",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				path := filepath.Join(fixture.cwd, "migrations", "link.godj.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, migrationProjectCheckOneModelDocument(), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "runner protocol version",
			contractID: "MIG-071",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				wire, err := migrationProjectCheckEmptyRunnerWire()
				if err != nil {
					t.Fatal(err)
				}
				fixture.injectedRunnerWire = wire
			},
		},
		{
			name:       "syntax broken build becomes valid",
			contractID: "MIG-072",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				if err := os.WriteFile(filepath.Join(fixture.cwd, "cmd", "broken", "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "definition document",
			contractID: "MIG-073",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				if err := os.WriteFile(filepath.Join(fixture.cwd, "migrations", "broken.godj.json"), migrationProjectCheckOneModelDocument(), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "duplicate runner response member",
			contractID: "MIG-074",
			mutate: func(t *testing.T, fixture *migrationProjectCheckFixture) {
				wire, err := migrationProjectCheckEmptyRunnerWire()
				if err != nil {
					t.Fatal(err)
				}
				fixture.injectedRunnerWire = wire
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			index := -1
			for current, contract := range manifest.Contracts {
				if contract.ID == test.contractID {
					index = current
					break
				}
			}
			if index < 0 {
				t.Fatalf("contract %s is missing", test.contractID)
			}
			factory := migrationProjectCheckFixtures[manifest.Contracts[index].Scenario]
			fixture, err := factory()
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(fixture.cleanupRoot)
			test.mutate(t, &fixture)
			execution, err := runMigrationProjectCheckFixture(context.Background(), test.contractID, fixture)
			if err != nil {
				t.Fatal(err)
			}
			actual := expected
			actual.Contracts = append([]protocol.Observation(nil), expected.Contracts...)
			actual.Contracts[index] = execution.observation
			differences, err := protocol.Compare(profile, manifest, expected, actual)
			if err == nil && len(differences) == 0 {
				t.Fatalf("%s mutation produced a false green", test.name)
			}
		})
	}
}

func TestGenerateMigrationLifecycleIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _, _ := loadMigrationLifecycleInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent migration-lifecycle runs produced different canonical observations")
	}
}

func TestMigrationLifecycleAdapterUsesPublicMigrateWithoutContractPayloadHardcoding(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_lifecycle_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"MIG-",
		"migration-lifecycle-oracle",
		"godj-migration-lifecycle-not-implemented",
		"godj-migration-lifecycle-deviation-expected",
		"switch contractID",
		"ReadFile(",
		"os.ReadFile(",
		"os.Open(",
		"os.OpenFile(",
		"ioutil.ReadFile(",
		"io.ReadAll(",
		"json.NewDecoder(",
		"json.Unmarshal(",
		"protocol.Load",
		"LoadManifest(",
		"LoadObservationSuite(",
		"LoadDeviationExpectation(",
		".ExecutePlan(",
		`INSERT INTO "godj_migrations"`,
		`DELETE FROM "godj_migrations"`,
		`CREATE TABLE "godj_lifecycle_`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("migration-lifecycle adapter contains forbidden hardcoded or legacy execution payload %q", forbidden)
		}
	}
	if got := strings.Count(text, ".Migrate("); got < 6 {
		t.Fatalf("migration-lifecycle adapter public Migrate call sites = %d, want setup and capture coverage", got)
	}
}

func TestMigrationLifecycleTargetMutationsPropagateThroughPublicMigrate(t *testing.T) {
	t.Parallel()

	t.Run("named", func(t *testing.T) {
		base := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.named_forward_target", nil)
		changed := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.named_forward_target", func(fixture *migrationLifecycleFixture) {
			fixture.request = migrationLifecycleNamedRequest(migrationLifecycleA3)
		})

		migrationLifecycleRequireDifferent(t, "named target result plan",
			migrationLifecycleResultField(t, base, "plan"),
			migrationLifecycleResultField(t, changed, "plan"),
		)
		migrationLifecycleRequireDifferent(t, "named target trace steps",
			migrationLifecycleMetricField(t, base, "steps"),
			migrationLifecycleMetricField(t, changed, "steps"),
		)
		migrationLifecycleRequireDifferent(t, "named target database state",
			migrationLifecycleDatabaseSnapshotField(t, base, "after"),
			migrationLifecycleDatabaseSnapshotField(t, changed, "after"),
		)
	})

	t.Run("zero", func(t *testing.T) {
		base := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.zero_target", nil)
		changed := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.zero_target", func(fixture *migrationLifecycleFixture) {
			fixture.request = migrationLifecycleZeroRequest("beta")
		})

		migrationLifecycleRequireDifferent(t, "zero target result plan",
			migrationLifecycleResultField(t, base, "plan"),
			migrationLifecycleResultField(t, changed, "plan"),
		)
		migrationLifecycleRequireDifferent(t, "zero target trace steps",
			migrationLifecycleMetricField(t, base, "steps"),
			migrationLifecycleMetricField(t, changed, "steps"),
		)
		migrationLifecycleRequireDifferent(t, "zero target database state",
			migrationLifecycleDatabaseSnapshotField(t, base, "after"),
			migrationLifecycleDatabaseSnapshotField(t, changed, "after"),
		)
	})
}

func TestMigrationLifecycleFaultMutationMovesFailedStepAndTail(t *testing.T) {
	t.Parallel()

	base := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.middle_forward_failure", nil)
	changed := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.middle_forward_failure", func(fixture *migrationLifecycleFixture) {
		fault := migrationLifecycleA3
		fixture.fault = &fault
	})
	if base.Error == nil || changed.Error == nil {
		t.Fatalf("fault mutation observations must both fail: before=%#v after=%#v", base.Error, changed.Error)
	}
	migrationLifecycleRequireDifferent(t, "failed step",
		migrationLifecycleMetricField(t, base, "failure_step"),
		migrationLifecycleMetricField(t, changed, "failure_step"),
	)
	migrationLifecycleRequireDifferent(t, "failed execution trace",
		migrationLifecycleMetricField(t, base, "steps"),
		migrationLifecycleMetricField(t, changed, "steps"),
	)
	migrationLifecycleRequireDifferent(t, "unstarted tail",
		migrationLifecycleMetricField(t, base, "unstarted_tail_count"),
		migrationLifecycleMetricField(t, changed, "unstarted_tail_count"),
	)
	migrationLifecycleRequireDifferent(t, "durable prefix after fault",
		migrationLifecycleDatabaseSnapshotField(t, base, "after"),
		migrationLifecycleDatabaseSnapshotField(t, changed, "after"),
	)
}

func TestMigrationLifecycleDefinitionMutationsPropagateThroughPublicMigrate(t *testing.T) {
	t.Parallel()

	t.Run("dependency_changes_plan", func(t *testing.T) {
		base := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.fresh_latest", nil)
		changed := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.fresh_latest", func(fixture *migrationLifecycleFixture) {
			definitions := migrationLifecycleDefinitions()
			definition := migrationLifecycleTestDefinition(t, definitions, migrationLifecycleA3)
			definition.Dependencies = []migrations.MigrationKey{migrationLifecycleB1}
			fixture.definitions = definitions
		})

		migrationLifecycleRequireDifferent(t, "dependency-derived plan",
			migrationLifecycleResultField(t, base, "plan"),
			migrationLifecycleResultField(t, changed, "plan"),
		)
		migrationLifecycleRequireDifferent(t, "dependency-derived trace",
			migrationLifecycleMetricField(t, base, "steps"),
			migrationLifecycleMetricField(t, changed, "steps"),
		)
	})

	t.Run("operation_changes_schema", func(t *testing.T) {
		base := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.fresh_latest", nil)
		changed := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.fresh_latest", func(fixture *migrationLifecycleFixture) {
			definitions := migrationLifecycleDefinitions()
			definition := migrationLifecycleTestDefinition(t, definitions, migrationLifecycleA3)
			operation, ok := definition.Operations[0].(migrations.AddField)
			if !ok {
				t.Fatalf("A3 operation = %T, want migrations.AddField", definition.Operations[0])
			}
			operation.Field.Name = "a3_mutated"
			operation.Field.GoName = "A3Mutated"
			operation.Field.Column = "a3_mutated"
			definition.Operations[0] = operation
			fixture.definitions = definitions
		})

		baseAfter := migrationLifecycleDatabaseSnapshotField(t, base, "after")
		changedAfter := migrationLifecycleDatabaseSnapshotField(t, changed, "after")
		migrationLifecycleRequireDifferent(t, "operation-derived database schema",
			migrationPlanningTestObjectField(t, baseAfter, "managed_schema"),
			migrationPlanningTestObjectField(t, changedAfter, "managed_schema"),
		)
		migrationLifecycleRequireDifferent(t, "operation-derived returned state",
			migrationLifecycleResultField(t, base, "returned_state"),
			migrationLifecycleResultField(t, changed, "returned_state"),
		)
	})

	t.Run("default_changes_state", func(t *testing.T) {
		base := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.fresh_latest", nil)
		changed := migrationLifecycleMutationObservation(t, "django.migration.lifecycle.fresh_latest", func(fixture *migrationLifecycleFixture) {
			definitions := migrationLifecycleDefinitions()
			definition := migrationLifecycleTestDefinition(t, definitions, migrationLifecycleA1)
			operation, ok := definition.Operations[0].(migrations.CreateModel)
			if !ok {
				t.Fatalf("A1 operation = %T, want migrations.CreateModel", definition.Operations[0])
			}
			for index := range operation.Model.Fields {
				if operation.Model.Fields[index].Column != "a1_marker" {
					continue
				}
				if operation.Model.Fields[index].Default == nil {
					t.Fatal("A1 marker default is nil")
				}
				operation.Model.Fields[index].Default.String = "a1_mutated"
				definition.Operations[0] = operation
				fixture.definitions = definitions
				return
			}
			t.Fatal("A1 marker field is missing")
		})

		migrationLifecycleRequireDifferent(t, "default-derived returned state",
			migrationLifecycleResultField(t, base, "returned_state"),
			migrationLifecycleResultField(t, changed, "returned_state"),
		)
	})
}

func TestMigrationLifecycleSetupMutationsChangeBeforeRecordsAndTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scenario string
		targets  []migrationLifecycleTarget
	}{
		{
			name:     "prefix",
			scenario: "django.migration.lifecycle.applied_prefix_latest",
			targets:  []migrationLifecycleTarget{{key: migrationLifecycleA2}},
		},
		{
			name:     "legacy",
			scenario: "django.migration.lifecycle.unknown_legacy_tail",
			targets: []migrationLifecycleTarget{
				{key: migrationLifecycleA2},
				{key: migrationLifecycleLegacy},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			base := migrationLifecycleMutationObservation(t, test.scenario, nil)
			changed := migrationLifecycleMutationObservation(t, test.scenario, func(fixture *migrationLifecycleFixture) {
				fixture.setupTargets = append([]migrationLifecycleTarget(nil), test.targets...)
			})

			baseBefore := migrationLifecycleDatabaseSnapshotField(t, base, "before")
			changedBefore := migrationLifecycleDatabaseSnapshotField(t, changed, "before")
			migrationLifecycleRequireDifferent(t, "setup before snapshot", baseBefore, changedBefore)
			migrationLifecycleRequireDifferent(t, "setup migration records",
				migrationPlanningTestObjectField(t, baseBefore, "migration_records"),
				migrationPlanningTestObjectField(t, changedBefore, "migration_records"),
			)
			migrationLifecycleRequireDifferent(t, "setup-derived execution tail",
				migrationLifecycleResultField(t, base, "plan"),
				migrationLifecycleResultField(t, changed, "plan"),
			)
			migrationLifecycleRequireDifferent(t, "setup-derived trace tail",
				migrationLifecycleMetricField(t, base, "steps"),
				migrationLifecycleMetricField(t, changed, "steps"),
			)
		})
	}
}

func migrationLifecycleMutationObservation(
	t *testing.T,
	scenario string,
	mutate func(*migrationLifecycleFixture),
) protocol.Observation {
	t.Helper()
	factory, ok := migrationLifecycleFixtures[scenario]
	if !ok {
		t.Fatalf("migration lifecycle mutation scenario %q is not registered", scenario)
	}
	fixture := factory()
	if mutate != nil {
		mutate(&fixture)
	}
	const arbitraryContractID = "LIFECYCLE-MUTATION-PROBE"
	observation, err := runMigrationLifecycleFixture(context.Background(), arbitraryContractID, fixture)
	if err != nil {
		t.Fatalf("runMigrationLifecycleFixture(%s) error = %v", scenario, err)
	}
	if observation.ID != arbitraryContractID || observation.Status != protocol.StatusObserved {
		t.Fatalf("arbitrary fixture observation identity/status = (%q, %q)", observation.ID, observation.Status)
	}
	return observation
}

func migrationLifecycleTestDefinition(
	t *testing.T,
	definitions []migrations.Migration,
	key migrations.MigrationKey,
) *migrations.Migration {
	t.Helper()
	for index := range definitions {
		if definitions[index].App == key.App && definitions[index].Name == key.Name {
			return &definitions[index]
		}
	}
	t.Fatalf("migration lifecycle definition %s.%s is missing", key.App, key.Name)
	return nil
}

func migrationLifecycleResultField(t *testing.T, observation protocol.Observation, name string) protocol.Value {
	t.Helper()
	if observation.Result == nil {
		t.Fatalf("migration lifecycle result is nil while selecting %q", name)
	}
	return migrationPlanningTestObjectField(t, *observation.Result, name)
}

func migrationLifecycleMetricField(t *testing.T, observation protocol.Observation, name string) protocol.Value {
	t.Helper()
	if observation.Metrics == nil {
		t.Fatalf("migration lifecycle metrics are nil while selecting %q", name)
	}
	return migrationPlanningTestObjectField(t, *observation.Metrics, name)
}

func migrationLifecycleDatabaseSnapshotField(t *testing.T, observation protocol.Observation, name string) protocol.Value {
	t.Helper()
	if observation.DBState == nil {
		t.Fatalf("migration lifecycle database state is nil while selecting %q", name)
	}
	return migrationPlanningTestObjectField(t, *observation.DBState, name)
}

func migrationLifecycleRequireDifferent(t *testing.T, label string, before, after protocol.Value) {
	t.Helper()
	if reflect.DeepEqual(before, after) {
		t.Fatalf("%s did not propagate through the lifecycle adapter: %#v", label, before)
	}
}

func migrationLifecycleDeviationPolicyForRunnerTest() protocol.DeviationPolicy {
	return protocol.DeviationPolicy{
		Decision: "DEV-0002",
		Contracts: []protocol.DeviationContractPolicy{
			{ID: "MIG-052", Changes: []protocol.DeviationChangePolicy{
				{Dimension: protocol.DeviationResult, Path: "plan[0]", Operation: protocol.DeviationReplace},
				{Dimension: protocol.DeviationResult, Path: "plan[1]", Operation: protocol.DeviationReplace},
				{Dimension: protocol.DeviationResult, Path: "plan[2]", Operation: protocol.DeviationReplace},
				{Dimension: protocol.DeviationMetrics, Path: "steps[0]", Operation: protocol.DeviationReplace},
				{Dimension: protocol.DeviationMetrics, Path: "steps[1]", Operation: protocol.DeviationReplace},
				{Dimension: protocol.DeviationMetrics, Path: "steps[2]", Operation: protocol.DeviationReplace},
			}},
		},
	}
}

func TestGenerateSaveLifecycleIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadSaveLifecycleInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent save lifecycle runs produced different canonical observations")
	}
}

func TestGenerateQueryCacheIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadQueryCacheInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent query-cache runs produced different canonical observations")
	}
}

func TestGenerateMigrationPlanningIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadMigrationPlanningInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent migration-planning runs produced different canonical observations")
	}
}

func TestGenerateMigrationRestartIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadMigrationRestartInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent migration-restart runs produced different canonical observations")
	}
}

func TestGenerateMigrationStateReconstructionIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadMigrationStateReconstructionInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent migration-state-reconstruction runs produced different canonical observations")
	}
}

func TestMigrationStateReconstructionFixtureMutationsPropagateThroughPublicAPIs(t *testing.T) {
	t.Parallel()

	const firstProbeID = "STATE-PROBE-NOT-A-MANIFEST-ID"
	const secondProbeID = "STATE-PROBE-SECOND-ARBITRARY-ID"
	base := migrationStateReconstructionFixtures["django.migration.state_reconstruction.first_after"]()
	baseObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, base)
	if err != nil {
		t.Fatal(err)
	}
	if baseObservation.ID != firstProbeID || baseObservation.Status != protocol.StatusObserved {
		t.Fatalf("arbitrary migration state identity/status = (%q, %q)", baseObservation.ID, baseObservation.Status)
	}
	secondIDObservation, err := runMigrationStateReconstructionFixture(context.Background(), secondProbeID, base)
	if err != nil {
		t.Fatal(err)
	}
	baseWithoutID := baseObservation
	baseWithoutID.ID = ""
	secondWithoutID := secondIDObservation
	secondWithoutID.ID = ""
	if !reflect.DeepEqual(baseWithoutID, secondWithoutID) {
		t.Fatal("arbitrary contract ID changed migration state payload")
	}

	changedTarget := migrationStateReconstructionFixtures["django.migration.state_reconstruction.first_after"]()
	changedTarget.targets[0] = migrationStateAlphaMiddle
	changedTargetObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, changedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(baseObservation.Result, changedTargetObservation.Result) {
		t.Fatal("target mutation did not change public Reconstruct result")
	}
	if reflect.DeepEqual(baseObservation.Metrics, changedTargetObservation.Metrics) {
		t.Fatal("target mutation did not change captured request")
	}
	if !reflect.DeepEqual(baseObservation.DBState, changedTargetObservation.DBState) {
		t.Fatal("logical target mutation unexpectedly changed divergent live database")
	}

	baseCross := migrationStateReconstructionFixtures["django.migration.state_reconstruction.cross_app_dependency"]()
	baseCrossObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, baseCross)
	if err != nil {
		t.Fatal(err)
	}
	changedDependency := migrationStateReconstructionFixtures["django.migration.state_reconstruction.cross_app_dependency"]()
	for index := range changedDependency.definitions {
		if changedDependency.definitions[index].Key() == migrationStateBetaRoot {
			changedDependency.definitions[index].Dependencies[0] = migrationStateDeltaRoot
		}
	}
	changedDependencyObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, changedDependency)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(baseCrossObservation.Result, changedDependencyObservation.Result) {
		t.Fatal("dependency mutation did not change public Reconstruct result")
	}
	if reflect.DeepEqual(baseCrossObservation.Metrics, changedDependencyObservation.Metrics) {
		t.Fatal("dependency mutation did not change captured graph facts")
	}

	baseApplied := migrationStateReconstructionFixtures["django.migration.state_reconstruction.unrelated_known_unknown_startup"]()
	baseAppliedObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, baseApplied)
	if err != nil {
		t.Fatal(err)
	}
	changedApplied := migrationStateReconstructionFixtures["django.migration.state_reconstruction.unrelated_known_unknown_startup"]()
	changedApplied.applied[len(changedApplied.applied)-1] = migrations.MigrationKey{App: "retired", Name: "0100_unknown"}
	changedAppliedObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, changedApplied)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(baseAppliedObservation.Result, changedAppliedObservation.Result) {
		t.Fatal("durable applied identity mutation did not propagate through LoadAppliedState")
	}
	if reflect.DeepEqual(baseAppliedObservation.DBState, changedAppliedObservation.DBState) {
		t.Fatal("durable applied identity mutation did not change recorder observation")
	}
	baseAppliedState := migrationPlanningTestObjectField(t, *baseAppliedObservation.Result, "state")
	changedAppliedState := migrationPlanningTestObjectField(t, *changedAppliedObservation.Result, "state")
	if !reflect.DeepEqual(baseAppliedState, changedAppliedState) {
		t.Fatal("unknown applied identity mutation unexpectedly materialized schema state")
	}

	changedLive := migrationStateReconstructionFixtures["django.migration.state_reconstruction.first_after"]()
	changedLive.divergentColumn = "different_wrong_column"
	changedLive.additionalDivergence = []migrationStateDivergentSchema{{table: "godj_state_live_extra", column: "extra_wrong"}}
	changedLiveObservation, err := runMigrationStateReconstructionFixture(context.Background(), firstProbeID, changedLive)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseObservation.Result, changedLiveObservation.Result) {
		t.Fatal("live database mutation changed definition-backed Reconstruct result")
	}
	if reflect.DeepEqual(baseObservation.DBState, changedLiveObservation.DBState) {
		t.Fatal("unexpected managed table or column mutation was omitted from database inventory")
	}
	if !reflect.DeepEqual(baseObservation.Metrics, changedLiveObservation.Metrics) {
		t.Fatal("live database mutation leaked into zero-I/O reconstruction metrics")
	}
}

func TestMigrationStateReconstructionAdapterHasNoContractOrOraclePayloadHardcoding(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_state_reconstruction_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"MIG-",
		"migration-state-reconstruction-oracle",
		"godj-migration-state-reconstruction-not-implemented",
		"switch contractID",
		"if contractID",
		"LoadObservationSuite",
		"LoadManifest",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("migration-state-reconstruction adapter contains forbidden hardcoded payload %q", forbidden)
		}
	}
}

func TestMigrationStateReconstructionCaptureHasNoDirectSQLOrMutationPath(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "migration_state_reconstruction_scenarios.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantedFunctions := map[string]bool{
		"captureMigrationStateReconstruction": false,
		"migrationStateRequest":               false,
	}
	allowedCalls := map[string]map[string]bool{
		"captureMigrationStateReconstruction": {
			"fmt.Errorf": true, "migrationStateRequest": true,
			"migrations.NewStateReconstructor": true, "reconstructor.Reconstruct": true,
		},
		"migrationStateRequest": {
			"append": true, "errors.New": true, "fmt.Errorf": true, "len": true, "make": true,
			"migrations.AfterStateRequest": true, "migrations.AppliedStateRequest": true,
			"migrations.BeforeStateRequest": true, "migrations.EmptyStateRequest": true,
			"migrations.LatestStateRequest": true, "migrations.LoadAppliedState": true,
		},
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, wanted := wantedFunctions[function.Name.Name]; !wanted {
			continue
		}
		wantedFunctions[function.Name.Name] = true
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CallExpr:
				name := migrationStateTestCallName(node.Fun)
				if !allowedCalls[function.Name.Name][name] {
					t.Errorf("%s calls non-allowlisted function %q", function.Name.Name, name)
				}
			case *ast.SelectorExpr:
				switch node.Sel.Name {
				case "Exec", "ExecContext", "Begin", "BeginTx", "BeginMigration", "Prepare", "PrepareContext", "Query", "QueryContext":
					t.Errorf("%s directly calls forbidden database method %s", function.Name.Name, node.Sel.Name)
				}
			case *ast.BasicLit:
				if strings.Contains(strings.ToUpper(node.Value), "PRAGMA") {
					t.Errorf("%s contains direct PRAGMA text", function.Name.Name)
				}
			}
			return true
		})
	}
	for name, found := range wantedFunctions {
		if !found {
			t.Errorf("capture source function %s is missing", name)
		}
	}
	source, err := os.ReadFile("migration_state_reconstruction_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "migrationRestartReadOnlyDataSource(path)") ||
		!strings.Contains(string(source), "appliedReader = &migrationStateObservedReader{delegate: readerBackend}") ||
		!strings.Contains(string(source), "appliedReader.calls != 1") {
		t.Fatal("migration state capture lost its read-only data source or exact-one-reader-call gate")
	}
}

func migrationStateTestCallName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		if receiver, ok := expression.X.(*ast.Ident); ok {
			return receiver.Name + "." + expression.Sel.Name
		}
		return expression.Sel.Name
	default:
		return fmt.Sprintf("%T", expression)
	}
}

func TestStateReconstructorSourceHasNoDatabaseDependencyOrIOSelector(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "..", "migrations", "reconstructor.go")
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range parsed.Imports {
		importPath := strings.Trim(imported.Path.Value, `"`)
		if importPath == "database/sql" || strings.Contains(importPath, "/db") || strings.Contains(importPath, "sqlite") || strings.Contains(importPath, "/backend") {
			t.Errorf("StateReconstructor imports forbidden database dependency %q", importPath)
		}
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "Exec", "ExecContext", "Begin", "BeginTx", "BeginMigration", "Prepare", "PrepareContext", "Query", "QueryContext", "ReadAppliedMigrations":
			t.Errorf("StateReconstructor directly uses forbidden I/O selector %s", selector.Sel.Name)
		}
		return true
	})
}

func TestMigrationRestartFixtureMutationsPropagateThroughPublicRestartAPIs(t *testing.T) {
	t.Parallel()

	const arbitraryContractID = "RESTART-PROBE-NOT-A-MANIFEST-ID"
	baseRecorded := migrationRestartFixtures["django.migration.restart.record_visible_to_fresh_reader"]()
	baseRecordedObservation, err := runMigrationRestartFixture(context.Background(), arbitraryContractID, baseRecorded)
	if err != nil {
		t.Fatal(err)
	}
	if baseRecordedObservation.ID != arbitraryContractID || baseRecordedObservation.Status != protocol.StatusObserved {
		t.Fatalf("arbitrary migration-restart identity/status = (%q, %q)", baseRecordedObservation.ID, baseRecordedObservation.Status)
	}

	changedRecorder := migrationRestartFixtures["django.migration.restart.record_visible_to_fresh_reader"]()
	changedRecorder.recorderSetup = append(changedRecorder.recorderSetup, migrationRestartRecorderTransition{
		key: migrationRestartA1, applied: false,
	})
	changedRecordedObservation, err := runMigrationRestartFixture(context.Background(), arbitraryContractID, changedRecorder)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][2]*protocol.Value{
		"loader result": {baseRecordedObservation.Result, changedRecordedObservation.Result},
		"durable state": {baseRecordedObservation.DBState, changedRecordedObservation.DBState},
		"setup metrics": {baseRecordedObservation.Metrics, changedRecordedObservation.Metrics},
	} {
		if reflect.DeepEqual(values[0], values[1]) {
			t.Fatalf("record/unrecord setup mutation did not propagate through fresh LoadAppliedState to %s", name)
		}
	}

	basePrefix := migrationRestartFixtures["django.migration.restart.applied_prefix_tail"]()
	basePrefixObservation, err := runMigrationRestartFixture(context.Background(), arbitraryContractID, basePrefix)
	if err != nil {
		t.Fatal(err)
	}
	changedTarget := migrationRestartFixtures["django.migration.restart.applied_prefix_tail"]()
	changedTarget.target = migrationRestartA2
	changedTargetObservation, err := runMigrationRestartFixture(context.Background(), arbitraryContractID, changedTarget)
	if err != nil {
		t.Fatal(err)
	}
	basePlan := migrationPlanningTestObjectField(t, *basePrefixObservation.Result, "plan")
	targetPlan := migrationPlanningTestObjectField(t, *changedTargetObservation.Result, "plan")
	if reflect.DeepEqual(basePlan, targetPlan) {
		t.Fatal("target mutation did not change the public Planner.Plan result")
	}

	baseUnknown := migrationRestartFixtures["django.migration.restart.unknown_legacy_record"]()
	baseUnknownObservation, err := runMigrationRestartFixture(context.Background(), arbitraryContractID, baseUnknown)
	if err != nil {
		t.Fatal(err)
	}
	changedGraph := migrationRestartFixtures["django.migration.restart.unknown_legacy_record"]()
	changedGraph.definitions[2].Dependencies[0] = migrationRestartA1
	changedGraphObservation, err := runMigrationRestartFixture(context.Background(), arbitraryContractID, changedGraph)
	if err != nil {
		t.Fatal(err)
	}
	baseUnknownPlan := migrationPlanningTestObjectField(t, *baseUnknownObservation.Result, "plan")
	changedUnknownPlan := migrationPlanningTestObjectField(t, *changedGraphObservation.Result, "plan")
	if reflect.DeepEqual(baseUnknownPlan, changedUnknownPlan) {
		t.Fatal("dependency mutation did not change the public CheckHistory/Plan result")
	}
	baseGraph := migrationPlanningTestObjectField(t, *baseUnknownObservation.Metrics, "graph")
	changedGraphValue := migrationPlanningTestObjectField(t, *changedGraphObservation.Metrics, "graph")
	if reflect.DeepEqual(baseGraph, changedGraphValue) {
		t.Fatal("dependency mutation did not change captured graph facts")
	}
}

func TestMigrationRestartAdapterHasNoContractOrOraclePayloadHardcoding(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_restart_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"MIG-",
		"migration-restart-oracle",
		"godj-migration-restart-not-implemented",
		"switch contractID",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("migration-restart adapter contains forbidden hardcoded payload %q", forbidden)
		}
	}
}

func TestMigrationExecutionScenariosAreDeterministic(t *testing.T) {
	t.Parallel()

	for scenario, factory := range migrationExecutionFixtures {
		scenario, factory := scenario, factory
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			first, err := runMigrationExecutionFixture(context.Background(), "PROBE-001", factory())
			if err != nil {
				t.Fatalf("runMigrationExecutionFixture(first) error = %v", err)
			}
			second, err := runMigrationExecutionFixture(context.Background(), "PROBE-001", factory())
			if err != nil {
				t.Fatalf("runMigrationExecutionFixture(second) error = %v", err)
			}
			firstJSON, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			secondJSON, err := json.Marshal(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstJSON, secondJSON) {
				t.Fatal("independent migration-execution runs produced different JSON observations")
			}
		})
	}
}

func TestMigrationExecutionFixtureMutationsPropagateWithoutContractPayloads(t *testing.T) {
	t.Parallel()

	base := migrationExecutionFixtures["django.migration.execute.linear_forward"]()
	baseObservation, err := runMigrationExecutionFixture(context.Background(), "PROBE-002", base)
	if err != nil {
		t.Fatal(err)
	}

	changed := migrationExecutionFixtures["django.migration.execute.linear_forward"]()
	changed.plan = changed.plan[:1]
	changedObservation, err := runMigrationExecutionFixture(context.Background(), "PROBE-002", changed)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][2]*protocol.Value{
		"result":   {baseObservation.Result, changedObservation.Result},
		"db_state": {baseObservation.DBState, changedObservation.DBState},
		"metrics":  {baseObservation.Metrics, changedObservation.Metrics},
	} {
		if reflect.DeepEqual(values[0], values[1]) {
			t.Fatalf("plan fixture mutation did not propagate to %s", name)
		}
	}
}

func TestMigrationExecutionAdapterHasNoContractOrOraclePayloadHardcoding(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_execution_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"MIG-",
		"migration-execution-oracle",
		"godj-migration-execution-not-implemented",
		"godj-migration-execution-deviation-expected",
		"switch contractID",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("migration-execution adapter contains forbidden hardcoded payload %q", forbidden)
		}
	}
}

func TestMigrationExecutionUnknownScenarioFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := migrationExecutionScenario(context.Background(), protocol.Contract{
		ID:       "PROBE-003",
		Scenario: "django.migration.execute.unknown_sentinel",
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported migration execution scenario "django.migration.execute.unknown_sentinel"`) {
		t.Fatalf("migrationExecutionScenario() error = %v", err)
	}
}

func TestMigrationExecutionTraceRejectsUnboundExtraAndNonterminalTransactions(t *testing.T) {
	t.Parallel()

	plan := migrationExecutionForwardPlan(executionA1)
	tests := []struct {
		name        string
		transaction *migrationExecutionTransaction
		want        string
	}{
		{
			name:        "unbound begin",
			transaction: &migrationExecutionTransaction{},
			want:        "began without binding",
		},
		{
			name: "unplanned step",
			transaction: &migrationExecutionTransaction{
				key:               executionA2,
				direction:         migrations.DirectionForward,
				operationStarted:  true,
				committed:         true,
				recorderSucceeded: true,
			},
			want: "unplanned migration step",
		},
		{
			name: "nonterminal step",
			transaction: &migrationExecutionTransaction{
				key:               executionA1,
				direction:         migrations.DirectionForward,
				operationStarted:  true,
				recorderSucceeded: true,
			},
			want: "terminal state",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			trace := &migrationExecutionTrace{transactions: []*migrationExecutionTransaction{test.transaction}}
			err := trace.validate(plan, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestMigrationPlanningFixtureMutationsPropagateWithoutContractIDPayloads(t *testing.T) {
	t.Parallel()

	const arbitraryContractID = "PROBE-001"
	base := migrationPlanningFixtures["django.migration.plan.linear_forward"]()
	baseObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, base)
	if err != nil {
		t.Fatal(err)
	}
	if baseObservation.ID != arbitraryContractID || baseObservation.Status != protocol.StatusObserved {
		t.Fatalf("arbitrary fixture observation identity/status = (%q, %q)", baseObservation.ID, baseObservation.Status)
	}
	basePlan := migrationPlanningResultPlan(t, baseObservation)

	changed := migrationPlanningFixtures["django.migration.plan.linear_forward"]()
	changed.cases[0].name = "fixture_mutation_sentinel"
	changedObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, changed)
	if err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][2]*protocol.Value{
		"result":   {baseObservation.Result, changedObservation.Result},
		"db_state": {baseObservation.DBState, changedObservation.DBState},
		"metrics":  {baseObservation.Metrics, changedObservation.Metrics},
	} {
		if reflect.DeepEqual(values[0], values[1]) {
			t.Fatalf("case-name fixture mutation did not propagate to %s", name)
		}
	}

	changedTarget := migrationPlanningFixtures["django.migration.plan.linear_forward"]()
	changedTarget.cases[0].targets[0] = planningNamedTarget(planningA2)
	targetObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, changedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(basePlan, migrationPlanningResultPlan(t, targetObservation)) {
		t.Fatal("target fixture mutation did not change the public planner plan")
	}

	changedApplied := migrationPlanningFixtures["django.migration.plan.linear_forward"]()
	changedApplied.cases[0].applied = []migrations.MigrationKey{planningA1}
	appliedObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, changedApplied)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(basePlan, migrationPlanningResultPlan(t, appliedObservation)) ||
		reflect.DeepEqual(baseObservation.DBState, appliedObservation.DBState) {
		t.Fatal("applied-state fixture mutation did not change the public planner plan and logical database state")
	}

	changedDependency := migrationPlanningFixtures["django.migration.plan.linear_forward"]()
	changedDependency.dependencies[1].parent = planningA1
	dependencyObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, changedDependency)
	if err != nil {
		t.Fatal(err)
	}
	dependencyPlan := migrationPlanningResultPlan(t, dependencyObservation)
	if reflect.DeepEqual(basePlan, dependencyPlan) {
		t.Fatal("dependency fixture mutation did not change the public planner plan")
	}
	wantDependencyPlan := protocol.List(
		protocol.Object(map[string]protocol.Value{
			"app":       protocol.String(planningA1.App),
			"direction": protocol.String(string(migrations.DirectionForward)),
			"name":      protocol.String(planningA1.Name),
		}),
		protocol.Object(map[string]protocol.Value{
			"app":       protocol.String(planningA3.App),
			"direction": protocol.String(string(migrations.DirectionForward)),
			"name":      protocol.String(planningA3.Name),
		}),
	)
	if !reflect.DeepEqual(dependencyPlan, wantDependencyPlan) {
		t.Fatalf("rewired A3 dependency plan = %#v, want A1 then A3 with A2 omitted %#v", dependencyPlan, wantDependencyPlan)
	}

	baseErrorFixture := migrationPlanningFixtures["django.migration.plan.missing_dependency"]()
	baseErrorObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, baseErrorFixture)
	if err != nil {
		t.Fatal(err)
	}
	changedErrorFixture := migrationPlanningFixtures["django.migration.plan.missing_dependency"]()
	changedErrorFixture.dependencies[0].parent = planningA2
	changedErrorObservation, err := runMigrationPlanningFixture(context.Background(), arbitraryContractID, changedErrorFixture)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(baseErrorObservation.Metrics, changedErrorObservation.Metrics) {
		t.Fatal("graph fixture mutation did not propagate to error facts")
	}
	if baseErrorObservation.Error == nil || changedErrorObservation.Error == nil ||
		baseErrorObservation.Error.Code != string(migrations.CodeDependencyNotFound) ||
		changedErrorObservation.Error.Code != string(migrations.CodeDependencyCycle) {
		t.Fatalf("missing-parent to self-cycle mutation did not change actual observed error: before=%#v after=%#v", baseErrorObservation.Error, changedErrorObservation.Error)
	}
}

func migrationPlanningResultPlan(t *testing.T, observation protocol.Observation) protocol.Value {
	t.Helper()
	if observation.Result == nil || observation.Result.Type != protocol.ValueObject {
		t.Fatalf("migration-planning result = %#v, want object", observation.Result)
	}
	cases := migrationPlanningTestObjectField(t, *observation.Result, "cases")
	if cases.Type != protocol.ValueList || len(cases.Items) == 0 {
		t.Fatalf("migration-planning cases = %#v, want non-empty list", cases)
	}
	plan := migrationPlanningTestObjectField(t, cases.Items[0], "plan")
	if plan.Type != protocol.ValueList {
		t.Fatalf("migration-planning plan = %#v, want list", plan)
	}
	return plan
}

func migrationPlanningTestObjectField(t *testing.T, object protocol.Value, name string) protocol.Value {
	t.Helper()
	if object.Type != protocol.ValueObject {
		t.Fatalf("cannot select field %q from %#v", name, object)
	}
	for _, field := range object.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("field %q is missing from %#v", name, object)
	return protocol.Value{}
}

func TestMigrationPlanningAdapterHasNoContractOrOraclePayloadHardcoding(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_planning_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"MIG-",
		"migration-planning-oracle",
		"godj-migration-planning-not-implemented",
		"switch contractID",
		"database/sql",
		"db/sqlite",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("migration-planning adapter contains forbidden hardcoded/runtime dependency %q", forbidden)
		}
	}
}

func TestMigrationPlanningMetricsAreDerivedFromCaptureStateAndCounters(t *testing.T) {
	t.Parallel()

	before := planningDatabaseState(nil)
	after := planningDatabaseState([]migrations.MigrationKey{planningA1})
	capture := migrationPlanningCapture{
		before:              before,
		after:               after,
		ddlStatements:       2,
		writeStatements:     3,
		nonSelectStatements: 5,
	}
	got := planningMutationMetrics(capture, map[string]protocol.Value{
		"fixture_fact": protocol.String("sentinel"),
	})
	want := protocol.Object(map[string]protocol.Value{
		"ddl_statement_count":        protocol.Integer("2"),
		"fixture_fact":               protocol.String("sentinel"),
		"non_select_statement_count": protocol.Integer("5"),
		"state_unchanged":            protocol.Boolean(false),
		"write_statement_count":      protocol.Integer("3"),
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("planning metrics = %#v, want capture-derived %#v", got, want)
	}
}

func TestMigrationPlanningLogicalStateAndGraphFactsAreCanonical(t *testing.T) {
	t.Parallel()

	gotState := planningDatabaseState([]migrations.MigrationKey{planningB1, planningA1})
	wantState := protocol.Object(map[string]protocol.Value{
		"applied_migrations": protocol.List(
			planningKeyValue(planningA1),
			planningKeyValue(planningB1),
		),
		"managed_schema_inventory": protocol.List(),
		"recorder_present":         protocol.Boolean(true),
	})
	if !reflect.DeepEqual(gotState, wantState) {
		t.Fatalf("unsorted applied input state = %#v, want canonical %#v", gotState, wantState)
	}

	orderedFixture := migrationPlanningFixtures["django.migration.plan.dependency_cycle"]()
	reversedFixture := migrationPlanningFixtures["django.migration.plan.dependency_cycle"]()
	reversedFixture.nodes[0], reversedFixture.nodes[1] = reversedFixture.nodes[1], reversedFixture.nodes[0]
	reversedFixture.dependencies[0], reversedFixture.dependencies[1] = reversedFixture.dependencies[1], reversedFixture.dependencies[0]
	ordered, err := runMigrationPlanningFixture(context.Background(), "PROBE-002", orderedFixture)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := runMigrationPlanningFixture(context.Background(), "PROBE-002", reversedFixture)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ordered, reversed) {
		t.Fatalf("reversed graph fixture changed canonical observation:\nordered=%#v\nreversed=%#v", ordered, reversed)
	}
}

func TestGenerateMatchesLockedMigrationDefinitionSourceOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationDefinitionSourceInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj migration-definition-source suite differs from locked oracle in %d place(s)", len(differences))
	}
}

func TestMigrationDefinitionSourceRegistryMatchesManifestScenarios(t *testing.T) {
	t.Parallel()

	_, manifest, _ := loadMigrationDefinitionSourceInputs(t)
	if len(migrationDefinitionSourceFixtures) != len(manifest.Contracts) {
		t.Fatalf(
			"migration definition source registry has %d scenarios, manifest has %d",
			len(migrationDefinitionSourceFixtures),
			len(manifest.Contracts),
		)
	}
	for _, contract := range manifest.Contracts {
		if _, ok := migrationDefinitionSourceFixtures[contract.Scenario]; !ok {
			t.Fatalf("migration definition source scenario %q is not registered", contract.Scenario)
		}
	}
}

func TestMigrationDefinitionSourceFixtureUsesArbitraryContractIdentity(t *testing.T) {
	t.Parallel()

	fixture := migrationDefinitionSourceFixtures["godj.migration.definition_source.canonical_batch"]()
	const arbitraryContractID = "DEFINITION-SOURCE-PROBE-NOT-A-MANIFEST-ID"
	observation, err := runMigrationDefinitionSourceFixture(context.Background(), arbitraryContractID, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ID != arbitraryContractID || observation.Status != protocol.StatusObserved {
		t.Fatalf(
			"arbitrary migration definition source identity/status = (%q, %q)",
			observation.ID,
			observation.Status,
		)
	}
	if observation.Result == nil || observation.Error != nil {
		t.Fatalf("arbitrary migration definition source observation = %#v, want successful actual result", observation)
	}
}

func TestMigrationDefinitionSourceMutationsProduceProtocolDifferences(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadMigrationDefinitionSourceInputs(t)

	tests := []struct {
		name                    string
		scenario                string
		wantComparisonRejection bool
		mutate                  func(*migrationDefinitionSourceFixture)
	}{
		{
			name:     "source",
			scenario: "godj.migration.definition_source.canonical_batch",
			mutate: func(fixture *migrationDefinitionSourceFixture) {
				index := migrationDefinitionFixtureSourceIndex(t, fixture, "opaque-z-root")
				fixture.sources[index].SourceID = "opaque-y-root-mutated"
			},
		},
		{
			name:                    "compatibility header",
			scenario:                "godj.migration.definition_source.canonical_batch",
			wantComparisonRejection: true,
			mutate: func(fixture *migrationDefinitionSourceFixture) {
				migrationDefinitionMutateFixtureDocument(t, fixture, "opaque-z-root", func(document map[string]any) {
					document["compatibility"].(map[string]any)["definition_format"] = float64(2)
				})
			},
		},
		{
			name:     "operation",
			scenario: "godj.migration.definition_source.canonical_batch",
			mutate: func(fixture *migrationDefinitionSourceFixture) {
				migrationDefinitionMutateFixtureDocument(t, fixture, "opaque-z-root", func(document map[string]any) {
					model := document["migration"].(map[string]any)["operations"].([]any)[0].(map[string]any)["model"].(map[string]any)
					model["fields"].([]any)[1].(map[string]any)["default"].(map[string]any)["string"] = "mutated-title"
				})
			},
		},
		{
			name:     "graph",
			scenario: "godj.migration.definition_source.canonical_batch",
			mutate: func(fixture *migrationDefinitionSourceFixture) {
				migrationDefinitionMutateFixtureDocument(t, fixture, "opaque-a-tail", func(document map[string]any) {
					document["migration"].(map[string]any)["dependencies"] = []any{}
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contractIndex := -1
			for index, contract := range manifest.Contracts {
				if contract.Scenario == test.scenario {
					contractIndex = index
					break
				}
			}
			if contractIndex < 0 {
				t.Fatalf("migration definition source scenario %q is missing from manifest", test.scenario)
			}
			contract := manifest.Contracts[contractIndex]
			fixture := migrationDefinitionSourceFixtures[test.scenario]()
			test.mutate(&fixture)
			changed, err := runMigrationDefinitionSourceFixture(context.Background(), contract.ID, fixture)
			if err != nil {
				t.Fatalf("run mutated migration definition source fixture: %v", err)
			}
			actual := expected
			actual.Contracts = append([]protocol.Observation(nil), expected.Contracts...)
			actual.Contracts[contractIndex] = changed
			differences, err := protocol.Compare(profile, manifest, expected, actual)
			if test.wantComparisonRejection {
				if err == nil {
					t.Fatalf("%s mutation changed a success payload into an error but protocol.Compare accepted it with differences %#v", test.name, differences)
				}
				return
			}
			if err != nil {
				t.Fatalf("Compare() mutation error = %v", err)
			}
			if len(differences) == 0 {
				t.Fatalf("%s mutation did not change the actual product observation", test.name)
			}
		})
	}
}

func TestMigrationDefinitionSourceAdapterHasNoOracleOrTestCandidateShortcut(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_definition_source_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"MIG-",
		"sha256:",
		"migration-definition-source-oracle",
		"godj-migration-definition-source-not-implemented",
		"conformance/definitionload",
		"candidate_test",
		"switch contractID",
		"if contractID",
		"protocol.Compare",
		"protocol.LoadManifest",
		"protocol.LoadObservationSuite",
		"json.Unmarshal",
		"json.NewDecoder",
		"migrations.NewPlanner",
		"os.ReadFile",
		"os.Open(",
		"os.OpenFile",
		"ioutil.ReadFile",
		"io.ReadAll",
		"handoffCalls++",
		"handoffCalls := 1",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("migration definition source adapter contains forbidden shortcut %q", forbidden)
		}
	}
	if got := strings.Count(text, "definition.Load("); got < 5 {
		t.Fatalf("migration definition source adapter Load call sites = %d, want actual product loading paths", got)
	}
	if got := strings.Count(text, ".Migrate("); got != 1 {
		t.Fatalf("migration definition source adapter Set.Migrate call sites = %d, want exactly one", got)
	}

	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "migration_definition_source_scenarios.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenCalls := map[string]bool{
		"LoadManifest":         true,
		"LoadObservationSuite": true,
		"ReadFile":             true,
		"ReadAll":              true,
		"Unmarshal":            true,
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !forbiddenCalls[selector.Sel.Name] {
			return true
		}
		t.Errorf("migration definition source adapter contains forbidden file/artifact decode call %s", selector.Sel.Name)
		return true
	})
}

func TestMigrationDefinitionLifecycleObservationRequiresActualSetMigrateHandoff(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("migration_definition_source_scenarios.go")
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "migration_definition_source_scenarios.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}

	foundFunction := false
	setMigrateCalls := 0
	returnedStateCaptures := 0
	probeCounterReads := 0
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "migrationDefinitionLifecycleObservation" {
			continue
		}
		foundFunction = true
		ast.Inspect(function.Body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok || len(assignment.Rhs) != 1 {
				return true
			}
			if selector, ok := assignment.Rhs[0].(*ast.SelectorExpr); ok {
				receiver, receiverOK := selector.X.(*ast.Ident)
				if receiverOK && receiver.Name == "probe" && selector.Sel.Name == "sessionOpenCalls" &&
					len(assignment.Lhs) == 1 {
					left, leftOK := assignment.Lhs[0].(*ast.Ident)
					if leftOK && left.Name == "handoffCalls" {
						probeCounterReads++
					}
				}
				return true
			}
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Migrate" {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "set" {
				return true
			}
			setMigrateCalls++
			if len(assignment.Lhs) >= 1 {
				left, ok := assignment.Lhs[0].(*ast.Ident)
				if ok && left.Name == "returnedState" {
					returnedStateCaptures++
				}
			}
			if len(call.Args) != 3 {
				t.Errorf("Set.Migrate arguments = %d, want context, instrumented executor, request", len(call.Args))
				return true
			}
			executor, ok := call.Args[1].(*ast.CompositeLit)
			if !ok || len(executor.Elts) != 1 {
				t.Error("Set.Migrate does not receive one explicit instrumented Executor backend")
				return true
			}
			binding, ok := executor.Elts[0].(*ast.KeyValueExpr)
			if !ok {
				t.Error("Set.Migrate Executor backend is not an explicit keyed binding")
				return true
			}
			key, keyOK := binding.Key.(*ast.Ident)
			value, valueOK := binding.Value.(*ast.Ident)
			if !keyOK || !valueOK || key.Name != "Backend" || value.Name != "probe" {
				t.Errorf("Set.Migrate Executor binding = %#v, want Backend: probe", binding)
			}
			return true
		})
	}
	if !foundFunction {
		t.Fatal("migrationDefinitionLifecycleObservation function is missing")
	}
	if setMigrateCalls != 1 || returnedStateCaptures != 1 {
		t.Fatalf(
			"actual Set.Migrate handoff/captured state = (%d, %d), want exactly (1, 1)",
			setMigrateCalls,
			returnedStateCaptures,
		)
	}
	if probeCounterReads != 1 {
		t.Fatalf("handoff_calls instrumented session-open reads = %d, want exactly one", probeCounterReads)
	}
	text := string(source)
	if strings.Count(text, "migrationDefinitionTransitionValues(probe.transitions)") != 1 ||
		strings.Count(text, "len(probe.transitions)") != 1 ||
		strings.Count(text, "migrationDefinitionProjectStateValue(returnedState)") != 1 {
		t.Fatal("lifecycle result/metrics do not flow from instrumented transitions and actual returned state")
	}
}

func TestMigrationDefinitionSourceUnknownScenarioFailsClosed(t *testing.T) {
	t.Parallel()

	_, err := migrationDefinitionSourceScenario(context.Background(), protocol.Contract{
		ID:       "DEFINITION-SOURCE-UNKNOWN-PROBE",
		Scenario: "godj.migration.definition_source.unknown_sentinel",
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported migration definition source scenario "godj.migration.definition_source.unknown_sentinel"`) {
		t.Fatalf("migrationDefinitionSourceScenario() error = %v", err)
	}
}

func migrationDefinitionFixtureSourceIndex(
	t *testing.T,
	fixture *migrationDefinitionSourceFixture,
	sourceID string,
) int {
	t.Helper()
	for index := range fixture.sources {
		if fixture.sources[index].SourceID == sourceID {
			return index
		}
	}
	t.Fatalf("migration definition fixture source %q is missing", sourceID)
	return -1
}

func migrationDefinitionMutateFixtureDocument(
	t *testing.T,
	fixture *migrationDefinitionSourceFixture,
	sourceID string,
	mutate func(map[string]any),
) {
	t.Helper()
	index := migrationDefinitionFixtureSourceIndex(t, fixture, sourceID)
	var document map[string]any
	if err := json.Unmarshal(fixture.sources[index].Document, &document); err != nil {
		t.Fatalf("decode migration definition fixture %q: %v", sourceID, err)
	}
	mutate(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode migration definition fixture %q: %v", sourceID, err)
	}
	fixture.sources[index].Document = encoded
}

func TestQueryCacheMetricsAreDerivedFromCaptureWindowQueryerCalls(t *testing.T) {
	t.Parallel()

	probeErr := errors.New("probe query failed")
	delegate := &metricsProbeQueryer{err: probeErr}
	recorder := &queryCallRecorder{}
	backend := observedQueryer(delegate, recorder)
	before := query.NewPlan("before_checkpoint", nil)
	first := query.NewPlan("first_in_window", nil)
	second := query.NewPlan("second_in_window", nil)
	if _, err := backend.Query(context.Background(), before); !errors.Is(err, probeErr) {
		t.Fatalf("pre-window Query() error = %v, want probe error", err)
	}
	checkpoint := recorder.checkpoint()
	if _, err := backend.Query(context.Background(), first); !errors.Is(err, probeErr) {
		t.Fatalf("first Query() error = %v, want probe error", err)
	}
	if _, err := backend.Query(context.Background(), second); !errors.Is(err, probeErr) {
		t.Fatalf("second Query() error = %v, want probe error", err)
	}

	got, err := queryCacheMetricStep(recorder, checkpoint, "sentinel_window")
	if err != nil {
		t.Fatalf("queryCacheMetricStep() error = %v", err)
	}
	want := protocol.Object(map[string]protocol.Value{
		"name":        protocol.String("sentinel_window"),
		"query_count": protocol.Integer("2"),
		"statement_kinds": protocol.List(
			protocol.String("SELECT"),
			protocol.String("SELECT"),
		),
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capture metrics = %#v, want call-derived %#v", got, want)
	}
	if gotTables := []string{delegate.calls[0].Table(), delegate.calls[1].Table(), delegate.calls[2].Table()}; !reflect.DeepEqual(gotTables, []string{"before_checkpoint", "first_in_window", "second_in_window"}) {
		t.Fatalf("delegate plans = %#v", gotTables)
	}
}

func TestQueryCacheCaptureUsesOperationValueAndStructuredErrorFields(t *testing.T) {
	t.Parallel()

	recorder := &queryCallRecorder{}
	resultSteps, metricSteps := newQueryCacheSteps()
	if err := captureQueryCacheStep(recorder, &resultSteps, &metricSteps, "value_probe", func() (protocol.Value, error) {
		return protocol.String("live-operation-sentinel"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(resultSteps) != 1 || resultSteps[0].Fields[1].Name != "value" ||
		resultSteps[0].Fields[1].Value.Text == nil || *resultSteps[0].Fields[1].Value.Text != "live-operation-sentinel" {
		t.Fatalf("captured operation value = %#v", resultSteps)
	}

	structured := &query.Error{Category: "sentinel_category", Code: "sentinel_code", Detail: "not a contract"}
	if err := captureQueryCacheErrorStep(recorder, &resultSteps, &metricSteps, "error_probe", func() error {
		return fmt.Errorf("wrapped: %w", structured)
	}); err != nil {
		t.Fatal(err)
	}
	errorObject := resultSteps[1].Fields[0].Value
	if errorObject.Type != protocol.ValueObject || len(errorObject.Fields) != 2 {
		t.Fatalf("captured error object = %#v", errorObject)
	}
	if errorObject.Fields[0].Name != "category" || errorObject.Fields[0].Value.Text == nil || *errorObject.Fields[0].Value.Text != "sentinel_category" {
		t.Fatalf("captured error category = %#v", errorObject.Fields)
	}
	if errorObject.Fields[1].Name != "code" || errorObject.Fields[1].Value.Text == nil || *errorObject.Fields[1].Value.Text != "sentinel_code" {
		t.Fatalf("captured error code = %#v", errorObject.Fields)
	}
}

func TestSaveMetricsAreDerivedFromObservedMutatorCalls(t *testing.T) {
	t.Parallel()

	delegate := &metricsProbeMutator{}
	recorder := &statementRecorder{}
	mutator := observedMutator(delegate, recorder)
	ctx := context.Background()

	insertID, err := mutator.Insert(ctx, query.NewInsertPlan("probe", nil))
	if err != nil || insertID != 73 {
		t.Fatalf("observed Insert() = (%d, %v), want (73, nil)", insertID, err)
	}
	updated, err := mutator.Update(ctx, query.NewUpdatePlan("probe", nil, query.FieldRef{}, query.Value{}))
	if err != nil || updated != 4 {
		t.Fatalf("observed Update() = (%d, %v), want (4, nil)", updated, err)
	}
	deleted, err := mutator.Delete(ctx, query.NewDeletePlan("probe", query.FieldRef{}, query.Value{}))
	if err != nil || deleted != 2 {
		t.Fatalf("observed Delete() = (%d, %v), want (2, nil)", deleted, err)
	}
	updated, err = mutator.Update(ctx, query.NewUpdatePlan("probe", nil, query.FieldRef{}, query.Value{}))
	if err != nil || updated != 4 {
		t.Fatalf("second observed Update() = (%d, %v), want (4, nil)", updated, err)
	}

	wantCalls := []string{"INSERT", "UPDATE", "DELETE", "UPDATE"}
	if !reflect.DeepEqual(delegate.calls, wantCalls) {
		t.Fatalf("delegate calls = %#v, want %#v", delegate.calls, wantCalls)
	}
	wantMetrics := protocol.Object(map[string]protocol.Value{
		"query_count": protocol.Integer("4"),
		"statement_kinds": protocol.List(
			protocol.String("INSERT"),
			protocol.String("UPDATE"),
			protocol.String("DELETE"),
			protocol.String("UPDATE"),
		),
	})
	if got := saveMetrics(recorder); !reflect.DeepEqual(got, wantMetrics) {
		t.Fatalf("save metrics = %#v, want metrics derived from calls %#v", got, wantMetrics)
	}

	emptyMetrics := protocol.Object(map[string]protocol.Value{
		"query_count":     protocol.Integer("0"),
		"statement_kinds": protocol.List(),
	})
	if got := saveMetrics(&statementRecorder{}); !reflect.DeepEqual(got, emptyMetrics) {
		t.Fatalf("independent empty recorder metrics = %#v, want %#v", got, emptyMetrics)
	}
}

func TestSaveResultObservationUsesRecorderForArbitraryContract(t *testing.T) {
	t.Parallel()

	const contractID = "MUTATION-PROBE-NOT-A-MANIFEST-CONTRACT"
	observation, err := withEmptyArticleDatabase(context.Background(), contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		delegate := &metricsProbeMutator{}
		recorder := &statementRecorder{}
		mutator := observedMutator(delegate, recorder)
		if _, err := mutator.Delete(ctx, query.NewDeletePlan("probe", query.FieldRef{}, query.Value{})); err != nil {
			return protocol.Observation{}, err
		}
		if _, err := mutator.Insert(ctx, query.NewInsertPlan("probe", nil)); err != nil {
			return protocol.Observation{}, err
		}
		if _, err := mutator.Delete(ctx, query.NewDeletePlan("probe", query.FieldRef{}, query.Value{})); err != nil {
			return protocol.Observation{}, err
		}
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, protocol.Null(), recorder)
	})
	if err != nil {
		t.Fatalf("arbitrary save result observation: %v", err)
	}
	if observation.ID != contractID {
		t.Fatalf("observation ID = %q, want %q", observation.ID, contractID)
	}
	wantMetrics := protocol.Object(map[string]protocol.Value{
		"query_count": protocol.Integer("3"),
		"statement_kinds": protocol.List(
			protocol.String("DELETE"),
			protocol.String("INSERT"),
			protocol.String("DELETE"),
		),
	})
	if observation.Metrics == nil || !reflect.DeepEqual(*observation.Metrics, wantMetrics) {
		t.Fatalf("arbitrary contract metrics = %#v, want recorder-derived %#v", observation.Metrics, wantMetrics)
	}
}

func TestConstructionContractsAreObservedBeforeQueryIO(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadLockedInputs(t)
	suite, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, contractID := range []string{"QRY-008", "QRY-010"} {
		observation := findObservation(t, suite, contractID)
		if observation.Phase != protocol.PhaseConstruction {
			t.Fatalf("%s phase = %q, want construction", contractID, observation.Phase)
		}
		if observation.Error == nil {
			t.Fatalf("%s error is nil", contractID)
		}
	}

	observation := findObservation(t, suite, "QRY-009")
	if observation.Phase != protocol.PhaseConstruction {
		t.Fatalf("QRY-009 phase = %q, want construction", observation.Phase)
	}
	wantMetrics := protocol.Object(map[string]protocol.Value{
		"queries_during_construction": protocol.Integer("0"),
	})
	if observation.Metrics == nil {
		t.Fatal("QRY-009 metrics are nil")
	}
	if !reflect.DeepEqual(*observation.Metrics, wantMetrics) {
		t.Fatalf("QRY-009 metrics = %#v, want %#v", *observation.Metrics, wantMetrics)
	}
}

func TestGenerateHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadLockedInputs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Generate(ctx, profile, manifest)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
}

func loadLockedInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadWriteMigrationInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "write-migration-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "write-migration-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadSaveLifecycleInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "save-lifecycle-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "save-lifecycle-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadQueryCacheInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "query-cache-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "query-cache-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadMigrationPlanningInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-planning-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-planning-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadMigrationRestartInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-restart-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-restart-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadMigrationStateReconstructionInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-state-reconstruction-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-state-reconstruction-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadMigrationLifecycleInputs(t *testing.T) (
	protocol.Profile,
	protocol.Manifest,
	protocol.ObservationSuite,
	protocol.DeviationExpectation,
) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-lifecycle-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	oracle, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-lifecycle-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	expectation, err := protocol.LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-lifecycle-deviation-expected.json"))
	if err != nil {
		t.Fatalf("LoadDeviationExpectation() error = %v", err)
	}
	return profile, manifest, oracle, expectation
}

func loadMigrationDefinitionSourceInputs(t *testing.T) (
	protocol.Profile,
	protocol.Manifest,
	protocol.ObservationSuite,
) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-definition-source-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-definition-source-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadMigrationProjectCheckInputs(t *testing.T) (
	protocol.Profile,
	protocol.Manifest,
	protocol.ObservationSuite,
) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-project-check-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-project-check-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadRelationProductInputs(t *testing.T) (
	protocol.Profile,
	protocol.Manifest,
	protocol.ObservationSuite,
) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "relation-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "relation-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func cloneObservationSuite(t *testing.T, source protocol.ObservationSuite) protocol.ObservationSuite {
	t.Helper()
	contents, err := protocol.MarshalCanonical(source)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := protocol.DecodeObservationSuite(bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	return clone
}

func findObservation(t *testing.T, suite protocol.ObservationSuite, contractID string) protocol.Observation {
	t.Helper()
	for _, observation := range suite.Contracts {
		if observation.ID == contractID {
			return observation
		}
	}
	t.Fatalf("observation %s is missing", contractID)
	return protocol.Observation{}
}
