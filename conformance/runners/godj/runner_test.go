package godj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/query"
)

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
