package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/progresshans/godj/internal/irresource"
	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/schema/ir"
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

// StateReconstructor owns the same immutable, relation-capable historical core
// used by the loaded migration lifecycle. Its zero value is equivalent to
// NewStateReconstructor() and is safe for repeated and concurrent Reconstruct
// calls.
type StateReconstructor struct {
	core        loadedStateReconstructor
	initialized bool
}

// NewStateReconstructor validates the complete relation chronology and
// forward/reverse readiness before publishing an immutable definition
// snapshot. Unsupported or nil sealed operations fail closed rather than
// retaining an alias to caller-owned state.
func NewStateReconstructor(migrations ...Migration) (StateReconstructor, error) {
	core, err := newLoadedStateReconstructor(migrations)
	if err != nil {
		return StateReconstructor{}, err
	}
	return StateReconstructor{core: core, initialized: true}, nil
}

// Reconstruct replays only in-memory state transitions. It performs no
// backend, recorder, SQL, or other I/O and returns a fresh ProjectState.
func (r StateReconstructor) Reconstruct(request StateRequest) (ProjectState, error) {
	core, err := r.currentCore()
	if err != nil {
		return EmptyProjectState(), err
	}
	return core.Reconstruct(request)
}

func (r StateReconstructor) currentCore() (loadedStateReconstructor, error) {
	if r.initialized {
		return r.core, nil
	}
	// Construct a fresh empty immutable core instead of mutating the zero value.
	// Concurrent calls therefore remain pure and race-free.
	return newLoadedStateReconstructor(nil)
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

// targetProjection asks Planner for each closure independently. Passing every
// target to a single Plan call would use Planner's sequential target semantics:
// a later already-applied ancestor can intentionally roll descendants back.
// Historical reconstruction instead takes a caller-ordered closure union.
func historicalTargetProjection(planner Planner, targets []MigrationKey) ([]PlanStep, error) {
	seen := make(map[MigrationKey]struct{})
	steps := make([]PlanStep, 0)
	for _, target := range targets {
		closure, err := planner.Plan(AppliedState{}, NamedTarget(target))
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

func cloneReconstructorOperation(operation Operation) (Operation, string, bool) {
	switch operation := cloneMigrationOperation(operation).(type) {
	case CreateModel, AddField, AlterField:
		return operation, operation.Kind(), true
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

// loadedStateReconstructor is the single relation-capable historical state
// engine shared by the public reconstruction API and loaded lifecycle. Its
// constructor snapshots and validates the exact visible full DAG before this
// value can be published through either boundary.
type loadedStateReconstructor struct {
	planner      Planner
	definitions  map[MigrationKey]Migration
	creators     map[loadedModelIdentity][]loadedModelCreator
	declarations []loadedRelationDeclaration
	ancestors    loadedAncestorIndex
}

type loadedAncestorIndex struct {
	positions map[MigrationKey]int
	sets      [][]uint64
}

type loadedModelIdentity struct {
	app   string
	model string
}

type loadedModelCreator struct {
	key            MigrationKey
	operationIndex int
	model          ir.Model
}

type loadedRelationDeclaration struct {
	key            MigrationKey
	operationIndex int
	operationKind  string
	source         loadedModelIdentity
	field          ir.Field
}

// loadedRelationTargetView borrows immutable values from the sealed
// definition and loaded-state builder. It exists so the complete backend
// intent can be resource-counted before any of its model snapshots are
// cloned.
type loadedRelationTargetView struct {
	sourceFieldName  string
	targetModel      ir.Model
	targetPrimaryKey ir.Field
}

type loadedOperationView struct {
	index         int
	operation     Operation
	appLabel      string
	before        ir.Model
	beforeExists  bool
	after         ir.Model
	afterExists   bool
	sourceFields  []ir.Field
	targets       []loadedRelationTargetView
	relatedModels []migrationgraph.MigrationModel
}

// loadedStateBuilder is the private, loader-authorized historical state. It
// mutates owned clones in place so readiness and reconstruction are linear in
// the accepted definition payload instead of repeatedly cloning the whole
// growing ProjectState for every AddField operation.
type loadedStateBuilder struct {
	apps          map[string]*loadedStateApp
	relationCount uint64
	reverse       map[loadedModelIdentity]map[string]loadedReverseOwner
	incoming      map[loadedModelIdentity]map[loadedReverseOwner]struct{}
}

type loadedStateApp struct {
	models   map[string]*loadedStateModel
	order    []string
	goNames  map[string]string
	dbTables map[string]string
}

type loadedStateModel struct {
	value      ir.Model
	primaryKey ir.Field
	fieldNames map[string]int
	goNames    map[string]string
	columns    map[string]string
}

type loadedReverseOwner struct {
	source loadedModelIdentity
	field  string
}

type loadedRelationRequirements uint8

const (
	loadedRequiresCreateModelForeignKeys loadedRelationRequirements = 1 << iota
	loadedRequiresAddNullableForeignKey
	loadedRequiresAddRequiredForeignKeyToEmptyTable
	loadedRequiresRemoveForeignKey
	loadedRequiresAlterFieldChoices
)

const (
	loadedDerivedIntentMaxOperations     = 2_048
	loadedDerivedIntentMaxFields         = 2_048
	loadedDerivedIntentMaxTargets        = 2_048
	loadedDerivedIntentMaxStringBytes    = 1 << 20
	loadedDerivedIntentMaxAggregateBytes = 16 << 20
	loadedDerivedIntentMaxNodes          = 262_144
)

type loadedDerivedIntentBudget struct {
	irresource.Budget
}

type loadedRelationOperationKind uint8

const (
	loadedRelationCreateModel loadedRelationOperationKind = iota + 1
	loadedRelationDeleteModel
	loadedRelationAddField
	loadedRelationRemoveField
	loadedRelationAlterField
)

type loadedRelationIntent struct {
	operations []loadedRelationOperation
}

type loadedRelationOperation struct {
	operationIndex int
	kind           loadedRelationOperationKind
	before         ir.Model
	after          ir.Model
	targets        []loadedRelationBackendTarget
	relatedModels  []migrationgraph.MigrationModel
}

type loadedRelationBackendTarget struct {
	sourceField ir.Field
	targetModel ir.Model
	targetKey   ir.Field
}

type loadedPlanStep struct {
	step         PlanStep
	requirements loadedRelationRequirements
	seal         [sha256.Size]byte
}

type loadedMaterializedStep struct {
	prepared       preparedPlanStep
	execution      []loadedOperationView
	intent         loadedRelationIntent
	requirements   loadedRelationRequirements
	stateUnchanged bool
	seal           [sha256.Size]byte
}

type loadedStepSealPayload struct {
	Direction    Direction                     `json:"direction"`
	App          string                        `json:"app"`
	Migration    string                        `json:"migration"`
	BeforeFormat int                           `json:"before_format"`
	AfterFormat  int                           `json:"after_format"`
	Operations   []loadedRelationOperationSeal `json:"operations"`
}

type loadedRelationOperationSeal struct {
	OperationIndex int                             `json:"operation_index"`
	Kind           loadedRelationOperationKind     `json:"kind"`
	Before         ir.Model                        `json:"before"`
	After          ir.Model                        `json:"after"`
	Targets        []loadedRelationTargetSeal      `json:"targets"`
	RelatedModels  []migrationgraph.MigrationModel `json:"related_models"`
}

type loadedRelationTargetSeal struct {
	SourceField ir.Field `json:"source_field"`
	TargetModel ir.Model `json:"target_model"`
	TargetKey   ir.Field `json:"target_key"`
}

func newLoadedStateBuilder() *loadedStateBuilder {
	return &loadedStateBuilder{
		apps:     make(map[string]*loadedStateApp),
		reverse:  make(map[loadedModelIdentity]map[string]loadedReverseOwner),
		incoming: make(map[loadedModelIdentity]map[loadedReverseOwner]struct{}),
	}
}

func (builder *loadedStateBuilder) clone() *loadedStateBuilder {
	if builder == nil {
		return newLoadedStateBuilder()
	}
	cloned := newLoadedStateBuilder()
	cloned.relationCount = builder.relationCount
	for appLabel, app := range builder.apps {
		clonedApp := &loadedStateApp{
			models:   make(map[string]*loadedStateModel, len(app.models)),
			order:    append([]string(nil), app.order...),
			goNames:  make(map[string]string, len(app.goNames)),
			dbTables: make(map[string]string, len(app.dbTables)),
		}
		for modelName, model := range app.models {
			clonedApp.models[modelName] = newLoadedStateModel(model.value, model.primaryKey)
		}
		for name, modelName := range app.goNames {
			clonedApp.goNames[name] = modelName
		}
		for table, modelName := range app.dbTables {
			clonedApp.dbTables[table] = modelName
		}
		cloned.apps[appLabel] = clonedApp
	}
	for target, owners := range builder.reverse {
		clonedOwners := make(map[string]loadedReverseOwner, len(owners))
		for name, owner := range owners {
			clonedOwners[name] = owner
		}
		cloned.reverse[target] = clonedOwners
	}
	for target, owners := range builder.incoming {
		clonedOwners := make(map[loadedReverseOwner]struct{}, len(owners))
		for owner := range owners {
			clonedOwners[owner] = struct{}{}
		}
		cloned.incoming[target] = clonedOwners
	}
	return cloned
}

func (builder *loadedStateBuilder) model(identity loadedModelIdentity) (*loadedStateModel, bool) {
	app, exists := builder.apps[identity.app]
	if !exists {
		return nil, false
	}
	model, exists := app.models[identity.model]
	return model, exists
}

func (builder *loadedStateBuilder) projectState() (ProjectState, error) {
	state := ProjectState{formatVersion: StateFormatVersion, apps: make(map[string]ir.Schema, len(builder.apps))}
	for appLabel, app := range builder.apps {
		schema := ir.Schema{
			FormatVersion: ir.CurrentFormatVersion,
			AppLabel:      appLabel,
			Models:        make([]ir.Model, 0, len(app.order)),
		}
		for _, modelName := range app.order {
			model, exists := app.models[modelName]
			if !exists {
				return ProjectState{}, fmt.Errorf("loaded model order contains missing model %s.%s", appLabel, modelName)
			}
			// Normalize clones its input before validating it, so these borrowed
			// model views never escape the builder.
			schema.Models = append(schema.Models, model.value)
		}
		normalized, err := ir.Normalize(schema)
		if err != nil {
			return ProjectState{}, fmt.Errorf("normalize loaded project app %s: %w", appLabel, err)
		}
		if !reflect.DeepEqual(normalized, schema) {
			return ProjectState{}, fmt.Errorf("loaded project app %s is not normalized", appLabel)
		}
		// normalized is already an owned, validated deep snapshot. Re-running
		// ProjectState.validate or cloning it here would normalize and copy the
		// entire accumulated state again at every migration boundary.
		state.apps[appLabel] = normalized
	}
	return state, nil
}

func (builder *loadedStateBuilder) empty() bool {
	return len(builder.apps) == 0 && builder.relationCount == 0 && len(builder.reverse) == 0 && len(builder.incoming) == 0
}

func newLoadedStateReconstructor(
	definitions []Migration,
) (loadedStateReconstructor, error) {
	return newLoadedStateReconstructorContext(context.Background(), definitions)
}

func newLoadedStateReconstructorContext(
	ctx context.Context,
	definitions []Migration,
) (loadedStateReconstructor, error) {
	if ctx == nil {
		return loadedStateReconstructor{}, executionContextError(PlanStep{}, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return loadedStateReconstructor{}, executionContextError(PlanStep{}, err)
	}
	if err := validateLoadedDefinitionResources(definitions); err != nil {
		return loadedStateReconstructor{}, err
	}
	if err := ctx.Err(); err != nil {
		return loadedStateReconstructor{}, executionContextError(PlanStep{}, err)
	}
	planner, err := NewPlanner(definitions...)
	if err != nil {
		return loadedStateReconstructor{}, err
	}
	return buildLoadedStateReconstructor(ctx, definitions, planner)
}

// buildLoadedStateReconstructor consumes resource-checked definitions and their
// immutable graph. Loaded lifecycles borrow both from the loader publication;
// the raw constructor above prepares them at its own input boundary.
func buildLoadedStateReconstructor(ctx context.Context, definitions []Migration, planner Planner) (loadedStateReconstructor, error) {
	if err := ctx.Err(); err != nil {
		return loadedStateReconstructor{}, executionContextError(PlanStep{}, err)
	}

	cloned, err := cloneLoadedReconstructorDefinitions(planner.graph, definitions)
	if err != nil {
		return loadedStateReconstructor{}, err
	}
	if err := ctx.Err(); err != nil {
		return loadedStateReconstructor{}, executionContextError(PlanStep{}, err)
	}

	reconstructor := loadedStateReconstructor{planner: planner, definitions: cloned}
	reconstructor.ancestors = newLoadedAncestorIndex(planner.graph)
	reconstructor.creators, reconstructor.declarations = collectLoadedStateGraph(planner.graph, cloned)
	if err := reconstructor.validateChronology(); err != nil {
		return loadedStateReconstructor{}, err
	}
	if err := ctx.Err(); err != nil {
		return loadedStateReconstructor{}, executionContextError(PlanStep{}, err)
	}
	if err := reconstructor.validateReadiness(ctx); err != nil {
		return loadedStateReconstructor{}, err
	}
	return reconstructor, nil
}

func cloneLoadedReconstructorDefinitions(graph *plannerGraph, definitions []Migration) (map[MigrationKey]Migration, error) {
	byKey := make(map[MigrationKey]Migration, len(definitions))
	for index := range definitions {
		byKey[definitions[index].Key()] = definitions[index]
	}
	cloned := make(map[MigrationKey]Migration, len(definitions))
	for _, key := range graph.nodes {
		definition := byKey[key]
		snapshot := Migration{
			App: definition.App, Name: definition.Name,
			Dependencies: append([]MigrationKey(nil), definition.Dependencies...),
			Operations:   make([]Operation, len(definition.Operations)),
		}
		for index, operation := range definition.Operations {
			if isNilOperation(operation) {
				return nil, invalidReconstructorOperation(definition, index, "", errors.New("operation is nil"))
			}
			copy, kind, supported := cloneReconstructorOperation(operation)
			if !supported {
				return nil, invalidReconstructorOperation(definition, index, "", fmt.Errorf("operation type %T is not supported by state reconstruction", operation))
			}
			if copy.App() != definition.App {
				return nil, invalidReconstructorOperation(definition, index, kind, fmt.Errorf("operation app %q does not match migration app %q", copy.App(), definition.App))
			}
			snapshot.Operations[index] = copy
		}
		cloned[key] = snapshot
	}
	return cloned, nil
}

func collectLoadedStateGraph(
	graph *plannerGraph,
	definitions map[MigrationKey]Migration,
) (map[loadedModelIdentity][]loadedModelCreator, []loadedRelationDeclaration) {
	creators := make(map[loadedModelIdentity][]loadedModelCreator)
	declarations := make([]loadedRelationDeclaration, 0)
	for _, key := range graph.nodes {
		definition := definitions[key]
		for index, operation := range definition.Operations {
			switch value := operation.(type) {
			case CreateModel:
				identity := loadedModelIdentity{app: value.AppLabel, model: value.Model.Name}
				creators[identity] = append(creators[identity], loadedModelCreator{key: key, operationIndex: index, model: value.Model.Clone()})
				for _, field := range value.Model.Fields {
					if fieldContainsRelation(field) {
						declarations = append(declarations, loadedRelationDeclaration{key: key, operationIndex: index, operationKind: value.Kind(), source: identity, field: field.Clone()})
					}
				}
			case AddField:
				if fieldContainsRelation(value.Field) {
					declarations = append(declarations, loadedRelationDeclaration{key: key, operationIndex: index, operationKind: value.Kind(), source: loadedModelIdentity{app: value.AppLabel, model: value.ModelName}, field: value.Field.Clone()})
				}
			}
		}
	}
	sort.Slice(declarations, func(left, right int) bool { return loadedDeclarationLess(declarations[left], declarations[right]) })
	return creators, declarations
}

func loadedDeclarationLess(left, right loadedRelationDeclaration) bool {
	if left.key != right.key {
		return migrationKeyLess(left.key, right.key)
	}
	if left.operationIndex != right.operationIndex {
		return left.operationIndex < right.operationIndex
	}
	if left.source != right.source {
		if left.source.app != right.source.app {
			return left.source.app < right.source.app
		}
		return left.source.model < right.source.model
	}
	return left.field.Name < right.field.Name
}

func (r loadedStateReconstructor) validateChronology() error {
	duplicateIdentities := make([]loadedModelIdentity, 0)
	for identity, creators := range r.creators {
		if len(creators) > 1 {
			duplicateIdentities = append(duplicateIdentities, identity)
		}
	}
	sort.Slice(duplicateIdentities, func(left, right int) bool {
		return loadedIdentityLess(duplicateIdentities[left], duplicateIdentities[right])
	})
	if len(duplicateIdentities) != 0 {
		identity := duplicateIdentities[0]
		sorted := append([]loadedModelCreator(nil), r.creators[identity]...)
		sort.Slice(sorted, func(left, right int) bool {
			if sorted[left].key != sorted[right].key {
				return migrationKeyLess(sorted[left].key, sorted[right].key)
			}
			return sorted[left].operationIndex < sorted[right].operationIndex
		})
		creator := sorted[1]
		return invalidLoadedState(r.definitions[creator.key], creator.operationIndex, operationKindAt(r.definitions[creator.key], creator.operationIndex), fmt.Errorf("model %s.%s has multiple historical creators", identity.app, identity.model))
	}
	for _, declaration := range r.declarations {
		if declaration.field.Relation == nil {
			continue
		}
		sourceCreators := r.creators[declaration.source]
		if len(sourceCreators) != 1 {
			return r.declarationError(declaration, fmt.Errorf("source model %s.%s requires exactly one historical creator", declaration.source.app, declaration.source.model))
		}
		sourceCreator := sourceCreators[0]
		sourceOwnedByCreate := declaration.operationKind == (CreateModel{}).Kind() &&
			sourceCreator.key == declaration.key && sourceCreator.operationIndex == declaration.operationIndex
		if !sourceOwnedByCreate && !r.creatorVisibleBefore(sourceCreator, declaration) {
			if sourceCreator.key == declaration.key {
				return r.declarationError(declaration, fmt.Errorf("source model %s.%s is created later in the same migration", declaration.source.app, declaration.source.model))
			}
			return r.declarationError(declaration, fmt.Errorf("source creator %s.%s is not dependency ancestry of the relation migration", sourceCreator.key.App, sourceCreator.key.Name))
		}
		target := loadedModelIdentity{app: declaration.field.Relation.Target.AppLabel, model: declaration.field.Relation.Target.ModelName}
		creators := r.creators[target]
		if len(creators) != 1 {
			return r.declarationError(declaration, fmt.Errorf("target model %s.%s requires exactly one historical creator", target.app, target.model))
		}
		creator := creators[0]
		switch {
		case target == declaration.source && sourceOwnedByCreate:
			// The table and its self constraint are created by one operation.
		case r.creatorVisibleBefore(creator, declaration):
			// An earlier same-migration or explicit ancestor creator is visible.
		case creator.key == declaration.key:
			return r.declarationError(declaration, fmt.Errorf("target model %s.%s is created later in the same migration", target.app, target.model))
		case r.isAncestor(creator.key, declaration.key):
			// Explicit transitive dependency ancestry is visible.
		default:
			return r.declarationError(declaration, fmt.Errorf("target creator %s.%s is not dependency ancestry of the relation migration", creator.key.App, creator.key.Name))
		}
	}
	return nil
}

func (r loadedStateReconstructor) creatorVisibleBefore(creator loadedModelCreator, declaration loadedRelationDeclaration) bool {
	if creator.key == declaration.key {
		return creator.operationIndex < declaration.operationIndex
	}
	return r.isAncestor(creator.key, declaration.key)
}

func (r loadedStateReconstructor) declarationError(declaration loadedRelationDeclaration, cause error) error {
	return invalidLoadedState(r.definitions[declaration.key], declaration.operationIndex, declaration.operationKind, cause)
}

func (r loadedStateReconstructor) isAncestor(ancestor, node MigrationKey) bool {
	if ancestor == node {
		return false
	}
	ancestorPosition, ancestorExists := r.ancestors.positions[ancestor]
	nodePosition, nodeExists := r.ancestors.positions[node]
	if !ancestorExists || !nodeExists || nodePosition >= len(r.ancestors.sets) {
		return false
	}
	word := ancestorPosition / 64
	bit := uint(ancestorPosition % 64)
	return word < len(r.ancestors.sets[nodePosition]) && r.ancestors.sets[nodePosition][word]&(uint64(1)<<bit) != 0
}

func newLoadedAncestorIndex(graph *plannerGraph) loadedAncestorIndex {
	positions := make(map[MigrationKey]int, len(graph.nodes))
	for index, key := range graph.nodes {
		positions[key] = index
	}
	// Every topological order produces the same ancestor sets. Sorted graph
	// inputs make this queue deterministic without re-scanning all nodes or
	// imposing the planner's externally observable minimum-ready ordering.
	indegree := make([]int, len(graph.nodes))
	ordered := make([]int, 0, len(graph.nodes))
	for position, key := range graph.nodes {
		indegree[position] = len(graph.parents[key])
		if indegree[position] == 0 {
			ordered = append(ordered, position)
		}
	}
	for head := 0; head < len(ordered); head++ {
		for _, child := range graph.children[graph.nodes[ordered[head]]] {
			position := positions[child]
			indegree[position]--
			if indegree[position] == 0 {
				ordered = append(ordered, position)
			}
		}
	}
	if len(ordered) != len(graph.nodes) {
		// NewPlanner rejects cycles before this boundary. Preserve a closed
		// index if that invariant ever changes.
		return loadedAncestorIndex{positions: positions, sets: make([][]uint64, len(graph.nodes))}
	}
	words := (len(graph.nodes) + 63) / 64
	sets := make([][]uint64, len(graph.nodes))
	storage := make([]uint64, len(graph.nodes)*words)
	for _, position := range ordered {
		key := graph.nodes[position]
		start, end := position*words, (position+1)*words
		ancestors := storage[start:end:end]
		for _, parent := range graph.parents[key] {
			parentPosition := positions[parent]
			ancestors[parentPosition/64] |= uint64(1) << uint(parentPosition%64)
			for word := range ancestors {
				ancestors[word] |= sets[parentPosition][word]
			}
		}
		sets[position] = ancestors
	}
	return loadedAncestorIndex{positions: positions, sets: sets}
}

func loadedIdentityLess(left, right loadedModelIdentity) bool {
	if left.app != right.app {
		return left.app < right.app
	}
	return left.model < right.model
}

func (r loadedStateReconstructor) applyLoadedMigration(
	builder *loadedStateBuilder,
	migration Migration,
	direction Direction,
) error {
	return r.applyLoadedMigrationContext(context.Background(), builder, migration, direction)
}

func (r loadedStateReconstructor) applyLoadedMigrationContext(
	ctx context.Context,
	builder *loadedStateBuilder,
	migration Migration,
	direction Direction,
) error {
	step := PlanStep{Key: migration.Key(), Direction: direction}
	if ctx == nil {
		return executionContextError(step, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return executionContextError(step, err)
	}
	if err := r.beginLoadedMigration(builder, migration, direction); err != nil {
		return err
	}
	for _, index := range operationIndices(len(migration.Operations), direction) {
		if err := ctx.Err(); err != nil {
			return executionContextError(step, err)
		}
		if err := r.applyLoadedOperation(builder, migration, index, direction); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return executionContextError(step, err)
	}
	return nil
}

func (r loadedStateReconstructor) beginLoadedMigration(
	builder *loadedStateBuilder,
	migration Migration,
	direction Direction,
) error {
	if migration.App == "" || migration.Name == "" {
		return migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", errors.New("migration identity is empty"))
	}
	return nil
}

func (r loadedStateReconstructor) applyLoadedOperation(
	builder *loadedStateBuilder,
	migration Migration,
	index int,
	direction Direction,
) error {
	if index < 0 || index >= len(migration.Operations) {
		return migrationError(CategoryState, CodeInvalidState, direction, migration, index, "", errors.New("operation index is outside the migration"))
	}
	operation := migration.Operations[index]
	if isNilOperation(operation) {
		return migrationError(CategoryState, CodeInvalidState, direction, migration, index, "", errors.New("operation is nil"))
	}
	if operation.App() != migration.App {
		return migrationError(CategoryState, CodeInvalidState, direction, migration, index, operation.Kind(), fmt.Errorf("operation app %q does not match migration app %q", operation.App(), migration.App))
	}
	var err error
	switch value := operation.(type) {
	case CreateModel:
		if direction == DirectionForward {
			err = builder.createModel(value)
		} else {
			err = builder.deleteModel(value)
		}
	case AddField:
		if direction == DirectionForward {
			err = builder.addField(value)
		} else {
			err = builder.removeField(value)
		}
	case AlterField:
		err = builder.alterField(value, direction == DirectionBackward)
	default:
		err = fmt.Errorf("operation type %T is not supported by state reconstruction", operation)
	}
	if err != nil {
		return migrationError(CategoryState, CodeInvalidState, direction, migration, index, operation.Kind(), err)
	}
	return nil
}

func (builder *loadedStateBuilder) createModel(operation CreateModel) error {
	normalized, err := normalizedSingleModel(operation.AppLabel, operation.Model)
	if err != nil {
		return fmt.Errorf("normalize model: %w", err)
	}
	app, exists := builder.apps[operation.AppLabel]
	if !exists {
		app = &loadedStateApp{
			models:   make(map[string]*loadedStateModel),
			goNames:  make(map[string]string),
			dbTables: make(map[string]string),
		}
		builder.apps[operation.AppLabel] = app
	}
	if _, exists := app.models[normalized.Name]; exists {
		return fmt.Errorf("model %s.%s already exists", operation.AppLabel, normalized.Name)
	}
	if other, exists := app.goNames[normalized.GoName]; exists {
		return fmt.Errorf("model %s.%s Go name collides with %s", operation.AppLabel, normalized.Name, other)
	}
	if other, exists := app.dbTables[normalized.DBTable]; exists {
		return fmt.Errorf("model %s.%s table collides with %s", operation.AppLabel, normalized.Name, other)
	}
	primaryKey, err := exactAutoPrimaryKey(normalized)
	if err != nil {
		return err
	}
	model := newLoadedStateModel(normalized, primaryKey)
	app.models[normalized.Name] = model
	app.order = append(app.order, normalized.Name)
	app.goNames[normalized.GoName] = normalized.Name
	app.dbTables[normalized.DBTable] = normalized.Name
	identity := loadedModelIdentity{app: operation.AppLabel, model: normalized.Name}
	if reverse := builder.reverse[identity]; len(reverse) != 0 {
		return fmt.Errorf("model %s.%s is created after a relation reverse owner", identity.app, identity.model)
	}
	for _, field := range normalized.Fields {
		if err := builder.addRelation(identity, field); err != nil {
			return err
		}
	}
	return nil
}

func (builder *loadedStateBuilder) deleteModel(operation CreateModel) error {
	want, err := normalizedSingleModel(operation.AppLabel, operation.Model)
	if err != nil {
		return fmt.Errorf("normalize model: %w", err)
	}
	app, exists := builder.apps[operation.AppLabel]
	if !exists {
		return fmt.Errorf("model %s.%s does not exist", operation.AppLabel, want.Name)
	}
	actual, exists := app.models[want.Name]
	if !exists {
		return fmt.Errorf("model %s.%s does not exist", operation.AppLabel, want.Name)
	}
	if !modelEqual(actual.value, want) {
		return fmt.Errorf("model %s.%s does not match CreateModel state", operation.AppLabel, want.Name)
	}
	identity := loadedModelIdentity{app: operation.AppLabel, model: want.Name}
	for owner := range builder.incoming[identity] {
		if owner.source != identity {
			return fmt.Errorf("model %s.%s is still targeted by relation reverse owners", identity.app, identity.model)
		}
	}
	for _, field := range actual.value.Fields {
		if err := builder.removeRelation(identity, field); err != nil {
			return err
		}
	}
	if len(app.order) == 0 || app.order[len(app.order)-1] != want.Name {
		return fmt.Errorf("model %s.%s is not the latest model in its app", operation.AppLabel, want.Name)
	}
	app.order = app.order[:len(app.order)-1]
	delete(app.models, want.Name)
	delete(app.goNames, want.GoName)
	delete(app.dbTables, want.DBTable)
	if len(app.order) == 0 {
		delete(builder.apps, operation.AppLabel)
	}
	return nil
}

func newLoadedStateModel(value ir.Model, primaryKey ir.Field) *loadedStateModel {
	model := &loadedStateModel{
		value:      value.Clone(),
		primaryKey: primaryKey.Clone(),
		fieldNames: make(map[string]int, len(value.Fields)),
		goNames:    make(map[string]string, len(value.Fields)),
		columns:    make(map[string]string, len(value.Fields)),
	}
	for index, field := range model.value.Fields {
		model.fieldNames[field.Name] = index
		model.goNames[field.GoName] = field.Name
		model.columns[field.Column] = field.Name
	}
	return model
}

func (builder *loadedStateBuilder) addField(operation AddField) error {
	identity := loadedModelIdentity{app: operation.AppLabel, model: operation.ModelName}
	model, exists := builder.model(identity)
	if !exists {
		return fmt.Errorf("model %s.%s does not exist", identity.app, identity.model)
	}
	field, err := normalizeLoadedAddedField(operation.AppLabel, operation.Field)
	if err != nil {
		return fmt.Errorf("normalize added field: %w", err)
	}
	if _, exists := model.fieldNames[field.Name]; exists {
		return fmt.Errorf("field %s.%s.%s already exists", identity.app, identity.model, field.Name)
	}
	if other, exists := model.goNames[field.GoName]; exists {
		return fmt.Errorf("field %s.%s.%s Go name collides with %s", identity.app, identity.model, field.Name, other)
	}
	if other, exists := model.columns[field.Column]; exists {
		return fmt.Errorf("field %s.%s.%s column collides with %s", identity.app, identity.model, field.Name, other)
	}
	if reverse := builder.reverse[identity]; reverse != nil {
		if owner, exists := reverse[field.Name]; exists {
			return fmt.Errorf("field %s.%s.%s collides with reverse relation %s.%s.%s", identity.app, identity.model, field.Name, owner.source.app, owner.source.model, owner.field)
		}
	}
	position, err := addedFieldPosition(model.value.Fields, operation.BeforeField)
	if err != nil {
		return err
	}
	model.goNames[field.GoName] = field.Name
	model.columns[field.Column] = field.Name
	fields := model.value.Fields
	// Step materialization borrows the previous slice as immutable Before
	// authority. Appending cannot change its elements; inserting can.
	if position < len(fields) {
		fields = slices.Clone(fields)
	}
	model.value.Fields = slices.Insert(fields, position, field.Clone())
	for index := position; index < len(model.value.Fields); index++ {
		model.fieldNames[model.value.Fields[index].Name] = index
	}
	return builder.addRelation(identity, field)
}

func (builder *loadedStateBuilder) removeField(operation AddField) error {
	identity := loadedModelIdentity{app: operation.AppLabel, model: operation.ModelName}
	model, exists := builder.model(identity)
	if !exists {
		return fmt.Errorf("model %s.%s does not exist", identity.app, identity.model)
	}
	want, err := normalizeLoadedAddedField(operation.AppLabel, operation.Field)
	if err != nil {
		return fmt.Errorf("normalize removed field: %w", err)
	}
	index, exists := model.fieldNames[want.Name]
	if !exists {
		return fmt.Errorf("field %s.%s.%s does not exist", identity.app, identity.model, want.Name)
	}
	actual := model.value.Fields[index]
	if !fieldEqual(actual, want) {
		return fmt.Errorf("field %s.%s.%s does not match AddField state", identity.app, identity.model, want.Name)
	}
	if err := validateAddedFieldPosition(model.value.Fields, index, operation.BeforeField); err != nil {
		return err
	}
	if err := builder.removeRelation(identity, actual); err != nil {
		return err
	}
	// Do not shift or clear the borrowed Before snapshot's backing array.
	fields := model.value.Fields
	model.value.Fields = append(fields[:index:index], fields[index+1:]...)
	for current := index; current < len(model.value.Fields); current++ {
		model.fieldNames[model.value.Fields[current].Name] = current
	}
	delete(model.fieldNames, actual.Name)
	delete(model.goNames, actual.GoName)
	delete(model.columns, actual.Column)
	return nil
}

func normalizeLoadedAddedField(appLabel string, value ir.Field) (ir.Field, error) {
	syntheticName := "_godj_loaded_pk"
	syntheticGoName := "GodjLoadedPK"
	syntheticColumn := "_godj_loaded_pk"
	for value.Name == syntheticName || value.GoName == syntheticGoName || value.Column == syntheticColumn {
		syntheticName += "_"
		syntheticGoName += "X"
		syntheticColumn += "_"
	}
	schema, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      appLabel,
		Models: []ir.Model{{
			Name:    "_godj_loaded_validation",
			GoName:  "GodjLoadedValidation",
			DBTable: "_godj_loaded_validation",
			Fields: []ir.Field{
				{Name: syntheticName, GoName: syntheticGoName, Column: syntheticColumn, Kind: ir.FieldAuto, PrimaryKey: true},
				value.Clone(),
			},
		}},
	})
	if err != nil {
		return ir.Field{}, err
	}
	return schema.Models[0].Fields[1].Clone(), nil
}

func (builder *loadedStateBuilder) addRelation(source loadedModelIdentity, field ir.Field) error {
	if !fieldContainsRelation(field) {
		return nil
	}
	if field.Relation == nil {
		return fmt.Errorf("relation field %s.%s.%s has no relation metadata", source.app, source.model, field.Name)
	}
	target := loadedModelIdentity{app: field.Relation.Target.AppLabel, model: field.Relation.Target.ModelName}
	targetModel, exists := builder.model(target)
	if !exists {
		return fmt.Errorf("historical target model %s.%s is not visible", target.app, target.model)
	}
	if targetModel.primaryKey.Kind != ir.FieldAuto || !targetModel.primaryKey.PrimaryKey || targetModel.primaryKey.Nullable {
		return fmt.Errorf("historical target model %s.%s requires exactly one non-null AutoField primary key", target.app, target.model)
	}
	name := field.Relation.Reverse.Name
	if name != "" {
		if _, exists := targetModel.fieldNames[name]; exists {
			return fmt.Errorf("reverse relation %s.%s.%s collides with a target field", target.app, target.model, name)
		}
		owners := builder.reverse[target]
		if owners == nil {
			owners = make(map[string]loadedReverseOwner)
			builder.reverse[target] = owners
		}
		if owner, exists := owners[name]; exists {
			return fmt.Errorf("reverse relation %s.%s.%s collides with %s.%s.%s", target.app, target.model, name, owner.source.app, owner.source.model, owner.field)
		}
		owners[name] = loadedReverseOwner{source: source, field: field.Name}
	}
	owners := builder.incoming[target]
	if owners == nil {
		owners = make(map[loadedReverseOwner]struct{})
		builder.incoming[target] = owners
	}
	owner := loadedReverseOwner{source: source, field: field.Name}
	if _, duplicate := owners[owner]; duplicate {
		return fmt.Errorf("relation field %s.%s.%s has a duplicate incoming owner", source.app, source.model, field.Name)
	}
	owners[owner] = struct{}{}
	builder.relationCount++
	return nil
}

func (builder *loadedStateBuilder) removeRelation(source loadedModelIdentity, field ir.Field) error {
	if !fieldContainsRelation(field) {
		return nil
	}
	if field.Relation == nil || builder.relationCount == 0 {
		return fmt.Errorf("relation field %s.%s.%s has inconsistent loaded state", source.app, source.model, field.Name)
	}
	target := loadedModelIdentity{app: field.Relation.Target.AppLabel, model: field.Relation.Target.ModelName}
	name := field.Relation.Reverse.Name
	if name != "" {
		owners := builder.reverse[target]
		owner, exists := owners[name]
		if !exists || owner.source != source || owner.field != field.Name {
			return fmt.Errorf("reverse relation %s.%s.%s has inconsistent loaded owner", target.app, target.model, name)
		}
		delete(owners, name)
		if len(owners) == 0 {
			delete(builder.reverse, target)
		}
	}
	owners := builder.incoming[target]
	owner := loadedReverseOwner{source: source, field: field.Name}
	if _, exists := owners[owner]; !exists {
		return fmt.Errorf("relation field %s.%s.%s has an inconsistent incoming owner", source.app, source.model, field.Name)
	}
	delete(owners, owner)
	if len(owners) == 0 {
		delete(builder.incoming, target)
	}
	builder.relationCount--
	return nil
}

func (r loadedStateReconstructor) validateReadiness(ctx context.Context) error {
	steps, err := r.fullForwardProjection()
	if err != nil {
		return err
	}
	builder := newLoadedStateBuilder()
	for _, step := range steps {
		if err := r.applyLoadedMigrationContext(ctx, builder, r.definitions[step.Key], DirectionForward); err != nil {
			return err
		}
	}
	if _, err := builder.projectState(); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	for index := len(steps) - 1; index >= 0; index-- {
		if err := r.applyLoadedMigrationContext(ctx, builder, r.definitions[steps[index].Key], DirectionBackward); err != nil {
			return err
		}
	}
	if !builder.empty() {
		return invalidLoadedState(Migration{}, NoOperation, "", errors.New("full reverse readiness did not reconstruct the empty state"))
	}
	return nil
}

func (r loadedStateReconstructor) Reconstruct(request StateRequest) (ProjectState, error) {
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

func (r loadedStateReconstructor) fullForwardProjection() ([]PlanStep, error) {
	return historicalFullForwardProjection(r.planner)
}

func historicalFullForwardProjection(planner Planner) ([]PlanStep, error) {
	graph := planner.graph
	if graph == nil {
		graph = emptyPlannerGraph()
	}
	steps, err := historicalTargetProjection(planner, graph.appLeaves())
	if err != nil {
		return nil, err
	}
	if len(steps) != len(graph.nodes) {
		return nil, invalidLoadedState(Migration{}, NoOperation, "", fmt.Errorf(
			"full historical projection covers %d of %d graph nodes",
			len(steps), len(graph.nodes),
		))
	}
	seen := make(map[MigrationKey]struct{}, len(steps))
	for _, step := range steps {
		if step.Direction != DirectionForward {
			return nil, invalidLoadedState(
				Migration{App: step.Key.App, Name: step.Key.Name},
				NoOperation,
				"",
				errors.New("full historical projection contains a non-forward step"),
			)
		}
		if _, exists := seen[step.Key]; exists {
			return nil, invalidLoadedState(
				Migration{App: step.Key.App, Name: step.Key.Name},
				NoOperation,
				"",
				errors.New("full historical projection repeats a graph node"),
			)
		}
		seen[step.Key] = struct{}{}
	}
	for _, key := range graph.nodes {
		if _, exists := seen[key]; !exists {
			return nil, invalidLoadedState(
				Migration{App: key.App, Name: key.Name},
				NoOperation,
				"",
				errors.New("full historical projection omits a graph node"),
			)
		}
	}
	return steps, nil
}

func (r loadedStateReconstructor) targetProjection(targets []MigrationKey) ([]PlanStep, error) {
	return historicalTargetProjection(r.planner, targets)
}

func (r loadedStateReconstructor) replay(steps []PlanStep) (ProjectState, error) {
	builder := newLoadedStateBuilder()
	for _, step := range steps {
		migration, exists := r.definitions[step.Key]
		if !exists {
			return EmptyProjectState(), invalidLoadedState(Migration{App: step.Key.App, Name: step.Key.Name}, NoOperation, "", errors.New("historical projection has no migration definition"))
		}
		if err := r.applyLoadedMigration(builder, migration, DirectionForward); err != nil {
			return EmptyProjectState(), err
		}
	}
	state, err := builder.projectState()
	if err != nil {
		return EmptyProjectState(), invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	return state, nil
}

func (r loadedStateReconstructor) builderForApplied(
	ctx context.Context,
	planner Planner,
	applied AppliedState,
) (*loadedStateBuilder, error) {
	steps, err := historicalFullForwardProjection(planner)
	if err != nil {
		return nil, err
	}
	builder := newLoadedStateBuilder()
	for _, step := range steps {
		if _, exists := applied.keys[step.Key]; !exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, executionContextError(step, err)
		}
		migration, exists := r.definitions[step.Key]
		if !exists {
			return nil, invalidLoadedState(
				Migration{App: step.Key.App, Name: step.Key.Name},
				NoOperation,
				"",
				errors.New("applied historical projection has no migration definition"),
			)
		}
		if err := r.applyLoadedMigration(builder, migration, DirectionForward); err != nil {
			return nil, err
		}
	}
	return builder, nil
}

func (r loadedStateReconstructor) dryLoadedPlan(
	ctx context.Context,
	builder *loadedStateBuilder,
	plan []PlanStep,
) ([]loadedPlanStep, error) {
	seen := make(map[MigrationKey]struct{}, len(plan))
	var direction Direction
	prepared := make([]loadedPlanStep, 0, len(plan))
	for index, step := range plan {
		if !validMigrationKey(step.Key) ||
			(step.Direction != DirectionForward && step.Direction != DirectionBackward) {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("actual plan[%d] has an invalid key or direction", index),
			)
		}
		if _, exists := seen[step.Key]; exists {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("actual plan[%d] duplicates migration %s.%s", index, step.Key.App, step.Key.Name),
			)
		}
		seen[step.Key] = struct{}{}
		if _, exists := r.definitions[step.Key]; !exists {
			return nil, executionPlanError(
				CodeInvalidExecutionPlan,
				step,
				fmt.Errorf("actual plan[%d] has no sealed definition", index),
			)
		}
		if direction == "" {
			direction = step.Direction
		} else if direction != step.Direction {
			return nil, executionPlanError(
				CodeMixedDirections,
				step,
				fmt.Errorf("actual plan[%d] direction %q differs from plan direction %q", index, step.Direction, direction),
			)
		}
		if err := ctx.Err(); err != nil {
			return nil, executionContextError(step, err)
		}
		materialized, err := r.materializeLoadedStep(ctx, builder, step, false)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, loadedPlanStep{
			step:         step,
			requirements: materialized.requirements,
			seal:         materialized.seal,
		})
	}
	return prepared, nil
}

func (r loadedStateReconstructor) materializeLoadedStep(
	ctx context.Context,
	builder *loadedStateBuilder,
	step PlanStep,
	includeExecutionState bool,
) (loadedMaterializedStep, error) {
	migration, exists := r.definitions[step.Key]
	if !exists {
		return loadedMaterializedStep{}, executionPlanError(
			CodeInvalidExecutionPlan,
			step,
			errors.New("actual plan step has no sealed definition"),
		)
	}
	indices := operationIndices(len(migration.Operations), step.Direction)
	if len(indices) > loadedDerivedIntentMaxOperations {
		return loadedMaterializedStep{}, migrationError(
			CategoryState, CodeInvalidState, step.Direction, migration,
			NoOperation, "", fmt.Errorf(
				"derived relation intent has %d operations, maximum %d",
				len(indices), loadedDerivedIntentMaxOperations,
			),
		)
	}
	budget := &loadedDerivedIntentBudget{Budget: irresource.New(irresource.Limits{
		Fields: loadedDerivedIntentMaxFields, StringBytes: loadedDerivedIntentMaxStringBytes,
		Nodes: loadedDerivedIntentMaxNodes, Bytes: loadedDerivedIntentMaxAggregateBytes,
	})}
	if err := budget.ConsumeNodes("transition", 1); err != nil {
		return loadedMaterializedStep{}, loadedDerivedIntentError(step, migration, NoOperation, "", err)
	}
	if err := budget.ConsumeString("transition.migration.app", step.Key.App); err != nil {
		return loadedMaterializedStep{}, loadedDerivedIntentError(step, migration, NoOperation, "", err)
	}
	if err := budget.ConsumeString("transition.migration.name", step.Key.Name); err != nil {
		return loadedMaterializedStep{}, loadedDerivedIntentError(step, migration, NoOperation, "", err)
	}
	if err := budget.ConsumeNodes("operations", len(indices)); err != nil {
		return loadedMaterializedStep{}, loadedDerivedIntentError(step, migration, NoOperation, "", err)
	}

	beforeFormat := StateFormatVersion
	if err := r.beginLoadedMigration(builder, migration, step.Direction); err != nil {
		return loadedMaterializedStep{}, err
	}

	operationViews := make([]loadedOperationView, 0, len(indices))
	var requirements loadedRelationRequirements
	for intentIndex, operationIndex := range indices {
		if err := ctx.Err(); err != nil {
			return loadedMaterializedStep{}, executionContextError(step, err)
		}
		operation := migration.Operations[operationIndex]
		if isNilOperation(operation) {
			return loadedMaterializedStep{}, migrationError(
				CategoryState, CodeInvalidState, step.Direction, migration,
				operationIndex, "", errors.New("operation is nil"),
			)
		}
		appLabel, modelName := operationSourceModel(operation)
		beforeModel, beforeExists := loadedBuilderModelView(builder, loadedModelIdentity{app: appLabel, model: modelName})
		changedRelationFields := operationRelationFieldViews(operation)
		if err := r.applyLoadedOperation(builder, migration, operationIndex, step.Direction); err != nil {
			return loadedMaterializedStep{}, err
		}
		afterModel, afterExists := loadedBuilderModelView(builder, loadedModelIdentity{app: appLabel, model: modelName})
		sourceModel := afterModel
		sourceExists := afterExists
		if step.Direction == DirectionBackward {
			if _, choicesOnly := operation.(AlterField); !choicesOnly {
				sourceModel = beforeModel
				sourceExists = beforeExists
			}
		}
		targets, relatedModels, err := loadedBuilderRelationGraph(ctx, builder, migration, operationIndex, step.Direction, sourceModel)
		if err != nil {
			return loadedMaterializedStep{}, err
		}
		if len(targets) != 0 && !sourceExists {
			return loadedMaterializedStep{}, migrationError(
				CategoryState, CodeInvalidState, step.Direction, migration,
				operationIndex, operation.Kind(), fmt.Errorf("relation source model %s.%s is missing", appLabel, modelName),
			)
		}
		sourceFields := make([]ir.Field, len(targets))
		for targetIndex := range targets {
			sourceField, fieldExists := loadedModelFieldView(sourceModel, targets[targetIndex].sourceFieldName)
			if !fieldExists || !fieldContainsRelation(sourceField) {
				return loadedMaterializedStep{}, migrationError(
					CategoryState, CodeInvalidState, step.Direction, migration,
					operationIndex, operation.Kind(), fmt.Errorf("relation source field %s.%s.%s is missing", appLabel, modelName, targets[targetIndex].sourceFieldName),
				)
			}
			sourceFields[targetIndex] = sourceField
		}
		if err := budget.scanOperation(
			intentIndex,
			loadedOptionalModelView(beforeModel, beforeExists),
			loadedOptionalModelView(afterModel, afterExists),
			sourceFields,
			targets,
			relatedModels,
		); err != nil {
			return loadedMaterializedStep{}, loadedDerivedIntentError(step, migration, operationIndex, operation.Kind(), err)
		}
		kind, err := loadedBackendOperationKind(operation, step.Direction)
		if err != nil {
			return loadedMaterializedStep{}, migrationError(
				CategoryState, CodeInvalidState, step.Direction, migration,
				operationIndex, operation.Kind(), err,
			)
		}
		// Capability bits describe the operation's mutation, not retained
		// relations that are carried only to seal the complete model boundary.
		// A scalar Add/Remove on a relation-bearing model therefore transports
		// target authority without requiring a relation Add/Remove capability.
		requirements |= loadedRequirementsForSourceFields(kind, changedRelationFields)
		operationViews = append(operationViews, loadedOperationView{
			index:         operationIndex,
			operation:     operation,
			appLabel:      appLabel,
			before:        beforeModel,
			beforeExists:  beforeExists,
			after:         afterModel,
			afterExists:   afterExists,
			sourceFields:  sourceFields,
			targets:       targets,
			relatedModels: relatedModels,
		})
	}
	if err := validateLoadedRelationMutationAuthorities(step, migration, operationViews); err != nil {
		return loadedMaterializedStep{}, err
	}

	intent := loadedRelationIntent{}
	if len(operationViews) != 0 {
		intent.operations = make([]loadedRelationOperation, len(operationViews))
		for viewIndex := range operationViews {
			view := operationViews[viewIndex]
			kind, err := loadedBackendOperationKind(view.operation, step.Direction)
			if err != nil {
				return loadedMaterializedStep{}, migrationError(
					CategoryState, CodeInvalidState, step.Direction, migration,
					view.index, view.operation.Kind(), err,
				)
			}
			backendTargets := make([]loadedRelationBackendTarget, len(view.targets))
			for targetIndex := range view.targets {
				backendTargets[targetIndex] = loadedRelationBackendTarget{
					sourceField: view.sourceFields[targetIndex].Clone(),
					targetModel: view.targets[targetIndex].targetModel.Clone(),
					targetKey:   view.targets[targetIndex].targetPrimaryKey.Clone(),
				}
			}
			intent.operations[viewIndex] = loadedRelationOperation{
				operationIndex: view.index,
				kind:           kind,
				before:         loadedOptionalModel(view.before, view.beforeExists),
				after:          loadedOptionalModel(view.after, view.afterExists),
				targets:        backendTargets,
				relatedModels:  migrationgraph.CloneMigrationModels(view.relatedModels),
			}
		}
	}

	stateUnchanged := includeExecutionState && len(indices) == 0
	var after ProjectState
	if includeExecutionState && !stateUnchanged {
		var err error
		after, err = builder.projectState()
		if err != nil {
			return loadedMaterializedStep{}, migrationError(
				CategoryState, CodeInvalidState, step.Direction, migration,
				NoOperation, "", err,
			)
		}
	}
	seal, err := sealLoadedStep(loadedStepSealPayload{
		Direction:    step.Direction,
		App:          step.Key.App,
		Migration:    step.Key.Name,
		BeforeFormat: beforeFormat,
		AfterFormat:  StateFormatVersion,
		Operations:   loadedRelationSealOperations(intent),
	})
	if err != nil {
		return loadedMaterializedStep{}, migrationError(
			CategoryState, CodeInvalidState, step.Direction, migration,
			NoOperation, "", fmt.Errorf("seal loaded migration step: %w", err),
		)
	}
	return loadedMaterializedStep{
		prepared: preparedPlanStep{
			step:      step,
			migration: migration,
			after:     after,
		},
		execution:      operationViews,
		intent:         intent,
		requirements:   requirements,
		stateUnchanged: stateUnchanged,
		seal:           seal,
	}, nil
}

// validateLoadedRelationMutationAuthorities validates the complete relation
// graph in both the whole-plan dry pass and execution rematerialization. The
// backend repeats this check against detached, sealed intent before any I/O.
func validateLoadedRelationMutationAuthorities(
	step PlanStep,
	migration Migration,
	views []loadedOperationView,
) error {
	for _, view := range views {
		kind, err := loadedBackendOperationKind(view.operation, step.Direction)
		if err != nil {
			return loadedRelationCapabilityError(step, migration, err.Error())
		}
		targets := make([]migrationgraph.MigrationTarget, len(view.targets))
		for index, target := range view.targets {
			targets[index] = migrationgraph.MigrationTarget{
				SourceField: view.sourceFields[index], TargetModel: target.targetModel,
				TargetKey: target.targetPrimaryKey,
			}
		}
		_, err = migrationgraph.ResolveMigrationGraph(view.appLabel, migrationgraph.MigrationOperation{
			OperationIndex: view.index, Kind: migrationgraph.MigrationOperationKind(kind),
			Before:  loadedOptionalModelView(view.before, view.beforeExists),
			After:   loadedOptionalModelView(view.after, view.afterExists),
			Targets: targets, RelatedModels: view.relatedModels,
		})
		if err != nil {
			return loadedRelationCapabilityError(step, migration, err.Error())
		}
	}
	return nil
}

func loadedBuilderModelView(builder *loadedStateBuilder, identity loadedModelIdentity) (ir.Model, bool) {
	model, exists := builder.model(identity)
	if !exists {
		return ir.Model{}, false
	}
	return model.value, true
}

// loadedBuilderRelationGraph borrows an operation's exact source boundary
// and the visible historical target closure. The source override is essential
// for self creation and backward removal, where the mutable builder is already
// at the opposite side of that operation. All other vertices are unchanged by
// the single-model operation and must actually exist in the builder.
func loadedBuilderRelationGraph(
	ctx context.Context,
	builder *loadedStateBuilder,
	migration Migration,
	operationIndex int,
	direction Direction,
	source ir.Model,
) ([]loadedRelationTargetView, []migrationgraph.MigrationModel, error) {
	operation := migration.Operations[operationIndex]
	fail := func(err error) ([]loadedRelationTargetView, []migrationgraph.MigrationModel, error) {
		return nil, nil, migrationError(CategoryState, CodeInvalidState, direction, migration,
			operationIndex, operation.Kind(), err)
	}
	app, _ := operationSourceModel(operation)
	sourceIdentity := ir.ModelIdentity{AppLabel: app, ModelName: source.Name}
	direct := map[ir.ModelIdentity]bool{sourceIdentity: true}
	fields := make([]ir.Field, 0)
	for _, field := range source.Fields {
		if !fieldContainsRelation(field) {
			continue
		}
		if field.Relation == nil || field.Kind != ir.FieldForeignKey {
			return fail(fmt.Errorf("relation field %q has invalid metadata", field.Name))
		}
		fields = append(fields, field)
		direct[field.Relation.Target] = true
	}
	if len(fields) == 0 {
		return nil, nil, nil
	}
	budget := irresource.New(irresource.Limits{
		Fields: loadedDerivedIntentMaxFields, StringBytes: loadedDerivedIntentMaxStringBytes,
		Nodes: loadedDerivedIntentMaxNodes, Bytes: loadedDerivedIntentMaxAggregateBytes,
	})
	scan := func(identity ir.ModelIdentity, model ir.Model) error {
		if err := budget.ConsumeString("relation_graph.app", identity.AppLabel); err != nil {
			return err
		}
		return budget.ScanModel("relation_graph.model", model)
	}
	if err := scan(sourceIdentity, source); err != nil {
		return fail(err)
	}
	models := map[ir.ModelIdentity]ir.Model{sourceIdentity: source}
	queue := []ir.ModelIdentity{sourceIdentity}
	for position := 0; position < len(queue); position++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		model := models[queue[position]]
		for _, field := range model.Fields {
			if field.Kind != ir.FieldForeignKey {
				continue
			}
			if field.Relation == nil {
				return fail(fmt.Errorf("relation field %q has no metadata", field.Name))
			}
			identity := field.Relation.Target
			if _, exists := models[identity]; exists {
				continue
			}
			if len(models) >= loadedDerivedIntentMaxTargets {
				return fail(fmt.Errorf("historical relation graph exceeds %d models", loadedDerivedIntentMaxTargets))
			}
			target, exists := loadedBuilderModelView(builder, loadedModelIdentity{app: identity.AppLabel, model: identity.ModelName})
			if !exists {
				return fail(fmt.Errorf("historical target model %s.%s is not visible", identity.AppLabel, identity.ModelName))
			}
			if err := scan(identity, target); err != nil {
				return fail(err)
			}
			if _, err := exactAutoPrimaryKeyView(target); err != nil {
				return fail(err)
			}
			models[identity] = target
			queue = append(queue, identity)
		}
	}
	targets := make([]loadedRelationTargetView, 0, len(fields))
	for _, field := range fields {
		target := models[field.Relation.Target]
		key, err := exactAutoPrimaryKeyView(target)
		if err != nil {
			return fail(err)
		}
		targets = append(targets, loadedRelationTargetView{
			sourceFieldName: field.Name, targetModel: target, targetPrimaryKey: key,
		})
	}
	related := make([]migrationgraph.MigrationModel, 0)
	for identity, model := range models {
		if !direct[identity] {
			related = append(related, migrationgraph.MigrationModel{AppLabel: identity.AppLabel, Model: model})
		}
	}
	sort.Slice(related, func(left, right int) bool {
		if related[left].AppLabel != related[right].AppLabel {
			return related[left].AppLabel < related[right].AppLabel
		}
		return related[left].Model.Name < related[right].Model.Name
	})
	return targets, related, nil
}

func loadedModelFieldView(model ir.Model, name string) (ir.Field, bool) {
	for index := range model.Fields {
		if model.Fields[index].Name == name {
			return model.Fields[index], true
		}
	}
	return ir.Field{}, false
}

func loadedOptionalModelView(model ir.Model, exists bool) ir.Model {
	if !exists {
		return ir.Model{}
	}
	return model
}

func loadedDerivedIntentError(
	step PlanStep,
	migration Migration,
	operationIndex int,
	operationKind string,
	err error,
) error {
	return migrationError(
		CategoryState,
		CodeInvalidState,
		step.Direction,
		migration,
		operationIndex,
		operationKind,
		fmt.Errorf("derived relation intent resource limit: %w", err),
	)
}

func (budget *loadedDerivedIntentBudget) scanOperation(
	operationIndex int,
	before ir.Model,
	after ir.Model,
	sourceFields []ir.Field,
	targets []loadedRelationTargetView,
	relatedModels []migrationgraph.MigrationModel,
) error {
	prefix := fmt.Sprintf("operations[%d]", operationIndex)
	if err := budget.ScanModel(prefix+".before", before); err != nil {
		return err
	}
	if err := budget.ScanModel(prefix+".after", after); err != nil {
		return err
	}
	if len(targets) > loadedDerivedIntentMaxTargets {
		return fmt.Errorf("%s has %d targets, maximum %d", prefix, len(targets), loadedDerivedIntentMaxTargets)
	}
	if len(sourceFields) != len(targets) {
		return fmt.Errorf("%s target source-field count is inconsistent", prefix)
	}
	if err := budget.ConsumeNodes(prefix+".targets", len(targets)); err != nil {
		return err
	}
	for targetIndex := range targets {
		targetPrefix := fmt.Sprintf("%s.targets[%d]", prefix, targetIndex)
		if err := budget.ScanField(targetPrefix+".source_field", sourceFields[targetIndex]); err != nil {
			return err
		}
		if err := budget.ScanModel(targetPrefix+".target_model", targets[targetIndex].targetModel); err != nil {
			return err
		}
		if err := budget.ScanField(targetPrefix+".target_key", targets[targetIndex].targetPrimaryKey); err != nil {
			return err
		}
	}
	if err := budget.ConsumeNodes(prefix+".related_models", len(relatedModels)); err != nil {
		return err
	}
	for index, model := range relatedModels {
		path := fmt.Sprintf("%s.related_models[%d]", prefix, index)
		if err := budget.ConsumeString(path+".app", model.AppLabel); err != nil {
			return err
		}
		if err := budget.ScanModel(path+".model", model.Model); err != nil {
			return err
		}
	}
	return nil
}

func loadedBackendOperationKind(operation Operation, direction Direction) (loadedRelationOperationKind, error) {
	switch operation.(type) {
	case CreateModel:
		if direction == DirectionForward {
			return loadedRelationCreateModel, nil
		}
		return loadedRelationDeleteModel, nil
	case AddField:
		if direction == DirectionForward {
			return loadedRelationAddField, nil
		}
		return loadedRelationRemoveField, nil
	case AlterField:
		return loadedRelationAlterField, nil
	default:
		return 0, fmt.Errorf("operation type %T is not supported by the loaded relation lifecycle", operation)
	}
}

func loadedRequirementsForSourceFields(
	kind loadedRelationOperationKind,
	fields []ir.Field,
) loadedRelationRequirements {
	if kind == loadedRelationAlterField {
		return loadedRequiresAlterFieldChoices
	}
	if len(fields) == 0 {
		return 0
	}
	switch kind {
	case loadedRelationCreateModel, loadedRelationDeleteModel:
		return loadedRequiresCreateModelForeignKeys
	case loadedRelationAddField:
		if fields[0].Nullable {
			return loadedRequiresAddNullableForeignKey
		}
		return loadedRequiresAddRequiredForeignKeyToEmptyTable
	case loadedRelationRemoveField:
		return loadedRequiresRemoveForeignKey
	default:
		return 0
	}
}

func loadedOptionalModel(model ir.Model, exists bool) ir.Model {
	if !exists {
		return ir.Model{}
	}
	return model.Clone()
}

func loadedSparseProjectState(app string, model ir.Model, exists bool) ProjectState {
	state := EmptyProjectState()
	if !exists {
		return state
	}
	state.apps[app] = ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      app,
		Models:        []ir.Model{model.Clone()},
	}
	return state
}

func loadedRelationSealOperations(intent loadedRelationIntent) []loadedRelationOperationSeal {
	operations := make([]loadedRelationOperationSeal, len(intent.operations))
	for operationIndex := range intent.operations {
		operation := intent.operations[operationIndex]
		targets := make([]loadedRelationTargetSeal, len(operation.targets))
		for targetIndex := range operation.targets {
			target := operation.targets[targetIndex]
			targets[targetIndex] = loadedRelationTargetSeal{
				SourceField: target.sourceField.Clone(),
				TargetModel: target.targetModel.Clone(),
				TargetKey:   target.targetKey.Clone(),
			}
		}
		operations[operationIndex] = loadedRelationOperationSeal{
			OperationIndex: operation.operationIndex,
			Kind:           operation.kind,
			Before:         operation.before.Clone(),
			After:          operation.after.Clone(),
			Targets:        targets,
			RelatedModels:  migrationgraph.CloneMigrationModels(operation.relatedModels),
		}
	}
	return operations
}

func sealLoadedStep(payload loadedStepSealPayload) ([sha256.Size]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func operationSourceModel(operation Operation) (string, string) {
	switch value := operation.(type) {
	case CreateModel:
		return value.AppLabel, value.Model.Name
	case AddField:
		return value.AppLabel, value.ModelName
	case AlterField:
		return value.AppLabel, value.ModelName
	default:
		return operation.App(), ""
	}
}

func operationRelationFieldViews(operation Operation) []ir.Field {
	fields := make([]ir.Field, 0)
	appendRelations := func(values []ir.Field) {
		for index := range values {
			if fieldContainsRelation(values[index]) {
				fields = append(fields, values[index])
			}
		}
	}
	switch value := operation.(type) {
	case CreateModel:
		appendRelations(value.Model.Fields)
	case AddField:
		if fieldContainsRelation(value.Field) {
			fields = append(fields, value.Field)
		}
	}
	return fields
}

func exactAutoPrimaryKey(model ir.Model) (ir.Field, error) {
	key, err := exactAutoPrimaryKeyView(model)
	if err != nil {
		return ir.Field{}, err
	}
	return key.Clone(), nil
}

func exactAutoPrimaryKeyView(model ir.Model) (ir.Field, error) {
	var key ir.Field
	count := 0
	for index := range model.Fields {
		if model.Fields[index].PrimaryKey {
			key = model.Fields[index]
			count++
		}
	}
	if count != 1 {
		return ir.Field{}, fmt.Errorf("historical target model %s requires exactly one primary key", model.Name)
	}
	if key.Kind != ir.FieldAuto || key.Nullable {
		return ir.Field{}, fmt.Errorf("historical target model %s primary key must be a non-nullable AutoField", model.Name)
	}
	return key, nil
}

func operationKindAt(migration Migration, index int) string {
	if index < 0 || index >= len(migration.Operations) || isNilOperation(migration.Operations[index]) {
		return ""
	}
	return migration.Operations[index].Kind()
}

func invalidLoadedState(migration Migration, operationIndex int, operation string, cause error) error {
	return migrationError(CategoryState, CodeInvalidState, DirectionForward, migration, operationIndex, operation, cause)
}

type loadedResourceViolation struct {
	migration     Migration
	operation     int
	operationKind string
	path          string
	reason        string
}

type loadedResourceScanCounts struct {
	definitions uint64
	operations  uint64
	fields      uint64
}

type loadedResourceBudget struct {
	nodes        uint64
	bytes        uint64
	nodeOverflow bool
	byteOverflow bool
	best         *loadedResourceViolation
	scan         loadedResourceScanCounts
}

// validateLoadedDefinitionResources is deliberately the first operation over
// caller-owned definition contents at the loader-authorized boundary. It does
// not sort, clone, normalize, or canonicalize any slice or nested IR value.
func validateLoadedDefinitionResources(definitions []Migration) error {
	_, err := scanLoadedDefinitionResources(definitions)
	return err
}

func scanLoadedDefinitionResources(definitions []Migration) (loadedResourceScanCounts, error) {
	if len(definitions) > maxLoadedDefinitions {
		return loadedResourceScanCounts{}, invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition count exceeds resource limit"))
	}
	budget := loadedResourceBudget{}
	loadedConsumeNodes(&budget, uint64(len(definitions)))
	for definitionIndex := range definitions {
		if budget.nodeOverflow {
			break
		}
		budget.scan.definitions++
		definition := definitions[definitionIndex]
		definitionStart := budget.bytes
		loadedConsumeString(&budget, definition, NoOperation, "", "app", definition.App, false)
		loadedConsumeString(&budget, definition, NoOperation, "", "name", definition.Name, false)
		if len(definition.Dependencies) > maxLoadedDependencies {
			loadedConsiderViolation(&budget, loadedResourceViolation{migration: definition, operation: NoOperation, path: "dependencies", reason: "dependency_count"})
		} else {
			loadedConsumeNodes(&budget, uint64(len(definition.Dependencies)))
			if budget.nodeOverflow {
				break
			}
			for index := range definition.Dependencies {
				loadedConsumeString(&budget, definition, NoOperation, "", fmt.Sprintf("dependencies[%d].app", index), definition.Dependencies[index].App, false)
				loadedConsumeString(&budget, definition, NoOperation, "", fmt.Sprintf("dependencies[%d].name", index), definition.Dependencies[index].Name, false)
			}
		}
		if len(definition.Operations) > maxLoadedOperations {
			loadedConsiderViolation(&budget, loadedResourceViolation{migration: definition, operation: NoOperation, path: "operations", reason: "operation_count"})
		} else {
			loadedConsumeNodes(&budget, uint64(len(definition.Operations)))
			if budget.nodeOverflow {
				break
			}
			for operationIndex, operation := range definition.Operations {
				if budget.nodeOverflow {
					break
				}
				loadedScanOperationResource(&budget, definition, operationIndex, operation)
			}
		}
		if !budget.byteOverflow && budget.bytes >= definitionStart && budget.bytes-definitionStart > maxLoadedDefinitionBytes {
			loadedConsiderViolation(&budget, loadedResourceViolation{migration: definition, operation: NoOperation, path: "definition", reason: "definition_bytes"})
		}
	}
	if budget.nodeOverflow || budget.nodes > maxLoadedDefinitionNodes {
		return budget.scan, invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition nodes exceed aggregate resource limit"))
	}
	if budget.byteOverflow || budget.bytes > maxLoadedDefinitionSetBytes {
		return budget.scan, invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition bytes exceed aggregate resource limit"))
	}
	if budget.best != nil {
		violation := budget.best
		return budget.scan, invalidLoadedState(
			violation.migration,
			violation.operation,
			violation.operationKind,
			fmt.Errorf("loaded definition resource limit exceeded at %s: %s", violation.path, violation.reason),
		)
	}
	return budget.scan, nil
}

func loadedScanOperationResource(budget *loadedResourceBudget, migration Migration, index int, operation Operation) {
	budget.scan.operations++
	if budget.nodeOverflow || isNilOperation(operation) {
		return
	}
	// Reject unknown dynamic operation types at the typed snapshot boundary
	// without invoking any methods on an embedding wrapper while scanning.
	operation = operationValue(operation)
	switch operation.(type) {
	case CreateModel, AddField, AlterField:
	default:
		return
	}
	kind := operation.Kind()
	wireKind := loadedOperationWireKind(operation)
	loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].kind", index), wireKind, false)
	loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].app_label", index), operation.App(), false)
	switch value := operationValue(operation).(type) {
	case CreateModel:
		loadedScanModelResource(budget, migration, index, kind, value.Model)
	case AddField:
		loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].model_name", index), value.ModelName, false)
		loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].before_field", index), value.BeforeField, false)
		loadedConsumeNodes(budget, 1)
		if budget.nodeOverflow {
			return
		}
		loadedScanFieldResource(budget, migration, index, kind, fmt.Sprintf("operations[%d].field", index), value.Field)
	case AlterField:
		loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].model_name", index), value.ModelName, false)
		loadedConsumeNodes(budget, 2)
		if budget.nodeOverflow {
			return
		}
		loadedScanFieldResource(budget, migration, index, kind, fmt.Sprintf("operations[%d].before", index), value.Before)
		loadedScanFieldResource(budget, migration, index, kind, fmt.Sprintf("operations[%d].after", index), value.After)
	}
}

func loadedOperationWireKind(operation Operation) string {
	switch operation.(type) {
	case CreateModel, *CreateModel:
		return "create_model"
	case AddField, *AddField:
		return "add_field"
	case AlterField, *AlterField:
		return "alter_field"
	default:
		return ""
	}
}

func loadedScanModelResource(budget *loadedResourceBudget, migration Migration, operationIndex int, kind string, model ir.Model) {
	if budget.nodeOverflow {
		return
	}
	prefix := fmt.Sprintf("operations[%d].model", operationIndex)
	loadedConsumeNodes(budget, 1)
	if budget.nodeOverflow {
		return
	}
	loadedConsumeString(budget, migration, operationIndex, kind, prefix+".name", model.Name, false)
	loadedConsumeString(budget, migration, operationIndex, kind, prefix+".go_name", model.GoName, false)
	loadedConsumeString(budget, migration, operationIndex, kind, prefix+".db_table", model.DBTable, false)
	if len(model.Fields) > maxLoadedFieldsPerCreateModel {
		loadedConsiderViolation(budget, loadedResourceViolation{migration: migration, operation: operationIndex, operationKind: kind, path: prefix + ".fields", reason: "field_count"})
		return
	}
	loadedConsumeNodes(budget, uint64(len(model.Fields)))
	if budget.nodeOverflow {
		return
	}
	for index := range model.Fields {
		if budget.nodeOverflow {
			return
		}
		loadedScanFieldResource(budget, migration, operationIndex, kind, fmt.Sprintf("%s.fields[%d]", prefix, index), model.Fields[index])
	}
}

func loadedScanFieldResource(budget *loadedResourceBudget, migration Migration, operationIndex int, kind, path string, field ir.Field) {
	budget.scan.fields++
	if budget.nodeOverflow {
		return
	}
	loadedConsumeString(budget, migration, operationIndex, kind, path+".name", field.Name, false)
	loadedConsumeString(budget, migration, operationIndex, kind, path+".go_name", field.GoName, false)
	loadedConsumeString(budget, migration, operationIndex, kind, path+".column", field.Column, false)
	loadedConsumeString(budget, migration, operationIndex, kind, path+".kind", string(field.Kind), false)
	if field.Default != nil {
		loadedConsumeNodes(budget, 1)
		if budget.nodeOverflow {
			return
		}
		loadedConsumeString(budget, migration, operationIndex, kind, path+".default.kind", string(field.Default.Kind), false)
		loadedConsumeString(budget, migration, operationIndex, kind, path+".default.string", field.Default.String, true)
		loadedConsumeString(budget, migration, operationIndex, kind, path+".default.datetime", field.Default.DateTime, true)
	}
	loadedConsumeNodes(budget, uint64(len(field.Choices))*2)
	if budget.nodeOverflow {
		return
	}
	for index, choice := range field.Choices {
		prefix := fmt.Sprintf("%s.choices[%d]", path, index)
		loadedConsumeString(budget, migration, operationIndex, kind, prefix+".label", choice.Label, true)
		loadedConsumeString(budget, migration, operationIndex, kind, prefix+".value.kind", string(choice.Value.Kind), false)
		loadedConsumeString(budget, migration, operationIndex, kind, prefix+".value.string", choice.Value.String, true)
		loadedConsumeString(budget, migration, operationIndex, kind, prefix+".value.datetime", choice.Value.DateTime, true)
	}
	if field.Relation != nil {
		loadedConsumeNodes(budget, 3)
		if budget.nodeOverflow {
			return
		}
		loadedConsumeString(budget, migration, operationIndex, kind, path+".relation.target.app_label", field.Relation.Target.AppLabel, false)
		loadedConsumeString(budget, migration, operationIndex, kind, path+".relation.target.model_name", field.Relation.Target.ModelName, false)
		loadedConsumeString(budget, migration, operationIndex, kind, path+".relation.cardinality", string(field.Relation.Cardinality), false)
		loadedConsumeString(budget, migration, operationIndex, kind, path+".relation.reverse.name", field.Relation.Reverse.Name, false)
		loadedConsumeString(budget, migration, operationIndex, kind, path+".relation.on_delete", string(field.Relation.OnDelete), false)
	}
}

func loadedConsumeNodes(budget *loadedResourceBudget, count uint64) {
	if budget.nodeOverflow || count > uint64(maxLoadedDefinitionNodes)-budget.nodes {
		budget.nodeOverflow = true
		return
	}
	budget.nodes += count
}

func loadedConsumeString(
	budget *loadedResourceBudget,
	migration Migration,
	operationIndex int,
	operationKind, path, value string,
	payload bool,
) {
	if payload {
		if len(value) > maxLoadedDefinitionBytes {
			loadedConsiderViolation(budget, loadedResourceViolation{migration: migration, operation: operationIndex, operationKind: operationKind, path: path, reason: "payload_bytes"})
		}
	}
	count := uint64(len(value))
	if budget.byteOverflow || count > uint64(maxLoadedDefinitionSetBytes)-budget.bytes {
		budget.byteOverflow = true
		return
	}
	budget.bytes += count
}

func loadedConsiderViolation(budget *loadedResourceBudget, candidate loadedResourceViolation) {
	if budget.best == nil || loadedResourceViolationLess(candidate, *budget.best) {
		copy := candidate
		budget.best = &copy
	}
}

func loadedResourceViolationLess(left, right loadedResourceViolation) bool {
	if left.migration.Key() != right.migration.Key() {
		return migrationKeyLess(left.migration.Key(), right.migration.Key())
	}
	if left.operation != right.operation {
		return left.operation < right.operation
	}
	if left.path != right.path {
		return left.path < right.path
	}
	return left.reason < right.reason
}
