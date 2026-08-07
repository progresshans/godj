package migrations

import "fmt"

// MigrationKey is the stable identity of a migration. Both App and Name must
// be non-empty when a key enters a Planner, AppliedState, or named Target.
type MigrationKey struct {
	App  string
	Name string
}

// AppliedState is an immutable snapshot of migration identities recorded as
// applied. Its zero value is a valid empty snapshot.
type AppliedState struct {
	keys map[MigrationKey]struct{}
}

// NewAppliedState validates and copies an applied migration snapshot.
func NewAppliedState(keys ...MigrationKey) (AppliedState, error) {
	ordered := append([]MigrationKey(nil), keys...)
	sortMigrationKeys(ordered)

	for _, key := range ordered {
		if !validMigrationKey(key) {
			return AppliedState{}, newPlanningError(CategoryHistory, CodeInvalidAppliedState, key, MigrationKey{}, nil)
		}
	}
	for index := 1; index < len(ordered); index++ {
		if ordered[index] == ordered[index-1] {
			return AppliedState{}, newPlanningError(CategoryHistory, CodeDuplicateApplied, ordered[index], MigrationKey{}, nil)
		}
	}

	applied := AppliedState{keys: make(map[MigrationKey]struct{}, len(ordered))}
	for _, key := range ordered {
		applied.keys[key] = struct{}{}
	}
	return applied, nil
}

type targetKind uint8

const (
	targetNamed targetKind = iota + 1
	targetZero
)

// Target is an immutable tagged migration planning target. Construct targets
// with NamedTarget or ZeroTarget; the zero value is deliberately invalid.
type Target struct {
	kind targetKind
	key  MigrationKey
	app  string
}

// NamedTarget keeps the named migration applied. If it is not applied, the
// planner adds it and its prerequisites; if it is applied, the planner removes
// its applicable descendants while retaining the target itself.
func NamedTarget(key MigrationKey) Target {
	return Target{kind: targetNamed, key: key}
}

// ZeroTarget removes the applied migration roots for app and their applied
// dependents. A syntactically valid app with no graph nodes is an empty plan.
func ZeroTarget(app string) Target {
	return Target{kind: targetZero, app: app}
}

// PlanStep describes one migration identity and the direction in which it
// should be executed.
type PlanStep struct {
	Key       MigrationKey
	Direction Direction
}

// PlanningError classifies graph, history, and target failures independently
// from migration execution and rollback errors.
type PlanningError struct {
	Category ErrorCategory
	Code     ErrorCode
	Node     MigrationKey
	Related  MigrationKey
	members  []MigrationKey
}

func (e *PlanningError) Error() string {
	if e == nil {
		return "migration planning error"
	}
	message := fmt.Sprintf("%s/%s", e.Category, e.Code)
	if e.Node != (MigrationKey{}) {
		message += fmt.Sprintf(" node=%s.%s", e.Node.App, e.Node.Name)
	}
	if e.Related != (MigrationKey{}) {
		message += fmt.Sprintf(" related=%s.%s", e.Related.App, e.Related.Name)
	}
	if len(e.members) != 0 {
		message += fmt.Sprintf(" members=%v", e.members)
	}
	return message
}

// Members returns a fresh copy of the selected cycle component. Mutating the
// returned slice cannot affect this error or later Planner results.
func (e *PlanningError) Members() []MigrationKey {
	if e == nil || len(e.members) == 0 {
		return nil
	}
	return append([]MigrationKey(nil), e.members...)
}

func newPlanningError(category ErrorCategory, code ErrorCode, node, related MigrationKey, members []MigrationKey) *PlanningError {
	return &PlanningError{
		Category: category,
		Code:     code,
		Node:     node,
		Related:  related,
		members:  append([]MigrationKey(nil), members...),
	}
}

// Planner owns an immutable migration identity/dependency graph. Its zero
// value is equivalent to NewPlanner() and is safe for concurrent Plan calls.
type Planner struct {
	graph *plannerGraph
}

// NewPlanner validates migrations and deep-copies only their identity and
// dependency edges. Operations and caller-owned slices are never retained.
func NewPlanner(migrations ...Migration) (Planner, error) {
	graph, err := newPlannerGraph(migrations)
	if err != nil {
		return Planner{}, err
	}
	return Planner{graph: graph}, nil
}

// Plan validates the complete target representation, checks known applied
// history, then processes targets in caller order against a local applied set.
// It performs no I/O and does not mutate Planner, AppliedState, or its inputs.
func (p Planner) Plan(applied AppliedState, targets ...Target) ([]PlanStep, error) {
	for _, target := range targets {
		if err := validateTarget(target); err != nil {
			return nil, err
		}
	}

	graph := p.graph
	if graph == nil {
		graph = emptyPlannerGraph()
	}
	working := cloneAppliedKeys(applied.keys)
	if err := graph.validateAppliedHistory(working); err != nil {
		return nil, err
	}

	var plan []PlanStep
	for _, target := range targets {
		switch target.kind {
		case targetNamed:
			if !graph.contains(target.key) {
				return nil, newPlanningError(CategoryPlan, CodeTargetNotFound, target.key, MigrationKey{}, nil)
			}
			if _, exists := working[target.key]; !exists {
				steps, err := graph.planForward(target.key, working)
				if err != nil {
					return nil, err
				}
				plan = append(plan, steps...)
				continue
			}
			for _, child := range graph.children[target.key] {
				if child.App != target.key.App {
					continue
				}
				steps, err := graph.planBackward(child, working)
				if err != nil {
					return nil, err
				}
				plan = append(plan, steps...)
			}
		case targetZero:
			for _, root := range graph.appRoots(target.app) {
				steps, err := graph.planBackward(root, working)
				if err != nil {
					return nil, err
				}
				plan = append(plan, steps...)
			}
		}
	}
	return plan, nil
}

func validateTarget(target Target) error {
	switch target.kind {
	case targetNamed:
		if validMigrationKey(target.key) && target.app == "" {
			return nil
		}
		return newPlanningError(CategoryPlan, CodeInvalidTarget, target.key, MigrationKey{}, nil)
	case targetZero:
		if target.app != "" && target.key == (MigrationKey{}) {
			return nil
		}
		return newPlanningError(CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{}, nil)
	default:
		return newPlanningError(CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{}, nil)
	}
}

func cloneAppliedKeys(keys map[MigrationKey]struct{}) map[MigrationKey]struct{} {
	cloned := make(map[MigrationKey]struct{}, len(keys))
	for key := range keys {
		cloned[key] = struct{}{}
	}
	return cloned
}
