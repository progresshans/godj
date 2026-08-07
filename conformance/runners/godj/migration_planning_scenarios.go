package godj

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/migrations"
)

// The migration-planning adapter deliberately models only logical graph,
// applied-state, and target inputs. Planner has no database dependency, so the
// capture below derives before/after state from the same caller-owned applied
// snapshot and exposes the structural zero-I/O boundary without pretending to
// run a database probe.
type migrationPlanningDependency struct {
	child  migrations.MigrationKey
	parent migrations.MigrationKey
}

type migrationPlanningTarget struct {
	app  string
	name string
	zero bool
}

type migrationPlanningCase struct {
	name    string
	applied []migrations.MigrationKey
	targets []migrationPlanningTarget
}

type migrationPlanningErrorRequest struct {
	applied   []migrations.MigrationKey
	targets   []migrationPlanningTarget
	operation string
}

type migrationPlanningFixture struct {
	phase        protocol.Phase
	nodes        []migrations.MigrationKey
	dependencies []migrationPlanningDependency
	cases        []migrationPlanningCase
	errorRequest *migrationPlanningErrorRequest
}

type migrationPlanningCapture struct {
	plan                []migrations.PlanStep
	err                 error
	before              protocol.Value
	after               protocol.Value
	ddlStatements       int
	writeStatements     int
	nonSelectStatements int
}

var (
	planningA1 = migrations.MigrationKey{App: "alpha", Name: "0001_initial"}
	planningA2 = migrations.MigrationKey{App: "alpha", Name: "0002_second"}
	planningA3 = migrations.MigrationKey{App: "alpha", Name: "0003_third"}
	planningB1 = migrations.MigrationKey{App: "beta", Name: "0001_initial"}
	planningB2 = migrations.MigrationKey{App: "beta", Name: "0002_second"}
	planningG1 = migrations.MigrationKey{App: "gamma", Name: "0001_initial"}
	planningS1 = migrations.MigrationKey{App: "shared", Name: "0001_initial"}
)

var migrationPlanningFixtures = map[string]func() migrationPlanningFixture{
	"django.migration.plan.linear_forward": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase:        protocol.PhaseEvaluation,
			nodes:        planningLinearNodes(),
			dependencies: planningLinearDependencies(),
			cases: []migrationPlanningCase{{
				name:    "empty_history_to_linear_target",
				targets: []migrationPlanningTarget{planningNamedTarget(planningA3)},
			}},
		}
	},
	"django.migration.plan.applied_pruning": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase:        protocol.PhaseEvaluation,
			nodes:        planningLinearNodes(),
			dependencies: planningLinearDependencies(),
			cases: []migrationPlanningCase{
				{
					name:    "partially_applied_prefix",
					applied: []migrations.MigrationKey{planningA1},
					targets: []migrationPlanningTarget{planningNamedTarget(planningA3)},
				},
				{
					name:    "fully_applied_target",
					applied: []migrations.MigrationKey{planningA1, planningA2, planningA3},
					targets: []migrationPlanningTarget{planningNamedTarget(planningA3)},
				},
			},
		}
	},
	"django.migration.plan.missing_target": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase: protocol.PhaseEvaluation,
			nodes: []migrations.MigrationKey{planningA1},
			errorRequest: &migrationPlanningErrorRequest{
				applied: []migrations.MigrationKey{},
				targets: []migrationPlanningTarget{planningNamedTarget(migrations.MigrationKey{App: "alpha", Name: "9999_missing"})},
			},
		}
	},
	"django.migration.plan.prior_target": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase:        protocol.PhaseEvaluation,
			nodes:        planningLinearNodes(),
			dependencies: planningLinearDependencies(),
			cases: []migrationPlanningCase{{
				name:    "retain_prior_target",
				applied: []migrations.MigrationKey{planningA1, planningA2, planningA3},
				targets: []migrationPlanningTarget{planningNamedTarget(planningA1)},
			}},
		}
	},
	"django.migration.plan.zero_with_dependents": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase:        protocol.PhaseEvaluation,
			nodes:        planningCrossNodes(),
			dependencies: planningCrossDependencies(),
			cases: []migrationPlanningCase{{
				name:    "zero_includes_cross_app_dependents",
				applied: planningCrossNodes(),
				targets: []migrationPlanningTarget{planningZeroTarget("alpha")},
			}},
		}
	},
	"django.migration.plan.cross_app_forward": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase:        protocol.PhaseEvaluation,
			nodes:        planningCrossNodes(),
			dependencies: planningCrossDependencies(),
			cases: []migrationPlanningCase{{
				name:    "dependency_before_cross_app_target",
				targets: []migrationPlanningTarget{planningNamedTarget(planningB2)},
			}},
		}
	},
	"django.migration.plan.cross_app_backward": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase:        protocol.PhaseEvaluation,
			nodes:        planningCrossNodes(),
			dependencies: planningCrossDependencies(),
			cases: []migrationPlanningCase{{
				name:    "dependent_before_cross_app_dependency",
				applied: planningCrossNodes(),
				targets: []migrationPlanningTarget{planningNamedTarget(planningA1)},
			}},
		}
	},
	"django.migration.plan.ordered_targets_shared_dependency": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase: protocol.PhaseEvaluation,
			nodes: []migrations.MigrationKey{planningS1, planningA1, planningB1},
			dependencies: []migrationPlanningDependency{
				{child: planningA1, parent: planningS1},
				{child: planningB1, parent: planningS1},
			},
			cases: []migrationPlanningCase{{
				name: "ordered_targets_share_one_dependency",
				targets: []migrationPlanningTarget{
					planningNamedTarget(planningA1),
					planningNamedTarget(planningB1),
				},
			}},
		}
	},
	"django.migration.plan.retained_other_branches": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase: protocol.PhaseEvaluation,
			nodes: []migrations.MigrationKey{planningA1, planningA2, planningA3, planningB1, planningG1},
			dependencies: []migrationPlanningDependency{
				{child: planningA2, parent: planningA1},
				{child: planningA3, parent: planningA2},
				{child: planningB1, parent: planningA1},
			},
			cases: []migrationPlanningCase{{
				name:    "same_app_descendants_with_retained_branches",
				applied: []migrations.MigrationKey{planningA1, planningA2, planningA3, planningB1, planningG1},
				targets: []migrationPlanningTarget{planningNamedTarget(planningA1)},
			}},
		}
	},
	"django.migration.plan.inconsistent_history": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase: protocol.PhaseEvaluation,
			nodes: []migrations.MigrationKey{planningA1, planningA2},
			dependencies: []migrationPlanningDependency{
				{child: planningA2, parent: planningA1},
			},
			errorRequest: &migrationPlanningErrorRequest{
				applied:   []migrations.MigrationKey{planningA2},
				operation: "validate_history_before_planning",
			},
		}
	},
	"django.migration.plan.missing_dependency": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase: protocol.PhaseConstruction,
			nodes: []migrations.MigrationKey{planningA2},
			dependencies: []migrationPlanningDependency{
				{child: planningA2, parent: migrations.MigrationKey{App: "alpha", Name: "0001_missing"}},
			},
			errorRequest: &migrationPlanningErrorRequest{operation: "build_graph"},
		}
	},
	"django.migration.plan.dependency_cycle": func() migrationPlanningFixture {
		return migrationPlanningFixture{
			phase: protocol.PhaseConstruction,
			nodes: []migrations.MigrationKey{planningA1, planningB1},
			dependencies: []migrationPlanningDependency{
				{child: planningA1, parent: planningB1},
				{child: planningB1, parent: planningA1},
			},
			errorRequest: &migrationPlanningErrorRequest{operation: "build_graph"},
		}
	},
}

func migrationPlanningScenario(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	factory, ok := migrationPlanningFixtures[contract.Scenario]
	if !ok {
		return protocol.Observation{}, fmt.Errorf("unsupported scenario %q", contract.Scenario)
	}
	fixture := factory()
	if fixture.phase != contract.Phase {
		return protocol.Observation{}, fmt.Errorf("scenario %q phase = %q, manifest requires %q", contract.Scenario, fixture.phase, contract.Phase)
	}
	return runMigrationPlanningFixture(ctx, contract.ID, fixture)
}

func runMigrationPlanningFixture(ctx context.Context, contractID string, fixture migrationPlanningFixture) (protocol.Observation, error) {
	if err := ctx.Err(); err != nil {
		return protocol.Observation{}, err
	}
	if fixture.errorRequest != nil {
		return runMigrationPlanningErrorFixture(fixture, contractID)
	}

	resultCases := make([]protocol.Value, 0, len(fixture.cases))
	databaseCases := make([]protocol.Value, 0, len(fixture.cases))
	metricCases := make([]protocol.Value, 0, len(fixture.cases))
	for _, scenarioCase := range fixture.cases {
		capture := captureMigrationPlanning(scenarioCase.applied, func() ([]migrations.PlanStep, error) {
			planner, err := migrations.NewPlanner(planningMigrations(fixture.nodes, fixture.dependencies)...)
			if err != nil {
				return nil, err
			}
			applied, err := migrations.NewAppliedState(scenarioCase.applied...)
			if err != nil {
				return nil, err
			}
			return planner.Plan(applied, planningTargets(scenarioCase.targets)...)
		})
		if capture.err != nil {
			return protocol.Observation{}, fmt.Errorf("scenario case %q unexpectedly failed: %w", scenarioCase.name, capture.err)
		}
		resultCases = append(resultCases, protocol.Object(map[string]protocol.Value{
			"applied": protocol.List(planningKeyValuesSorted(scenarioCase.applied)...),
			"name":    protocol.String(scenarioCase.name),
			"plan":    protocol.List(planningStepValues(capture.plan)...),
			"targets": protocol.List(planningTargetValues(scenarioCase.targets)...),
		}))
		databaseCases = append(databaseCases, protocol.Object(map[string]protocol.Value{
			"after":  capture.after,
			"before": capture.before,
			"name":   protocol.String(scenarioCase.name),
		}))
		metricCases = append(metricCases, planningMutationMetrics(capture, map[string]protocol.Value{
			"name": protocol.String(scenarioCase.name),
		}))
	}

	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(resultCases...)})
	databaseState := protocol.Object(map[string]protocol.Value{"cases": protocol.List(databaseCases...)})
	metrics := protocol.Object(map[string]protocol.Value{"cases": protocol.List(metricCases...)})
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   fixture.phase,
		Result:  &result,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func runMigrationPlanningErrorFixture(fixture migrationPlanningFixture, contractID string) (protocol.Observation, error) {
	request := fixture.errorRequest
	capture := captureMigrationPlanning(request.applied, func() ([]migrations.PlanStep, error) {
		planner, err := migrations.NewPlanner(planningMigrations(fixture.nodes, fixture.dependencies)...)
		if err != nil {
			return nil, err
		}
		applied, err := migrations.NewAppliedState(request.applied...)
		if err != nil {
			return nil, err
		}
		return planner.Plan(applied, planningTargets(request.targets)...)
	})
	if capture.err == nil {
		return protocol.Observation{}, errors.New("migration-planning error fixture unexpectedly succeeded")
	}
	var planningError *migrations.PlanningError
	if !errors.As(capture.err, &planningError) {
		return protocol.Observation{}, fmt.Errorf("migration-planning error = %T, want *migrations.PlanningError: %w", capture.err, capture.err)
	}

	facts := map[string]protocol.Value{
		"graph":   planningGraphValue(fixture.nodes, fixture.dependencies),
		"request": planningRequestValue(*request),
	}
	metrics := planningMutationMetrics(capture, facts)
	databaseState := protocol.Object(map[string]protocol.Value{
		"after":  capture.after,
		"before": capture.before,
	})
	observedError := &protocol.ObservedError{
		Category:          string(planningError.Category),
		Code:              string(planningError.Code),
		Message:           capture.err.Error(),
		MessageIsContract: boolPointer(false),
	}
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   fixture.phase,
		Error:   observedError,
		DBState: &databaseState,
		Metrics: &metrics,
	}, nil
}

func captureMigrationPlanning(applied []migrations.MigrationKey, operation func() ([]migrations.PlanStep, error)) migrationPlanningCapture {
	before := planningDatabaseState(applied)
	plan, err := operation()
	after := planningDatabaseState(applied)
	return migrationPlanningCapture{
		plan:   plan,
		err:    err,
		before: before,
		after:  after,
	}
}

func planningMutationMetrics(capture migrationPlanningCapture, facts map[string]protocol.Value) protocol.Value {
	fields := make(map[string]protocol.Value, len(facts)+4)
	for name, value := range facts {
		fields[name] = value
	}
	fields["ddl_statement_count"] = protocol.Integer(fmt.Sprint(capture.ddlStatements))
	fields["non_select_statement_count"] = protocol.Integer(fmt.Sprint(capture.nonSelectStatements))
	fields["state_unchanged"] = protocol.Boolean(reflect.DeepEqual(capture.before, capture.after))
	fields["write_statement_count"] = protocol.Integer(fmt.Sprint(capture.writeStatements))
	return protocol.Object(fields)
}

func planningMigrations(nodes []migrations.MigrationKey, dependencies []migrationPlanningDependency) []migrations.Migration {
	byKey := make(map[migrations.MigrationKey][]migrations.MigrationKey, len(nodes))
	for _, node := range nodes {
		byKey[node] = nil
	}
	for _, dependency := range dependencies {
		byKey[dependency.child] = append(byKey[dependency.child], dependency.parent)
	}
	definitions := make([]migrations.Migration, 0, len(nodes))
	for _, node := range nodes {
		definitions = append(definitions, migrations.Migration{
			App:          node.App,
			Name:         node.Name,
			Dependencies: append([]migrations.MigrationKey(nil), byKey[node]...),
		})
	}
	return definitions
}

func planningTargets(targets []migrationPlanningTarget) []migrations.Target {
	result := make([]migrations.Target, 0, len(targets))
	for _, target := range targets {
		if target.zero {
			result = append(result, migrations.ZeroTarget(target.app))
			continue
		}
		result = append(result, migrations.NamedTarget(migrations.MigrationKey{App: target.app, Name: target.name}))
	}
	return result
}

func planningDatabaseState(applied []migrations.MigrationKey) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"applied_migrations":       protocol.List(planningKeyValuesSorted(applied)...),
		"managed_schema_inventory": protocol.List(),
		"recorder_present":         protocol.Boolean(true),
	})
}

func planningGraphValue(nodes []migrations.MigrationKey, dependencies []migrationPlanningDependency) protocol.Value {
	orderedDependencies := append([]migrationPlanningDependency(nil), dependencies...)
	sort.Slice(orderedDependencies, func(left, right int) bool {
		if planningKeyLess(orderedDependencies[left].child, orderedDependencies[right].child) {
			return true
		}
		if planningKeyLess(orderedDependencies[right].child, orderedDependencies[left].child) {
			return false
		}
		return planningKeyLess(orderedDependencies[left].parent, orderedDependencies[right].parent)
	})
	dependencyValues := make([]protocol.Value, 0, len(orderedDependencies))
	for _, dependency := range orderedDependencies {
		dependencyValues = append(dependencyValues, protocol.Object(map[string]protocol.Value{
			"child":  planningKeyValue(dependency.child),
			"parent": planningKeyValue(dependency.parent),
		}))
	}
	return protocol.Object(map[string]protocol.Value{
		"dependencies": protocol.List(dependencyValues...),
		"nodes":        protocol.List(planningKeyValuesSorted(nodes)...),
	})
}

func planningRequestValue(request migrationPlanningErrorRequest) protocol.Value {
	fields := make(map[string]protocol.Value, 3)
	if request.applied != nil {
		fields["applied"] = protocol.List(planningKeyValuesSorted(request.applied)...)
	}
	if request.targets != nil {
		fields["targets"] = protocol.List(planningTargetValues(request.targets)...)
	}
	if request.operation != "" {
		fields["operation"] = protocol.String(request.operation)
	}
	return protocol.Object(fields)
}

func planningStepValues(steps []migrations.PlanStep) []protocol.Value {
	values := make([]protocol.Value, 0, len(steps))
	for _, step := range steps {
		values = append(values, protocol.Object(map[string]protocol.Value{
			"app":       protocol.String(step.Key.App),
			"direction": protocol.String(string(step.Direction)),
			"name":      protocol.String(step.Key.Name),
		}))
	}
	return values
}

func planningTargetValues(targets []migrationPlanningTarget) []protocol.Value {
	values := make([]protocol.Value, 0, len(targets))
	for _, target := range targets {
		name := protocol.String(target.name)
		if target.zero {
			name = protocol.Null()
		}
		values = append(values, protocol.Object(map[string]protocol.Value{
			"app":  protocol.String(target.app),
			"name": name,
		}))
	}
	return values
}

func planningKeyValuesSorted(keys []migrations.MigrationKey) []protocol.Value {
	ordered := append([]migrations.MigrationKey(nil), keys...)
	sort.Slice(ordered, func(left, right int) bool { return planningKeyLess(ordered[left], ordered[right]) })
	values := make([]protocol.Value, 0, len(ordered))
	for _, key := range ordered {
		values = append(values, planningKeyValue(key))
	}
	return values
}

func planningKeyValue(key migrations.MigrationKey) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"app":  protocol.String(key.App),
		"name": protocol.String(key.Name),
	})
}

func planningKeyLess(left, right migrations.MigrationKey) bool {
	if left.App != right.App {
		return left.App < right.App
	}
	return left.Name < right.Name
}

func planningNamedTarget(key migrations.MigrationKey) migrationPlanningTarget {
	return migrationPlanningTarget{app: key.App, name: key.Name}
}

func planningZeroTarget(app string) migrationPlanningTarget {
	return migrationPlanningTarget{app: app, zero: true}
}

func planningLinearNodes() []migrations.MigrationKey {
	return []migrations.MigrationKey{planningA1, planningA2, planningA3}
}

func planningLinearDependencies() []migrationPlanningDependency {
	return []migrationPlanningDependency{
		{child: planningA2, parent: planningA1},
		{child: planningA3, parent: planningA2},
	}
}

func planningCrossNodes() []migrations.MigrationKey {
	return []migrations.MigrationKey{planningA1, planningA2, planningB1, planningB2}
}

func planningCrossDependencies() []migrationPlanningDependency {
	return []migrationPlanningDependency{
		{child: planningA2, parent: planningA1},
		{child: planningB1, parent: planningA2},
		{child: planningB2, parent: planningB1},
	}
}
