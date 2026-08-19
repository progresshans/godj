package migrations

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
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
			if fieldName, incompatible := relationIncompatibleField(copy); incompatible {
				return nil, invalidReconstructorOperation(
					definition,
					index,
					kind,
					fmt.Errorf("Schema IR v2 migration state cannot represent relation-bearing field %q", fieldName),
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

func relationIncompatibleField(operation Operation) (string, bool) {
	switch operation := operation.(type) {
	case CreateModel:
		for _, field := range operation.Model.Fields {
			if field.Kind == ir.FieldForeignKey || field.Relation != nil {
				return field.Name, true
			}
		}
	case *CreateModel:
		for _, field := range operation.Model.Fields {
			if field.Kind == ir.FieldForeignKey || field.Relation != nil {
				return field.Name, true
			}
		}
	case AddField:
		if operation.Field.Kind == ir.FieldForeignKey || operation.Field.Relation != nil {
			return operation.Field.Name, true
		}
	case *AddField:
		if operation.Field.Kind == ir.FieldForeignKey || operation.Field.Relation != nil {
			return operation.Field.Name, true
		}
	}
	return "", false
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

// loadedDefinitionAuthority is minted only after the definition package's
// private carrier has been consumed and validated by Executor.Migrate. It is
// intentionally neither constructible nor observable outside this package.
type loadedDefinitionAuthority struct {
	marker   *loadedDefinitionAuthorityMarker
	planner  Planner
	profiles map[MigrationKey]loadedDefinitionProfile
}

type loadedDefinitionAuthorityMarker struct{}

type loadedDefinitionProfile struct {
	definitionFormat int64
	loaderABI        int64
	operationCodec   int64
	schemaIR         int64
}

func (a *loadedDefinitionAuthority) valid() bool {
	return a != nil && a.marker != nil
}

func (a *loadedDefinitionAuthority) relationProfile(key MigrationKey) bool {
	profile, exists := a.profiles[key]
	return exists && profile == (loadedDefinitionProfile{definitionFormat: 1, loaderABI: 2, operationCodec: 2, schemaIR: 3})
}

// loadedStateReconstructor is the relation-capable historical state engine.
// It has no public constructor: a fresh instance can be built only from a
// just-validated loader authority and the exact visible full DAG.
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

type loadedRelationTarget struct {
	SourceModel      ir.Model
	SourceField      ir.Field
	TargetModel      ir.Model
	TargetPrimaryKey ir.Field
}

// loadedStateBuilder is the private, loader-authorized historical state. It
// mutates owned clones in place so readiness and reconstruction are linear in
// the accepted definition payload instead of repeatedly cloning the whole
// growing ProjectState for every AddField operation.
type loadedStateBuilder struct {
	formatVersion int
	apps          map[string]*loadedStateApp
	relationCount uint64
	reverse       map[loadedModelIdentity]map[string]loadedReverseOwner
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

func newLoadedStateBuilder() *loadedStateBuilder {
	return &loadedStateBuilder{
		formatVersion: StateFormatVersion,
		apps:          make(map[string]*loadedStateApp),
		reverse:       make(map[loadedModelIdentity]map[string]loadedReverseOwner),
	}
}

func (builder *loadedStateBuilder) schemaIRVersion() int {
	if builder.formatVersion == RelationStateFormatVersion {
		return ir.RelationFormatVersion
	}
	return ir.FormatVersion
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
	state := ProjectState{formatVersion: builder.formatVersion, apps: make(map[string]ir.Schema, len(builder.apps))}
	for appLabel, app := range builder.apps {
		schema := ir.Schema{
			FormatVersion: builder.schemaIRVersion(),
			AppLabel:      appLabel,
			Models:        make([]ir.Model, 0, len(app.order)),
		}
		for _, modelName := range app.order {
			model, exists := app.models[modelName]
			if !exists {
				return ProjectState{}, fmt.Errorf("loaded model order contains missing model %s.%s", appLabel, modelName)
			}
			schema.Models = append(schema.Models, model.value.Clone())
		}
		normalized, err := ir.Normalize(schema)
		if err != nil {
			return ProjectState{}, fmt.Errorf("normalize loaded project app %s: %w", appLabel, err)
		}
		if !reflect.DeepEqual(normalized, schema) {
			return ProjectState{}, fmt.Errorf("loaded project app %s is not normalized", appLabel)
		}
		state.apps[appLabel] = normalized.Clone()
	}
	if err := state.validate(); err != nil {
		return ProjectState{}, err
	}
	return state.Clone(), nil
}

func (builder *loadedStateBuilder) empty() bool {
	return len(builder.apps) == 0 && builder.relationCount == 0 && len(builder.reverse) == 0
}

func newLoadedStateReconstructor(
	authority *loadedDefinitionAuthority,
	definitions []Migration,
) (loadedStateReconstructor, error) {
	if !authority.valid() {
		return loadedStateReconstructor{}, invalidLoadedState(Migration{}, NoOperation, "", errors.New("validated loader authority is missing"))
	}
	if err := validateLoadedDefinitionResources(definitions); err != nil {
		return loadedStateReconstructor{}, err
	}
	// Rebuild from the newest visible bytes even though the authority already
	// owns the planner produced at carrier validation. Equality of graph nodes
	// is checked without retaining either caller-owned graph representation.
	planner, err := NewPlanner(definitions...)
	if err != nil {
		return loadedStateReconstructor{}, err
	}
	if !plannerGraphsEqual(planner.graph, authority.planner.graph) {
		return loadedStateReconstructor{}, invalidLoadedState(Migration{}, NoOperation, "", errors.New("validated loader graph changed before state reconstruction"))
	}

	cloned, err := cloneLoadedReconstructorDefinitions(planner.graph, definitions)
	if err != nil {
		return loadedStateReconstructor{}, err
	}
	if len(authority.profiles) != len(cloned) {
		return loadedStateReconstructor{}, invalidLoadedState(Migration{}, NoOperation, "", errors.New("validated profile graph is incomplete"))
	}
	for key := range cloned {
		if _, exists := authority.profiles[key]; !exists {
			return loadedStateReconstructor{}, invalidLoadedState(cloned[key], NoOperation, "", errors.New("validated migration profile is missing"))
		}
		if migrationContainsRelation(cloned[key]) && !authority.relationProfile(key) {
			return loadedStateReconstructor{}, invalidLoadedState(cloned[key], firstRelationOperation(cloned[key]), operationKindAt(cloned[key], firstRelationOperation(cloned[key])), errors.New("relation operation requires the relation definition profile"))
		}
	}

	reconstructor := loadedStateReconstructor{planner: planner, definitions: cloned}
	reconstructor.ancestors = newLoadedAncestorIndex(planner.graph)
	reconstructor.creators, reconstructor.declarations = collectLoadedStateGraph(planner.graph, cloned)
	if err := reconstructor.validateChronology(); err != nil {
		return loadedStateReconstructor{}, err
	}
	if err := reconstructor.validateReadiness(); err != nil {
		return loadedStateReconstructor{}, err
	}
	return reconstructor, nil
}

func plannerGraphsEqual(left, right *plannerGraph) bool {
	if left == nil {
		left = emptyPlannerGraph()
	}
	if right == nil {
		right = emptyPlannerGraph()
	}
	return reflect.DeepEqual(left.nodes, right.nodes) && reflect.DeepEqual(left.parents, right.parents)
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
				return nil, invalidReconstructorOperation(definition, index, "", fmt.Errorf("operation type %T is not supported by loaded state reconstruction", operation))
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
			case *CreateModel:
				if value == nil {
					continue
				}
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
			case *AddField:
				if value != nil && fieldContainsRelation(value.Field) {
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
	for _, declaration := range r.declarations {
		if declaration.field.Relation == nil {
			continue
		}
		target := loadedModelIdentity{app: declaration.field.Relation.Target.AppLabel, model: declaration.field.Relation.Target.ModelName}
		if target == declaration.source {
			return r.declarationError(declaration, errors.New("self-referential ForeignKey is outside the bounded relation lifecycle"))
		}
	}
	if cycle := firstLoadedRelationCycle(r.declarations); len(cycle) != 0 {
		cycleSet := make(map[loadedModelIdentity]struct{}, len(cycle))
		for _, identity := range cycle {
			cycleSet[identity] = struct{}{}
		}
		for _, declaration := range r.declarations {
			if declaration.field.Relation == nil {
				continue
			}
			target := loadedModelIdentity{app: declaration.field.Relation.Target.AppLabel, model: declaration.field.Relation.Target.ModelName}
			_, sourceInCycle := cycleSet[declaration.source]
			_, targetInCycle := cycleSet[target]
			if sourceInCycle && targetInCycle {
				return r.declarationError(declaration, errors.New("ForeignKey relation cycle is outside the bounded relation lifecycle"))
			}
		}
	}
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
	indegree := make(map[MigrationKey]int, len(graph.nodes))
	for _, key := range graph.nodes {
		indegree[key] = len(graph.parents[key])
	}
	ordered := make([]MigrationKey, 0, len(graph.nodes))
	processed := make(map[MigrationKey]struct{}, len(graph.nodes))
	for len(ordered) != len(graph.nodes) {
		var next MigrationKey
		found := false
		for _, key := range graph.nodes {
			if _, exists := processed[key]; exists || indegree[key] != 0 {
				continue
			}
			next = key
			found = true
			break
		}
		if !found {
			// NewPlanner already rejected cycles. A zero index fails closed in
			// chronology if that invariant ever changes.
			return loadedAncestorIndex{positions: positions, sets: make([][]uint64, len(graph.nodes))}
		}
		processed[next] = struct{}{}
		ordered = append(ordered, next)
		for _, child := range graph.children[next] {
			indegree[child]--
		}
	}
	words := (len(graph.nodes) + 63) / 64
	sets := make([][]uint64, len(graph.nodes))
	for _, key := range ordered {
		position := positions[key]
		ancestors := make([]uint64, words)
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

func firstLoadedRelationCycle(declarations []loadedRelationDeclaration) []loadedModelIdentity {
	edges := make(map[loadedModelIdentity][]loadedModelIdentity)
	nodes := make(map[loadedModelIdentity]struct{})
	for _, declaration := range declarations {
		if declaration.field.Relation == nil {
			continue
		}
		target := loadedModelIdentity{app: declaration.field.Relation.Target.AppLabel, model: declaration.field.Relation.Target.ModelName}
		edges[declaration.source] = append(edges[declaration.source], target)
		nodes[declaration.source] = struct{}{}
		nodes[target] = struct{}{}
	}
	ordered := make([]loadedModelIdentity, 0, len(nodes))
	for node := range nodes {
		ordered = append(ordered, node)
	}
	sort.Slice(ordered, func(left, right int) bool { return loadedIdentityLess(ordered[left], ordered[right]) })
	for node := range edges {
		sort.Slice(edges[node], func(left, right int) bool { return loadedIdentityLess(edges[node][left], edges[node][right]) })
	}
	type relationFrame struct {
		node loadedModelIdentity
		next int
	}
	state := make(map[loadedModelIdentity]uint8, len(nodes))
	positions := make(map[loadedModelIdentity]int, len(nodes))
	path := make([]loadedModelIdentity, 0, len(nodes))
	frames := make([]relationFrame, 0, len(nodes))
	for _, node := range ordered {
		if state[node] != 0 {
			continue
		}
		state[node] = 1
		positions[node] = len(path)
		path = append(path, node)
		frames = append(frames, relationFrame{node: node})
		for len(frames) != 0 {
			frame := &frames[len(frames)-1]
			neighbors := edges[frame.node]
			if frame.next == len(neighbors) {
				state[frame.node] = 2
				delete(positions, frame.node)
				path = path[:len(path)-1]
				frames = frames[:len(frames)-1]
				continue
			}
			target := neighbors[frame.next]
			frame.next++
			switch state[target] {
			case 0:
				state[target] = 1
				positions[target] = len(path)
				path = append(path, target)
				frames = append(frames, relationFrame{node: target})
			case 1:
				cycle := append([]loadedModelIdentity(nil), path[positions[target]:]...)
				sort.Slice(cycle, func(left, right int) bool { return loadedIdentityLess(cycle[left], cycle[right]) })
				return cycle
			}
		}
	}
	return nil
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
	if migration.App == "" || migration.Name == "" {
		return migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", errors.New("migration identity is empty"))
	}
	relationBearing := migrationContainsRelation(migration)
	if relationBearing && builder.formatVersion == StateFormatVersion {
		builder.formatVersion = RelationStateFormatVersion
	}
	for _, index := range operationIndices(len(migration.Operations), direction) {
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
		case *CreateModel:
			if value == nil {
				err = errors.New("operation is nil")
			} else if direction == DirectionForward {
				err = builder.createModel(*value)
			} else {
				err = builder.deleteModel(*value)
			}
		case AddField:
			if direction == DirectionForward {
				err = builder.addField(value)
			} else {
				err = builder.removeField(value)
			}
		case *AddField:
			if value == nil {
				err = errors.New("operation is nil")
			} else if direction == DirectionForward {
				err = builder.addField(*value)
			} else {
				err = builder.removeField(*value)
			}
		default:
			err = fmt.Errorf("operation type %T is not supported by loaded state reconstruction", operation)
		}
		if err != nil {
			return migrationError(CategoryState, CodeInvalidState, direction, migration, index, operation.Kind(), err)
		}
	}
	if relationBearing && builder.formatVersion == RelationStateFormatVersion && builder.relationCount == 0 {
		builder.formatVersion = StateFormatVersion
	}
	return nil
}

func (builder *loadedStateBuilder) createModel(operation CreateModel) error {
	normalized, err := normalizedSingleModelVersion(operation.AppLabel, operation.Model, builder.schemaIRVersion())
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
	want, err := normalizedSingleModelVersion(operation.AppLabel, operation.Model, builder.schemaIRVersion())
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
	if reverse := builder.reverse[identity]; len(reverse) != 0 {
		return fmt.Errorf("model %s.%s is still targeted by relation reverse owners", identity.app, identity.model)
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
	field, err := normalizeLoadedAddedField(operation.AppLabel, operation.Field, builder.schemaIRVersion())
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
	model.fieldNames[field.Name] = len(model.value.Fields)
	model.goNames[field.GoName] = field.Name
	model.columns[field.Column] = field.Name
	model.value.Fields = append(model.value.Fields, field.Clone())
	return builder.addRelation(identity, field)
}

func (builder *loadedStateBuilder) removeField(operation AddField) error {
	identity := loadedModelIdentity{app: operation.AppLabel, model: operation.ModelName}
	model, exists := builder.model(identity)
	if !exists {
		return fmt.Errorf("model %s.%s does not exist", identity.app, identity.model)
	}
	want, err := normalizeLoadedAddedField(operation.AppLabel, operation.Field, builder.schemaIRVersion())
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
	if index != len(model.value.Fields)-1 {
		return fmt.Errorf("field %s.%s.%s is not the latest field on its model", identity.app, identity.model, want.Name)
	}
	if err := builder.removeRelation(identity, actual); err != nil {
		return err
	}
	model.value.Fields = model.value.Fields[:index]
	delete(model.fieldNames, actual.Name)
	delete(model.goNames, actual.GoName)
	delete(model.columns, actual.Column)
	return nil
}

func normalizeLoadedAddedField(appLabel string, value ir.Field, formatVersion int) (ir.Field, error) {
	syntheticName := "_godj_loaded_pk"
	syntheticGoName := "GodjLoadedPK"
	syntheticColumn := "_godj_loaded_pk"
	for value.Name == syntheticName || value.GoName == syntheticGoName || value.Column == syntheticColumn {
		syntheticName += "_"
		syntheticGoName += "X"
		syntheticColumn += "_"
	}
	schema, err := ir.Normalize(ir.Schema{
		FormatVersion: formatVersion,
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
	builder.relationCount--
	return nil
}

func (r loadedStateReconstructor) validateReadiness() error {
	steps, err := r.fullForwardProjection()
	if err != nil {
		return err
	}
	builder := newLoadedStateBuilder()
	for _, step := range steps {
		if err := r.applyLoadedMigration(builder, r.definitions[step.Key], DirectionForward); err != nil {
			return err
		}
	}
	if _, err := builder.projectState(); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	for index := len(steps) - 1; index >= 0; index-- {
		if err := r.applyLoadedMigration(builder, r.definitions[steps[index].Key], DirectionBackward); err != nil {
			return err
		}
	}
	if !builder.empty() || builder.formatVersion != StateFormatVersion {
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
	leaves := r.planner.graph.appLeaves()
	targets := make([]Target, len(leaves))
	for index := range leaves {
		targets[index] = NamedTarget(leaves[index])
	}
	steps, err := r.planner.Plan(AppliedState{}, targets...)
	if err != nil {
		return nil, err
	}
	if len(steps) != len(r.planner.graph.nodes) {
		return nil, invalidLoadedState(Migration{}, NoOperation, "", fmt.Errorf(
			"full historical projection covers %d of %d graph nodes",
			len(steps), len(r.planner.graph.nodes),
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
	for _, key := range r.planner.graph.nodes {
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
	projection := StateReconstructor{planner: r.planner, definitions: r.definitions}
	return projection.targetProjection(targets)
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

func (r loadedStateReconstructor) preflight(
	before ProjectState,
	migration Migration,
	direction Direction,
) ([]preparedOperation, ProjectState, error) {
	if err := before.validate(); err != nil {
		return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", err)
	}
	if migration.App == "" || migration.Name == "" {
		return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", errors.New("migration identity is empty"))
	}
	relationBearing := migrationContainsRelation(migration)
	state := before.Clone()
	if relationBearing && state.FormatVersion() == StateFormatVersion {
		promoted, err := promoteProjectState(state)
		if err != nil {
			index := firstRelationOperation(migration)
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, operationKindAt(migration, index), err)
		}
		state = promoted
	}
	indices := operationIndices(len(migration.Operations), direction)
	prepared := make([]preparedOperation, 0, len(indices))
	for _, index := range indices {
		operation := migration.Operations[index]
		if isNilOperation(operation) {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, "", errors.New("operation is nil"))
		}
		if operation.App() != migration.App {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, operation.Kind(), fmt.Errorf("operation app %q does not match migration app %q", operation.App(), migration.App))
		}
		from := state.Clone()
		var next ProjectState
		var err error
		if direction == DirectionForward {
			next, err = operation.stateForward(from)
		} else {
			next, err = operation.stateBackward(from)
		}
		if err != nil {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, operation.Kind(), err)
		}
		targets, err := r.bindOperationRelations(from, next, migration, index, direction)
		if err != nil {
			return nil, before.Clone(), err
		}
		if err := validateLoadedRelationState(next); err != nil {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, operation.Kind(), err)
		}
		prepared = append(prepared, preparedOperation{index: index, op: operation, from: from, to: next.Clone(), relationTargets: targets})
		state = next
	}
	if relationBearing && state.FormatVersion() == RelationStateFormatVersion {
		if _, _, _, exists := firstProjectStateRelation(state); !exists {
			demoted, err := demoteProjectState(state)
			if err != nil {
				return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", err)
			}
			state = demoted
		}
	}
	return prepared, state.Clone(), nil
}

func (r loadedStateReconstructor) bindOperationRelations(
	from, to ProjectState,
	migration Migration,
	operationIndex int,
	direction Direction,
) ([]loadedRelationTarget, error) {
	operation := migration.Operations[operationIndex]
	fields := operationRelationFields(operation)
	if len(fields) == 0 {
		return nil, nil
	}
	sourceApp, sourceModelName := operationSourceModel(operation)
	visible := from
	if direction == DirectionBackward {
		visible = from
	}
	sourceModel, sourceExists := relationSourceModelForBoundary(from, to, sourceApp, sourceModelName, direction)
	if !sourceExists {
		return nil, migrationError(CategoryState, CodeInvalidState, direction, migration, operationIndex, operation.Kind(), fmt.Errorf("relation source model %s.%s is missing", sourceApp, sourceModelName))
	}
	targets := make([]loadedRelationTarget, 0, len(fields))
	for _, field := range fields {
		if field.Relation == nil {
			continue
		}
		targetIdentity := field.Relation.Target
		targetModel, exists := visible.Model(targetIdentity.AppLabel, targetIdentity.ModelName)
		if !exists {
			return nil, migrationError(CategoryState, CodeInvalidState, direction, migration, operationIndex, operation.Kind(), fmt.Errorf("historical target model %s.%s is not visible", targetIdentity.AppLabel, targetIdentity.ModelName))
		}
		primaryKey, err := exactAutoPrimaryKey(targetModel)
		if err != nil {
			return nil, migrationError(CategoryState, CodeInvalidState, direction, migration, operationIndex, operation.Kind(), err)
		}
		targets = append(targets, loadedRelationTarget{
			SourceModel: sourceModel.Clone(), SourceField: field.Clone(),
			TargetModel: targetModel.Clone(), TargetPrimaryKey: primaryKey.Clone(),
		})
	}
	return targets, nil
}

func relationSourceModelForBoundary(from, to ProjectState, app, model string, direction Direction) (ir.Model, bool) {
	if direction == DirectionBackward {
		return from.Model(app, model)
	}
	if value, exists := to.Model(app, model); exists {
		return value, true
	}
	return from.Model(app, model)
}

func operationSourceModel(operation Operation) (string, string) {
	switch value := operation.(type) {
	case CreateModel:
		return value.AppLabel, value.Model.Name
	case *CreateModel:
		return value.AppLabel, value.Model.Name
	case AddField:
		return value.AppLabel, value.ModelName
	case *AddField:
		return value.AppLabel, value.ModelName
	default:
		return operation.App(), ""
	}
}

func operationRelationFields(operation Operation) []ir.Field {
	fields := make([]ir.Field, 0)
	switch value := operation.(type) {
	case CreateModel:
		for _, field := range value.Model.Fields {
			if fieldContainsRelation(field) {
				fields = append(fields, field.Clone())
			}
		}
	case *CreateModel:
		if value != nil {
			for _, field := range value.Model.Fields {
				if fieldContainsRelation(field) {
					fields = append(fields, field.Clone())
				}
			}
		}
	case AddField:
		if fieldContainsRelation(value.Field) {
			fields = append(fields, value.Field.Clone())
		}
	case *AddField:
		if value != nil && fieldContainsRelation(value.Field) {
			fields = append(fields, value.Field.Clone())
		}
	}
	return fields
}

func exactAutoPrimaryKey(model ir.Model) (ir.Field, error) {
	keys := make([]ir.Field, 0, 1)
	for _, field := range model.Fields {
		if field.PrimaryKey {
			keys = append(keys, field.Clone())
		}
	}
	if len(keys) != 1 {
		return ir.Field{}, fmt.Errorf("historical target model %s requires exactly one primary key", model.Name)
	}
	if keys[0].Kind != ir.FieldAuto || keys[0].Nullable {
		return ir.Field{}, fmt.Errorf("historical target model %s primary key must be a non-nullable AutoField", model.Name)
	}
	return keys[0].Clone(), nil
}

func validateLoadedRelationState(state ProjectState) error {
	type relationValue struct {
		source loadedModelIdentity
		field  ir.Field
	}
	relations := make([]relationValue, 0)
	for _, app := range state.Apps() {
		schema := state.apps[app]
		for _, model := range schema.Models {
			for _, field := range model.Fields {
				if fieldContainsRelation(field) {
					relations = append(relations, relationValue{source: loadedModelIdentity{app: app, model: model.Name}, field: field.Clone()})
				}
			}
		}
	}
	sort.Slice(relations, func(left, right int) bool {
		if relations[left].source != relations[right].source {
			return loadedIdentityLess(relations[left].source, relations[right].source)
		}
		return relations[left].field.Name < relations[right].field.Name
	})
	reverseOwners := make(map[string]relationValue)
	declarations := make([]loadedRelationDeclaration, 0, len(relations))
	for _, relation := range relations {
		if relation.field.Relation == nil {
			return fmt.Errorf("relation field %s.%s.%s has no relation arm", relation.source.app, relation.source.model, relation.field.Name)
		}
		targetIdentity := relation.field.Relation.Target
		target, exists := state.Model(targetIdentity.AppLabel, targetIdentity.ModelName)
		if !exists {
			return fmt.Errorf("relation target %s.%s is missing", targetIdentity.AppLabel, targetIdentity.ModelName)
		}
		if _, err := exactAutoPrimaryKey(target); err != nil {
			return err
		}
		if relation.source == (loadedModelIdentity{app: targetIdentity.AppLabel, model: targetIdentity.ModelName}) {
			return fmt.Errorf("self-referential relation %s.%s.%s is unsupported", relation.source.app, relation.source.model, relation.field.Name)
		}
		reverse := relation.field.Relation.Reverse
		if reverse.Name != "" {
			for _, targetField := range target.Fields {
				if targetField.Name == reverse.Name {
					return fmt.Errorf("reverse name %q collides with target field %s.%s.%s", reverse.Name, targetIdentity.AppLabel, targetIdentity.ModelName, targetField.Name)
				}
			}
			namespace := targetIdentity.AppLabel + "\x00" + targetIdentity.ModelName + "\x00" + reverse.Name
			if previous, exists := reverseOwners[namespace]; exists {
				return fmt.Errorf("reverse name %q collides between %s.%s.%s and %s.%s.%s", reverse.Name, previous.source.app, previous.source.model, previous.field.Name, relation.source.app, relation.source.model, relation.field.Name)
			}
			reverseOwners[namespace] = relation
		}
		declarations = append(declarations, loadedRelationDeclaration{source: relation.source, field: relation.field.Clone()})
	}
	if cycle := firstLoadedRelationCycle(declarations); len(cycle) != 0 {
		return fmt.Errorf("relation graph contains a cycle at %s.%s", cycle[0].app, cycle[0].model)
	}
	return nil
}

func firstRelationOperation(migration Migration) int {
	for index, operation := range migration.Operations {
		if len(operationRelationFields(operation)) != 0 {
			return index
		}
	}
	return NoOperation
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

type loadedResourceBudget struct {
	nodes        uint64
	bytes        uint64
	nodeOverflow bool
	byteOverflow bool
	best         *loadedResourceViolation
}

// validateLoadedDefinitionResources is deliberately the first operation over
// caller-owned definition contents at the loader-authorized boundary. It does
// not sort, clone, normalize, or canonicalize any slice or nested IR value.
func validateLoadedDefinitionResources(definitions []Migration) error {
	if len(definitions) > definitionhandoff.MaxDefinitions {
		return invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition count exceeds resource limit"))
	}
	budget := loadedResourceBudget{}
	loadedConsumeNodes(&budget, uint64(len(definitions)))
	for definitionIndex := range definitions {
		if budget.nodeOverflow {
			break
		}
		definition := definitions[definitionIndex]
		definitionStart := budget.bytes
		loadedConsumeString(&budget, definition, NoOperation, "", "app", definition.App, false)
		loadedConsumeString(&budget, definition, NoOperation, "", "name", definition.Name, false)
		if len(definition.Dependencies) > definitionhandoff.MaxDependencies {
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
		if len(definition.Operations) > definitionhandoff.MaxOperations {
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
		if !budget.byteOverflow && budget.bytes >= definitionStart && budget.bytes-definitionStart > definitionhandoff.MaxDefinitionBytes {
			loadedConsiderViolation(&budget, loadedResourceViolation{migration: definition, operation: NoOperation, path: "definition", reason: "definition_bytes"})
		}
	}
	if budget.nodeOverflow || budget.nodes > definitionhandoff.MaxDefinitionNodes {
		return invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition nodes exceed aggregate resource limit"))
	}
	if budget.byteOverflow || budget.bytes > definitionhandoff.MaxDefinitionSetBytes {
		return invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition bytes exceed aggregate resource limit"))
	}
	if budget.best != nil {
		violation := budget.best
		return invalidLoadedState(
			violation.migration,
			violation.operation,
			violation.operationKind,
			fmt.Errorf("loaded definition resource limit exceeded at %s: %s", violation.path, violation.reason),
		)
	}
	return nil
}

func loadedScanOperationResource(budget *loadedResourceBudget, migration Migration, index int, operation Operation) {
	if budget.nodeOverflow || isNilOperation(operation) {
		return
	}
	kind := operation.Kind()
	wireKind := loadedOperationWireKind(operation)
	loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].kind", index), wireKind, false)
	loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].app_label", index), operation.App(), false)
	switch value := operation.(type) {
	case CreateModel:
		loadedScanModelResource(budget, migration, index, kind, value.Model)
	case *CreateModel:
		if value != nil {
			loadedScanModelResource(budget, migration, index, kind, value.Model)
		}
	case AddField:
		loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].model_name", index), value.ModelName, false)
		loadedConsumeNodes(budget, 1)
		if budget.nodeOverflow {
			return
		}
		loadedScanFieldResource(budget, migration, index, kind, fmt.Sprintf("operations[%d].field", index), value.Field)
	case *AddField:
		if value != nil {
			loadedConsumeString(budget, migration, index, kind, fmt.Sprintf("operations[%d].model_name", index), value.ModelName, false)
			loadedConsumeNodes(budget, 1)
			if budget.nodeOverflow {
				return
			}
			loadedScanFieldResource(budget, migration, index, kind, fmt.Sprintf("operations[%d].field", index), value.Field)
		}
	}
}

func loadedOperationWireKind(operation Operation) string {
	switch operation.(type) {
	case CreateModel, *CreateModel:
		return "create_model"
	case AddField, *AddField:
		return "add_field"
	default:
		return operation.Kind()
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
	if len(model.Fields) > definitionhandoff.MaxFieldsPerCreateModel {
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
	if budget.nodeOverflow || count > uint64(definitionhandoff.MaxDefinitionNodes)-budget.nodes {
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
		if len(value) > definitionhandoff.MaxDefinitionBytes {
			loadedConsiderViolation(budget, loadedResourceViolation{migration: migration, operation: operationIndex, operationKind: operationKind, path: path, reason: "payload_bytes"})
		}
	}
	count := uint64(len(value))
	if budget.byteOverflow || count > uint64(definitionhandoff.MaxDefinitionSetBytes)-budget.bytes {
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
