package migrations

import (
	"fmt"
	"sort"
)

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

// MigrationStatus is the closed status vocabulary returned by Planner.Statuses.
type MigrationStatus string

const (
	MigrationStatusApplied           MigrationStatus = "applied"
	MigrationStatusUnapplied         MigrationStatus = "unapplied"
	MigrationStatusDefinitionMissing MigrationStatus = "definition-missing"
)

// MigrationStatusEntry describes one known migration definition or one
// applied recorder identity whose definition is absent from this Planner.
type MigrationStatusEntry struct {
	Key    MigrationKey
	Status MigrationStatus
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

// CheckHistory validates known dependency relationships in an applied
// migration snapshot without planning targets or performing I/O. Applied keys
// unknown to this Planner are preserved and ignored by this check.
func (p Planner) CheckHistory(applied AppliedState) error {
	graph := p.graph
	if graph == nil {
		graph = emptyPlannerGraph()
	}
	return graph.validateAppliedHistory(cloneAppliedKeys(applied.keys))
}

// Statuses returns a fresh read-only view of every known migration and every
// applied recorder identity whose definition is absent. It validates known
// applied history before constructing any result, performs no I/O, and does
// not mutate Planner or AppliedState.
//
// Apps are grouped lexicographically. Known migrations retain the Planner's
// dependency-valid full-forward order within each app. Definition-missing
// identities follow the known rows in their app and are ordered by name.
func (p Planner) Statuses(applied AppliedState) ([]MigrationStatusEntry, error) {
	if err := p.CheckHistory(applied); err != nil {
		return nil, err
	}

	graph := p.graph
	if graph == nil {
		graph = emptyPlannerGraph()
	}
	steps, err := historicalFullForwardProjection(p)
	if err != nil {
		return nil, err
	}

	knownByApp := make(map[string][]MigrationKey)
	missingByApp := make(map[string][]MigrationKey)
	apps := make(map[string]struct{})
	for _, step := range steps {
		knownByApp[step.Key.App] = append(knownByApp[step.Key.App], step.Key)
		apps[step.Key.App] = struct{}{}
	}
	for key := range applied.keys {
		if graph.contains(key) {
			continue
		}
		missingByApp[key.App] = append(missingByApp[key.App], key)
		apps[key.App] = struct{}{}
	}

	appNames := make([]string, 0, len(apps))
	for app := range apps {
		appNames = append(appNames, app)
	}
	sort.Strings(appNames)

	statuses := make([]MigrationStatusEntry, 0, len(graph.nodes)+len(applied.keys))
	for _, app := range appNames {
		for _, key := range knownByApp[app] {
			status := MigrationStatusUnapplied
			if _, exists := applied.keys[key]; exists {
				status = MigrationStatusApplied
			}
			statuses = append(statuses, MigrationStatusEntry{Key: key, Status: status})
		}

		missing := missingByApp[app]
		sortMigrationKeys(missing)
		for _, key := range missing {
			statuses = append(statuses, MigrationStatusEntry{
				Key:    key,
				Status: MigrationStatusDefinitionMissing,
			})
		}
	}
	return statuses, nil
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
