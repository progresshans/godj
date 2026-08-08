package migrations

import (
	"errors"
	"fmt"
)

type stateRequestKind uint8

const (
	stateRequestEmpty stateRequestKind = iota + 1
	stateRequestLatest
	stateRequestBefore
	stateRequestAfter
	stateRequestApplied
)

// StateRequest is an immutable tagged request for a historical ProjectState.
// Construct requests with the functions below; the zero value is deliberately
// invalid so an explicit empty state cannot be confused with the latest state.
type StateRequest struct {
	kind    stateRequestKind
	targets []MigrationKey
	applied AppliedState
}

// EmptyStateRequest requests the explicit empty historical state.
func EmptyStateRequest() StateRequest {
	return StateRequest{kind: stateRequestEmpty}
}

// LatestStateRequest requests the union of every same-app leaf closure.
func LatestStateRequest() StateRequest {
	return StateRequest{kind: stateRequestLatest}
}

// BeforeStateRequest requests the dependency closure immediately before all
// named targets. The target order is copied and controls independent-branch
// replay order; every explicitly named target is excluded from the replay.
func BeforeStateRequest(first MigrationKey, rest ...MigrationKey) StateRequest {
	return targetStateRequest(stateRequestBefore, first, rest)
}

// AfterStateRequest requests the union of the named target closures, including
// the targets. The target order is copied and shared dependencies replay once.
func AfterStateRequest(first MigrationKey, rest ...MigrationKey) StateRequest {
	return targetStateRequest(stateRequestAfter, first, rest)
}

func targetStateRequest(kind stateRequestKind, first MigrationKey, rest []MigrationKey) StateRequest {
	targets := make([]MigrationKey, 1, len(rest)+1)
	targets[0] = first
	targets = append(targets, rest...)
	return StateRequest{kind: kind, targets: targets}
}

// AppliedStateRequest requests the state represented by a durable applied
// history snapshot. Known nodes replay in canonical full-forward order;
// unknown valid identities remain part of the snapshot but create no schema.
func AppliedStateRequest(applied AppliedState) StateRequest {
	return StateRequest{
		kind:    stateRequestApplied,
		applied: AppliedState{keys: cloneAppliedKeys(applied.keys)},
	}
}

// StateReconstructor owns an immutable migration graph and deep-copied built-in
// definitions. Its zero value is equivalent to NewStateReconstructor() and is
// safe for repeated and concurrent Reconstruct calls.
type StateReconstructor struct {
	planner     Planner
	definitions map[MigrationKey]Migration
}

// NewStateReconstructor validates the identity graph and snapshots every
// supported built-in operation. Unsupported or nil sealed operations fail
// closed rather than retaining an alias to caller-owned state.
func NewStateReconstructor(migrations ...Migration) (StateReconstructor, error) {
	planner, err := NewPlanner(migrations...)
	if err != nil {
		return StateReconstructor{}, err
	}
	definitions, err := cloneReconstructorDefinitions(planner.graph, migrations)
	if err != nil {
		return StateReconstructor{}, err
	}
	return StateReconstructor{planner: planner, definitions: definitions}, nil
}

// Reconstruct replays only in-memory state transitions. It performs no
// backend, recorder, SQL, or other I/O and returns a fresh ProjectState.
func (r StateReconstructor) Reconstruct(request StateRequest) (ProjectState, error) {
	targets, applied, err := validateStateRequest(request)
	if err != nil {
		return EmptyProjectState(), err
	}

	var steps []PlanStep
	switch request.kind {
	case stateRequestEmpty:
		return EmptyProjectState(), nil
	case stateRequestLatest:
		steps, err = r.fullForwardProjection()
	case stateRequestBefore, stateRequestAfter:
		steps, err = r.targetProjection(targets)
		if err == nil && request.kind == stateRequestBefore {
			steps = withoutExplicitTargets(steps, targets)
		}
	case stateRequestApplied:
		if err = r.planner.CheckHistory(applied); err == nil {
			steps, err = r.fullForwardProjection()
		}
		if err == nil {
			steps = onlyAppliedSteps(steps, applied)
		}
	}
	if err != nil {
		return EmptyProjectState(), err
	}
	return r.replay(steps)
}

func validateStateRequest(request StateRequest) ([]MigrationKey, AppliedState, error) {
	switch request.kind {
	case stateRequestEmpty, stateRequestLatest:
		if request.targets != nil || request.applied.keys != nil {
			return nil, AppliedState{}, invalidStateRequest(MigrationKey{})
		}
		return nil, AppliedState{}, nil
	case stateRequestBefore, stateRequestAfter:
		if len(request.targets) == 0 || request.applied.keys != nil {
			return nil, AppliedState{}, invalidStateRequest(MigrationKey{})
		}
		targets := append([]MigrationKey(nil), request.targets...)
		for _, key := range targets {
			if err := validateTarget(NamedTarget(key)); err != nil {
				return nil, AppliedState{}, err
			}
		}
		return targets, AppliedState{}, nil
	case stateRequestApplied:
		if request.targets != nil || request.applied.keys == nil {
			return nil, AppliedState{}, invalidStateRequest(MigrationKey{})
		}
		keys := make([]MigrationKey, 0, len(request.applied.keys))
		for key := range request.applied.keys {
			keys = append(keys, key)
		}
		applied, err := NewAppliedState(keys...)
		if err != nil {
			return nil, AppliedState{}, err
		}
		return nil, applied, nil
	default:
		return nil, AppliedState{}, invalidStateRequest(MigrationKey{})
	}
}

func invalidStateRequest(node MigrationKey) error {
	return newPlanningError(CategoryPlan, CodeInvalidTarget, node, MigrationKey{}, nil)
}

func (r StateReconstructor) graph() *plannerGraph {
	if r.planner.graph == nil {
		return emptyPlannerGraph()
	}
	return r.planner.graph
}

func (r StateReconstructor) fullForwardProjection() ([]PlanStep, error) {
	return r.targetProjection(r.graph().appLeaves())
}

// targetProjection asks Planner for each closure independently. Passing every
// target to a single Plan call would use Planner's sequential target semantics:
// a later already-applied ancestor can intentionally roll descendants back.
// Historical reconstruction instead takes a caller-ordered closure union.
func (r StateReconstructor) targetProjection(targets []MigrationKey) ([]PlanStep, error) {
	seen := make(map[MigrationKey]struct{})
	steps := make([]PlanStep, 0)
	for _, target := range targets {
		closure, err := r.planner.Plan(AppliedState{}, NamedTarget(target))
		if err != nil {
			return nil, err
		}
		for _, step := range closure {
			if step.Direction != DirectionForward {
				return nil, migrationError(
					CategoryState,
					CodeInvalidState,
					step.Direction,
					Migration{App: step.Key.App, Name: step.Key.Name},
					NoOperation,
					"",
					errors.New("historical projection contains a non-forward step"),
				)
			}
			if _, exists := seen[step.Key]; exists {
				continue
			}
			seen[step.Key] = struct{}{}
			steps = append(steps, step)
		}
	}
	return steps, nil
}

func withoutExplicitTargets(steps []PlanStep, targets []MigrationKey) []PlanStep {
	excluded := make(map[MigrationKey]struct{}, len(targets))
	for _, target := range targets {
		excluded[target] = struct{}{}
	}
	filtered := make([]PlanStep, 0, len(steps))
	for _, step := range steps {
		if _, exists := excluded[step.Key]; !exists {
			filtered = append(filtered, step)
		}
	}
	return filtered
}

func onlyAppliedSteps(steps []PlanStep, applied AppliedState) []PlanStep {
	filtered := make([]PlanStep, 0, len(steps))
	for _, step := range steps {
		if _, exists := applied.keys[step.Key]; exists {
			filtered = append(filtered, step)
		}
	}
	return filtered
}

func (r StateReconstructor) replay(steps []PlanStep) (ProjectState, error) {
	state := EmptyProjectState()
	for _, step := range steps {
		migration, exists := r.definitions[step.Key]
		if !exists {
			return EmptyProjectState(), migrationError(
				CategoryState,
				CodeInvalidState,
				DirectionForward,
				Migration{App: step.Key.App, Name: step.Key.Name},
				NoOperation,
				"",
				errors.New("historical projection has no migration definition"),
			)
		}
		_, next, err := preflight(state, migration, DirectionForward)
		if err != nil {
			return EmptyProjectState(), err
		}
		state = next
	}
	return state.Clone(), nil
}

func cloneReconstructorDefinitions(graph *plannerGraph, migrations []Migration) (map[MigrationKey]Migration, error) {
	byKey := make(map[MigrationKey]Migration, len(migrations))
	for _, migration := range migrations {
		byKey[migration.Key()] = migration
	}

	cloned := make(map[MigrationKey]Migration, len(migrations))
	for _, key := range graph.nodes {
		definition := byKey[key]
		snapshot := Migration{
			App:          definition.App,
			Name:         definition.Name,
			Dependencies: append([]MigrationKey(nil), definition.Dependencies...),
			Operations:   make([]Operation, len(definition.Operations)),
		}
		for index, operation := range definition.Operations {
			if isNilOperation(operation) {
				return nil, invalidReconstructorOperation(definition, index, "", errors.New("operation is nil"))
			}
			copy, kind, supported := cloneReconstructorOperation(operation)
			if !supported {
				return nil, invalidReconstructorOperation(
					definition,
					index,
					"",
					fmt.Errorf("operation type %T is not supported by StateReconstructor", operation),
				)
			}
			if copy.App() != definition.App {
				return nil, invalidReconstructorOperation(
					definition,
					index,
					kind,
					fmt.Errorf("operation app %q does not match migration app %q", copy.App(), definition.App),
				)
			}
			snapshot.Operations[index] = copy
		}
		cloned[key] = snapshot
	}
	return cloned, nil
}

func cloneReconstructorOperation(operation Operation) (Operation, string, bool) {
	switch operation := operation.(type) {
	case CreateModel:
		operation.Model = operation.Model.Clone()
		return operation, operation.Kind(), true
	case *CreateModel:
		cloned := *operation
		cloned.Model = operation.Model.Clone()
		return &cloned, operation.Kind(), true
	case AddField:
		operation.Field = cloneMigrationField(operation.Field)
		return operation, operation.Kind(), true
	case *AddField:
		cloned := *operation
		cloned.Field = cloneMigrationField(operation.Field)
		return &cloned, operation.Kind(), true
	default:
		return nil, "", false
	}
}

func invalidReconstructorOperation(migration Migration, index int, kind string, cause error) error {
	return migrationError(
		CategoryState,
		CodeInvalidState,
		DirectionForward,
		migration,
		index,
		kind,
		cause,
	)
}
