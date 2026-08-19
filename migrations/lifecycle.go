package migrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
	"github.com/progresshans/godj/schema/ir"
)

type lifecycleRequestKind uint8

const (
	lifecycleRequestLatest lifecycleRequestKind = iota + 1
	lifecycleRequestTargeted
)

// LifecycleRequest is an immutable tagged request for a complete migration
// lifecycle. Construct one with LatestLifecycleRequest or
// TargetedLifecycleRequest; its zero value is deliberately invalid.
type LifecycleRequest struct {
	kind    lifecycleRequestKind
	targets []Target
}

// LatestLifecycleRequest requests every same-app leaf of the loaded migration
// graph and therefore converges all known applications to their latest state.
func LatestLifecycleRequest() LifecycleRequest {
	return LifecycleRequest{kind: lifecycleRequestLatest}
}

// TargetedLifecycleRequest preserves the caller's target order while copying
// the complete request representation before it can be retained.
func TargetedLifecycleRequest(first Target, rest ...Target) LifecycleRequest {
	targets := make([]Target, 1, len(rest)+1)
	targets[0] = first
	targets = append(targets, rest...)
	return LifecycleRequest{kind: lifecycleRequestTargeted, targets: targets}
}

const lifecycleCleanupTimeout = 5 * time.Second

// Migrate reads one revision-bound history snapshot, validates and plans the
// complete lifecycle, then executes each migration in its own fenced
// transaction. Backends without the optional revision-fence capability fail
// closed; this method never falls back to the legacy transaction path.
func (e Executor) Migrate(
	ctx context.Context,
	definitions []Migration,
	request LifecycleRequest,
) (resultState ProjectState, resultErr error) {
	resultState = EmptyProjectState()
	if ctx == nil {
		return resultState, executionContextError(PlanStep{}, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}

	requestKind, requestTargetView, err := inspectLifecycleRequest(request)
	if err != nil {
		return resultState, err
	}

	// A relation carrier is the only path that may mint private loaded-state
	// authority. Scan caller-owned values before either the carrier or the
	// definitions can be cloned.
	hasCarrier := definitionhandoff.Has(ctx)
	if hasCarrier {
		if err := validateLoadedDefinitionResources(definitions); err != nil {
			return resultState, err
		}
		if err := ctx.Err(); err != nil {
			return resultState, executionContextError(PlanStep{}, err)
		}
	}
	hasRelation := definitionsContainRelation(definitions)
	if !hasCarrier && hasRelation {
		unsupported := relationMigrationUnsupported(definitions, "", errors.New("loader definition handoff is missing"))
		if err := ctx.Err(); err != nil {
			return resultState, executionContextError(PlanStep{}, err)
		}
		return resultState, unsupported
	}
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}
	if hasCarrier {
		if err := validateLoadedLifecycleTargets(definitions, requestTargetView); err != nil {
			return resultState, err
		}
	}
	requestTargets := append([]Target(nil), requestTargetView...)

	// Snapshot caller-owned definitions before any backend I/O. The
	// reconstructor then validates the graph and deep-copies every supported
	// operation so neither planning nor execution retains caller aliases.
	definitionSnapshot := cloneMigrationDefinitions(definitions)
	var authority *loadedDefinitionAuthority
	ctx, authority, err = consumeDefinitionHandoff(ctx, definitionSnapshot, "")
	if err != nil {
		return resultState, err
	}
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}
	var reconstructor StateReconstructor
	var loadedReconstructor *loadedStateReconstructor
	if authority != nil && hasRelation {
		loaded, loadedErr := newLoadedStateReconstructor(authority, definitionSnapshot)
		if loadedErr != nil {
			return resultState, loadedErr
		}
		if err := ctx.Err(); err != nil {
			return resultState, executionContextError(PlanStep{}, err)
		}
		loadedReconstructor = &loaded
	} else {
		reconstructor, err = NewStateReconstructor(definitionSnapshot...)
		if err != nil {
			return resultState, err
		}
	}
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}

	if isNilInterface(e.Backend) {
		return resultState, revisionFenceUnsupportedError(errors.New("backend is nil"))
	}
	fencedBackend, ok := e.Backend.(backend.RevisionFencedBackend)
	if !ok || isNilInterface(fencedBackend) {
		return resultState, revisionFenceUnsupportedError(errors.New("backend does not implement revision-fenced migrations"))
	}

	cleanupBase := context.WithoutCancel(ctx)
	session, openErr := fencedBackend.OpenRevisionFencedSession(ctx)
	if !isNilInterface(session) {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(cleanupBase, lifecycleCleanupTimeout)
			defer cancel()
			if closeErr := session.Close(cleanupCtx); closeErr != nil {
				secondary := migrationError(
					CategoryTransaction,
					CodeSessionCloseFailed,
					"",
					Migration{},
					NoOperation,
					"",
					closeErr,
				)
				if resultErr == nil {
					resultErr = secondary
				} else {
					// Keep the lifecycle failure first. errors.Join preserves both
					// causes without mislabeling terminal session cleanup as a
					// transaction rollback failure.
					resultErr = errors.Join(resultErr, secondary)
				}
			}
		}()
	}
	if openErr != nil {
		return resultState, classifyLifecycleError(openErr, CategoryTransaction, CodeBeginFailed, PlanStep{})
	}
	if authority != nil {
		if err := ctx.Err(); err != nil {
			return resultState, executionContextError(PlanStep{}, err)
		}
	}
	if isNilInterface(session) {
		return resultState, migrationError(
			CategoryTransaction,
			CodeBeginFailed,
			"",
			Migration{},
			NoOperation,
			"",
			errors.New("backend returned a nil revision-fenced session"),
		)
	}

	records, err := session.ReadAppliedMigrations(ctx)
	if err != nil {
		if category, code, ok := backendErrorClass(err); ok {
			return resultState, migrationError(category, code, "", Migration{}, NoOperation, "", err)
		}
		return resultState, newRecorderReadError(err)
	}
	if authority != nil {
		if err := ctx.Err(); err != nil {
			return resultState, executionContextError(PlanStep{}, err)
		}
		historyTargetCount := len(requestTargets)
		if requestKind == lifecycleRequestLatest {
			historyTargetCount = len(authority.planner.graph.appLeaves())
		}
		if err := validateLoadedHistoryPlanResources(definitionSnapshot, historyTargetCount, records); err != nil {
			return resultState, err
		}
		if err := ctx.Err(); err != nil {
			return resultState, executionContextError(PlanStep{}, err)
		}
	}
	keys := make([]MigrationKey, len(records))
	for index, record := range records {
		keys[index] = MigrationKey{App: record.App, Name: record.Name}
	}
	applied, err := NewAppliedState(keys...)
	if err != nil {
		return resultState, err
	}
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}
	if loadedReconstructor != nil {
		return e.migrateLoadedPlan(
			ctx,
			session,
			*loadedReconstructor,
			definitionSnapshot,
			applied,
			requestKind,
			requestTargets,
		)
	}

	// Check history before interpreting contained targets. This preserves the
	// command-level safety precedence: known inconsistent history prevents plan
	// evaluation and every migration transaction.
	if err := reconstructor.planner.CheckHistory(applied); err != nil {
		return resultState, err
	}
	before, err := reconstructor.Reconstruct(AppliedStateRequest(applied))
	if err != nil {
		return resultState, err
	}
	resultState = before.Clone()
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}

	var targets []Target
	switch requestKind {
	case lifecycleRequestLatest:
		leaves := reconstructor.graph().appLeaves()
		targets = make([]Target, len(leaves))
		for index, leaf := range leaves {
			targets[index] = NamedTarget(leaf)
		}
	case lifecycleRequestTargeted:
		targets = requestTargets
	}
	plan, err := reconstructor.planner.Plan(applied, targets...)
	if err != nil {
		return resultState, err
	}
	prepared, err := preflightPlan(ctx, before, definitionSnapshot, plan)
	if err != nil {
		return resultState, err
	}

	working := before.Clone()
	for _, preparedStep := range prepared {
		if err := ctx.Err(); err != nil {
			return working.Clone(), executionContextError(preparedStep.step, err)
		}
		working, err = executeFencedMigration(ctx, session, working, preparedStep)
		resultState = working.Clone()
		if err != nil {
			return resultState, err
		}
	}

	// Cancellation observed only after the final durable commit cannot undo the
	// successful lifecycle. Mandatory Close uses its own bounded context.
	return working.Clone(), nil
}

func (e Executor) migrateLoadedPlan(
	ctx context.Context,
	session backend.RevisionFencedSession,
	reconstructor loadedStateReconstructor,
	definitions []Migration,
	applied AppliedState,
	requestKind lifecycleRequestKind,
	requestTargets []Target,
) (ProjectState, error) {
	// The planner used for the actual request is deliberately rebuilt only
	// after the exact fenced history snapshot exists. Static readiness proves
	// that this immutable graph is safe to rebuild; it does not guess which
	// migrations the snapshot will make current.
	planner, err := NewPlanner(definitions...)
	if err != nil {
		return EmptyProjectState(), err
	}
	if err := planner.CheckHistory(applied); err != nil {
		return EmptyProjectState(), err
	}
	if err := ctx.Err(); err != nil {
		return EmptyProjectState(), executionContextError(PlanStep{}, err)
	}

	var targets []Target
	switch requestKind {
	case lifecycleRequestLatest:
		leaves := planner.graph.appLeaves()
		targets = make([]Target, len(leaves))
		for index, leaf := range leaves {
			targets[index] = NamedTarget(leaf)
		}
	case lifecycleRequestTargeted:
		targets = requestTargets
	}
	plan, err := planner.Plan(applied, targets...)
	if err != nil {
		return EmptyProjectState(), err
	}
	if err := ctx.Err(); err != nil {
		return EmptyProjectState(), executionContextError(PlanStep{}, err)
	}

	initialBuilder, err := reconstructor.builderForApplied(ctx, planner, applied)
	if err != nil {
		return EmptyProjectState(), err
	}
	before, err := initialBuilder.projectState()
	if err != nil {
		return EmptyProjectState(), invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), executionContextError(PlanStep{}, err)
	}

	// Validate every actual step against one mutable historical builder before
	// selecting any optional relation port or opening any migration transaction.
	// Retain only capability requirements and a semantic seal for each step;
	// operation snapshots are regenerated one step at a time after selection.
	dryBuilder := initialBuilder.clone()
	prepared, err := reconstructor.dryLoadedPlan(ctx, dryBuilder, plan)
	if err != nil {
		return before.Clone(), err
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), executionContextError(PlanStep{}, err)
	}
	relationSession, err := selectLoadedRelationLifecycle(ctx, e.Backend, session, reconstructor, prepared)
	if err != nil {
		return before.Clone(), err
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), executionContextError(PlanStep{}, err)
	}

	working := before.Clone()
	executionBuilder := initialBuilder
	for _, expected := range prepared {
		if err := ctx.Err(); err != nil {
			return working.Clone(), executionContextError(expected.step, err)
		}
		materialized, materializeErr := reconstructor.materializeLoadedStep(ctx, executionBuilder, expected.step, true)
		if materializeErr != nil {
			return working.Clone(), materializeErr
		}
		if materialized.requirements != expected.requirements ||
			materialized.relation != expected.relation ||
			materialized.seal != expected.seal {
			migration := reconstructor.definitions[expected.step.Key]
			return working.Clone(), migrationError(
				CategoryState,
				CodeInvalidState,
				expected.step.Direction,
				migration,
				NoOperation,
				"",
				errors.New("loaded migration step changed after whole-plan validation"),
			)
		}
		working, err = executeLoadedFencedMigration(
			ctx,
			session,
			relationSession,
			working,
			materialized,
		)
		if err != nil {
			return working.Clone(), err
		}
	}
	return working.Clone(), nil
}

func selectLoadedRelationLifecycle(
	ctx context.Context,
	value backend.AtomicBackend,
	session backend.RevisionFencedSession,
	reconstructor loadedStateReconstructor,
	steps []loadedPlanStep,
) (backend.RelationRevisionFencedSession, error) {
	firstRelation := -1
	for index := range steps {
		if steps[index].relation {
			firstRelation = index
			break
		}
	}
	if firstRelation < 0 {
		return nil, nil
	}
	first := steps[firstRelation]
	migration := reconstructor.definitions[first.step.Key]
	relationBackend, ok := value.(backend.RelationRevisionFencedBackend)
	if !ok || isNilInterface(relationBackend) {
		return nil, loadedRelationCapabilityError(
			first.step,
			migration,
			"backend does not implement relation revision-fenced migrations",
		)
	}
	capabilities := relationBackend.RelationMigrationCapabilities()
	if err := ctx.Err(); err != nil {
		return nil, executionContextError(first.step, err)
	}
	for _, step := range steps {
		if !step.relation {
			continue
		}
		missing := firstMissingLoadedRelationCapability(step.requirements, capabilities)
		if missing != "" {
			return nil, loadedRelationCapabilityError(
				step.step,
				reconstructor.definitions[step.step.Key],
				"required relation capability is false: "+missing,
			)
		}
	}
	relationSession, ok := session.(backend.RelationRevisionFencedSession)
	if !ok || isNilInterface(relationSession) {
		return nil, loadedRelationCapabilityError(
			first.step,
			migration,
			"fenced session does not implement relation migration begin",
		)
	}
	return relationSession, nil
}

func firstMissingLoadedRelationCapability(
	requirements loadedRelationRequirements,
	capabilities backend.RelationMigrationCapabilities,
) string {
	checks := []struct {
		bit       loadedRelationRequirements
		supported bool
		name      string
	}{
		{loadedRequiresCreateModelForeignKeys, capabilities.CreateModelForeignKeys, "CreateModelForeignKeys"},
		{loadedRequiresAddNullableForeignKey, capabilities.AddNullableForeignKey, "AddNullableForeignKey"},
		{loadedRequiresAddRequiredForeignKeyToEmptyTable, capabilities.AddRequiredForeignKeyToEmptyTable, "AddRequiredForeignKeyToEmptyTable"},
		{loadedRequiresRemoveForeignKeyByTableRemake, capabilities.RemoveForeignKeyByTableRemake, "RemoveForeignKeyByTableRemake"},
	}
	for _, check := range checks {
		if requirements&check.bit != 0 && !check.supported {
			return check.name
		}
	}
	return ""
}

func loadedRelationCapabilityError(step PlanStep, migration Migration, detail string) *Error {
	return migrationError(
		CategoryCapability,
		CodeUnsupported,
		step.Direction,
		migration,
		NoOperation,
		"",
		backend.NewCapabilityError("relation_migration", detail, nil),
	)
}

func executeLoadedFencedMigration(
	ctx context.Context,
	session backend.RevisionFencedSession,
	relationSession backend.RelationRevisionFencedSession,
	before ProjectState,
	materialized loadedMaterializedStep,
) (ProjectState, error) {
	prepared := materialized.prepared
	if err := ctx.Err(); err != nil {
		return before.Clone(), executionContextError(prepared.step, err)
	}
	transitionKind := backend.HistoryTransitionApply
	if prepared.step.Direction == DirectionBackward {
		transitionKind = backend.HistoryTransitionUnapply
	}
	transition := backend.HistoryTransition{
		Migration: backend.AppliedMigration{App: prepared.step.Key.App, Name: prepared.step.Key.Name},
		Kind:      transitionKind,
	}

	var transaction backend.RevisionFencedTransaction
	var beginErr error
	if materialized.relation {
		if isNilInterface(relationSession) {
			return before.Clone(), loadedRelationCapabilityError(
				prepared.step,
				prepared.migration,
				"validated relation step has no relation session",
			)
		}
		beginIntent := loadedBackendRelationIntent(materialized.intent)
		if err := ctx.Err(); err != nil {
			return before.Clone(), executionContextError(prepared.step, err)
		}
		transaction, beginErr = relationSession.BeginRelationFencedMigration(
			ctx,
			transition,
			beginIntent,
		)
	} else {
		transaction, beginErr = session.BeginFencedMigration(ctx, transition)
	}
	if beginErr != nil {
		primary := classifyLifecycleError(beginErr, CategoryTransaction, CodeBeginFailed, prepared.step)
		if !isNilInterface(transaction) {
			return before.Clone(), rollbackFenced(ctx, transaction, primary)
		}
		return before.Clone(), primary
	}
	if isNilInterface(transaction) {
		return before.Clone(), migrationError(
			CategoryTransaction,
			CodeBeginFailed,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			errors.New("backend returned a nil revision-fenced transaction"),
		)
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), rollbackFenced(ctx, transaction, executionContextError(prepared.step, err))
	}

	if primary := executeLoadedMigrationBody(
		ctx,
		prepared.migration,
		prepared.step.Direction,
		materialized.execution,
		transaction,
	); primary != nil {
		return before.Clone(), rollbackFenced(ctx, transaction, primary)
	}

	outcome, commitErr := transaction.CommitFenced(ctx)
	switch outcome.Durability {
	case backend.CommitCommitted:
		committed := before
		if !materialized.stateUnchanged {
			committed = prepared.after
		}
		if commitErr == nil {
			return committed, nil
		}
		return committed, migrationError(
			CategoryTransaction,
			CodeCommitCleanupFailed,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			commitErr,
		)
	case backend.CommitRolledBack:
		if commitErr == nil {
			commitErr = errors.New("backend reported a rolled-back commit without an error")
		}
		return before.Clone(), classifyLifecycleError(commitErr, CategoryTransaction, CodeCommitFailed, prepared.step)
	case backend.CommitUnknown:
		if commitErr == nil {
			commitErr = errors.New("backend reported an unknown commit outcome without an error")
		}
		return before.Clone(), migrationError(
			CategoryTransaction,
			CodeCommitOutcomeUnknown,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			commitErr,
		)
	default:
		if commitErr == nil {
			commitErr = fmt.Errorf("backend returned invalid commit durability %d", outcome.Durability)
		} else {
			commitErr = fmt.Errorf("backend returned invalid commit durability %d: %w", outcome.Durability, commitErr)
		}
		return before.Clone(), migrationError(
			CategoryTransaction,
			CodeCommitOutcomeUnknown,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			commitErr,
		)
	}
}

func loadedBackendRelationIntent(value loadedRelationIntent) backend.RelationMigrationIntent {
	intent := backend.RelationMigrationIntent{
		Operations: make([]backend.RelationMigrationOperation, len(value.operations)),
	}
	for operationIndex := range value.operations {
		operation := value.operations[operationIndex]
		targets := make([]backend.RelationMigrationTarget, len(operation.targets))
		for targetIndex := range operation.targets {
			target := operation.targets[targetIndex]
			targets[targetIndex] = backend.RelationMigrationTarget{
				SourceField: target.sourceField.Clone(),
				TargetModel: target.targetModel.Clone(),
				TargetKey:   target.targetKey.Clone(),
			}
		}
		intent.Operations[operationIndex] = backend.RelationMigrationOperation{
			OperationIndex: operation.operationIndex,
			Kind:           backend.RelationMigrationOperationKind(operation.kind),
			Before:         operation.before.Clone(),
			After:          operation.after.Clone(),
			Targets:        targets,
		}
	}
	return intent
}

func executeLoadedMigrationBody(
	ctx context.Context,
	migration Migration,
	direction Direction,
	operations []loadedOperationView,
	transaction migrationBodyTransaction,
) *Error {
	step := PlanStep{Key: migration.Key(), Direction: direction}
	for _, operation := range operations {
		if err := ctx.Err(); err != nil {
			return executionContextError(step, err)
		}
		from := loadedSparseProjectState(
			operation.beforeFormat,
			operation.appLabel,
			operation.before,
			operation.beforeExists,
		)
		to := loadedSparseProjectState(
			operation.afterFormat,
			operation.appLabel,
			operation.after,
			operation.afterExists,
		)
		if err := ctx.Err(); err != nil {
			return executionContextError(step, err)
		}
		var err error
		if direction == DirectionForward {
			err = operation.operation.databaseForward(ctx, transaction, from, to)
		} else {
			err = operation.operation.databaseBackward(ctx, transaction, from, to)
		}
		if err != nil {
			category, code := operationErrorClass(err)
			return migrationError(category, code, direction, migration, operation.index, operation.operation.Kind(), err)
		}
		if err := ctx.Err(); err != nil {
			return migrationError(
				CategoryExecution,
				CodeOperationFailed,
				direction,
				migration,
				operation.index,
				operation.operation.Kind(),
				err,
			)
		}
	}

	var err error
	if direction == DirectionForward {
		err = transaction.RecordApplied(ctx, migration.App, migration.Name)
	} else {
		err = transaction.RecordUnapplied(ctx, migration.App, migration.Name)
	}
	if err != nil {
		category, code := recorderErrorClass(err)
		return migrationError(category, code, direction, migration, NoOperation, "", err)
	}
	if err := ctx.Err(); err != nil {
		return executionContextError(step, err)
	}
	return nil
}

func consumeDefinitionHandoff(
	ctx context.Context,
	definitions []Migration,
	direction Direction,
) (context.Context, *loadedDefinitionAuthority, error) {
	baseContext, handoff, found := definitionhandoff.Take(ctx)
	hasRelation := definitionsContainRelation(definitions)
	if !found {
		if hasRelation {
			return baseContext, nil, relationMigrationUnsupported(definitions, direction, errors.New("loader definition handoff is missing"))
		}
		return baseContext, nil, nil
	}

	visible := make([]definitionhandoff.Definition, len(definitions))
	for index := range definitions {
		converted, err := migrationHandoffDefinition(definitions[index])
		if err != nil {
			return baseContext, nil, relationMigrationUnsupported(definitions, direction, fmt.Errorf("definition[%d]: %w", index, err))
		}
		visible[index] = converted
	}
	if err := handoff.ValidateVisible(visible); err != nil {
		return baseContext, nil, relationMigrationUnsupported(definitions, direction, err)
	}
	planner, err := NewPlanner(definitions...)
	if err != nil {
		return baseContext, nil, relationMigrationUnsupported(definitions, direction, fmt.Errorf("sealed full graph is invalid: %w", err))
	}
	records := handoff.Records()
	profiles := make(map[MigrationKey]loadedDefinitionProfile, len(records))
	for index := range records {
		record := records[index]
		key := MigrationKey{App: record.Definition.App, Name: record.Definition.Name}
		if _, exists := profiles[key]; exists {
			return baseContext, nil, relationMigrationUnsupported(definitions, direction, fmt.Errorf("sealed profile graph duplicates %s.%s", key.App, key.Name))
		}
		profiles[key] = loadedDefinitionProfile{
			definitionFormat: record.Profile.DefinitionFormat,
			loaderABI:        record.Profile.LoaderABI,
			operationCodec:   record.Profile.OperationCodec,
			schemaIR:         record.Profile.SchemaIR,
		}
	}
	authority := &loadedDefinitionAuthority{marker: &loadedDefinitionAuthorityMarker{}, planner: planner, profiles: profiles}
	return baseContext, authority, nil
}

func relationMigrationUnsupported(definitions []Migration, direction Direction, cause error) *Error {
	migration := Migration{}
	found := false
	for index := range definitions {
		if migrationContainsRelation(definitions[index]) {
			if !found || migrationKeyLess(definitions[index].Key(), migration.Key()) {
				migration = definitions[index]
				found = true
			}
		}
	}
	if !found && len(definitions) != 0 {
		migration = definitions[0]
		for index := 1; index < len(definitions); index++ {
			if migrationKeyLess(definitions[index].Key(), migration.Key()) {
				migration = definitions[index]
			}
		}
	}
	capability := backend.NewCapabilityError("relation_migration", "validated relation lifecycle is unavailable", cause)
	return migrationError(CategoryCapability, CodeUnsupported, direction, migration, NoOperation, "", capability)
}

func definitionsContainRelation(definitions []Migration) bool {
	for index := range definitions {
		if migrationContainsRelation(definitions[index]) {
			return true
		}
	}
	return false
}

func migrationContainsRelation(migration Migration) bool {
	for _, operation := range migration.Operations {
		switch value := operation.(type) {
		case CreateModel:
			if modelContainsRelation(value.Model) {
				return true
			}
		case *CreateModel:
			if value != nil && modelContainsRelation(value.Model) {
				return true
			}
		case AddField:
			if fieldContainsRelation(value.Field) {
				return true
			}
		case *AddField:
			if value != nil && fieldContainsRelation(value.Field) {
				return true
			}
		default:
			if embeddedBuiltinContainsRelation(operation) {
				return true
			}
		}
	}
	return false
}

var (
	createModelOperationType = reflect.TypeOf(CreateModel{})
	addFieldOperationType    = reflect.TypeOf(AddField{})
	operationInterfaceType   = reflect.TypeOf((*Operation)(nil)).Elem()
)

type embeddedBuiltinProviderKind uint8

const (
	embeddedCreateModelProvider embeddedBuiltinProviderKind = iota + 1
	embeddedAddFieldProvider
	embeddedInterfaceProvider
)

type embeddedBuiltinProvider struct {
	kind embeddedBuiltinProviderKind
	path []int
}

type embeddedBuiltinTypeNode struct {
	typeValue    reflect.Type
	path         []int
	multiplicity uint8
}

// embeddedBuiltinContainsRelation closes the Go method-promotion escape hatch
// around Operation's package-private marker. It runs only for a non-built-in
// dynamic type and inspects anonymous wrappers until it finds an embedded
// built-in operation value.
func embeddedBuiltinContainsRelation(operation Operation) bool {
	if isNilOperation(operation) {
		return false
	}
	return embeddedBuiltinValueContainsRelation(reflect.ValueOf(operation))
}

func embeddedBuiltinValueContainsRelation(value reflect.Value) bool {
	visitedPointers := make(map[uintptr]struct{})
	current := value
	for {
		unwrapped, ok := reflectedUnwrapValue(current, visitedPointers)
		if !ok {
			return true
		}
		provider, found, unambiguous := firstEmbeddedBuiltinProvider(unwrapped.Type())
		if !unambiguous {
			return true
		}
		if !found {
			return false
		}
		providerValue := unwrapped
		for _, index := range provider.path {
			providerValue, ok = reflectedUnwrapValue(providerValue, visitedPointers)
			if !ok || providerValue.Kind() != reflect.Struct || index < 0 || index >= providerValue.NumField() {
				return true
			}
			providerValue = providerValue.Field(index)
		}
		providerValue, ok = reflectedUnwrapValue(providerValue, visitedPointers)
		if !ok {
			return true
		}
		switch provider.kind {
		case embeddedCreateModelProvider:
			contains, readable := reflectedCreateModelContainsRelation(providerValue)
			return !readable || contains
		case embeddedAddFieldProvider:
			contains, readable := reflectedAddFieldContainsRelation(providerValue)
			return !readable || contains
		case embeddedInterfaceProvider:
			current = providerValue
		default:
			return true
		}
	}
}

// firstEmbeddedBuiltinProvider mirrors Go selector promotion: only built-in
// operation providers at the minimum anonymous-field depth participate. A
// deeper relation-bearing field shadowed by a shallower scalar provider is
// not the operation whose methods will execute. True same-depth ambiguity and
// an unreadable runtime provider fail closed.
func firstEmbeddedBuiltinProvider(
	root reflect.Type,
) (embeddedBuiltinProvider, bool, bool) {
	for root.Kind() == reflect.Pointer {
		root = root.Elem()
	}
	switch root {
	case createModelOperationType:
		return embeddedBuiltinProvider{kind: embeddedCreateModelProvider}, true, true
	case addFieldOperationType:
		return embeddedBuiltinProvider{kind: embeddedAddFieldProvider}, true, true
	}
	if root.Kind() != reflect.Struct {
		return embeddedBuiltinProvider{}, false, true
	}
	level := []embeddedBuiltinTypeNode{{typeValue: root, multiplicity: 1}}
	seenDepth := map[reflect.Type]int{root: 0}
	depth := 0
	for len(level) != 0 {
		depth++
		var provider embeddedBuiltinProvider
		providerCount := uint8(0)
		next := make([]embeddedBuiltinTypeNode, 0)
		nextIndex := make(map[reflect.Type]int)
		for _, node := range level {
			for index := 0; index < node.typeValue.NumField(); index++ {
				field := node.typeValue.Field(index)
				if !field.Anonymous {
					continue
				}
				path := append(append([]int(nil), node.path...), index)
				fieldType := field.Type
				if fieldType.Kind() == reflect.Interface && fieldType.Implements(operationInterfaceType) {
					if providerCount == 0 {
						provider = embeddedBuiltinProvider{kind: embeddedInterfaceProvider, path: path}
					}
					providerCount = cappedProviderMultiplicity(providerCount, node.multiplicity)
					continue
				}
				for fieldType.Kind() == reflect.Pointer {
					fieldType = fieldType.Elem()
				}
				switch fieldType {
				case createModelOperationType:
					if providerCount == 0 {
						provider = embeddedBuiltinProvider{kind: embeddedCreateModelProvider, path: path}
					}
					providerCount = cappedProviderMultiplicity(providerCount, node.multiplicity)
					continue
				case addFieldOperationType:
					if providerCount == 0 {
						provider = embeddedBuiltinProvider{kind: embeddedAddFieldProvider, path: path}
					}
					providerCount = cappedProviderMultiplicity(providerCount, node.multiplicity)
					continue
				}
				if fieldType.Kind() != reflect.Struct {
					continue
				}
				if seen, exists := seenDepth[fieldType]; exists && seen < depth {
					continue
				}
				if nextPosition, exists := nextIndex[fieldType]; exists {
					next[nextPosition].multiplicity = cappedProviderMultiplicity(next[nextPosition].multiplicity, node.multiplicity)
					continue
				}
				seenDepth[fieldType] = depth
				nextIndex[fieldType] = len(next)
				next = append(next, embeddedBuiltinTypeNode{typeValue: fieldType, path: path, multiplicity: node.multiplicity})
			}
		}
		if providerCount > 1 {
			return embeddedBuiltinProvider{}, false, false
		}
		if providerCount == 1 {
			return provider, true, true
		}
		level = next
	}
	return embeddedBuiltinProvider{}, false, true
}

func cappedProviderMultiplicity(left, right uint8) uint8 {
	if left >= 2 || right >= 2 || left+right >= 2 {
		return 2
	}
	return left + right
}

func reflectedUnwrapValue(value reflect.Value, visitedPointers map[uintptr]struct{}) (reflect.Value, bool) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		if value.Kind() == reflect.Pointer {
			identity := value.Pointer()
			if _, exists := visitedPointers[identity]; exists {
				return reflect.Value{}, false
			}
			visitedPointers[identity] = struct{}{}
		}
		value = value.Elem()
	}
	return value, value.IsValid()
}

func reflectedCreateModelContainsRelation(value reflect.Value) (bool, bool) {
	model := value.FieldByName("Model")
	if !model.IsValid() || model.Kind() != reflect.Struct {
		return false, false
	}
	fields := model.FieldByName("Fields")
	if !fields.IsValid() || fields.Kind() != reflect.Slice {
		return false, false
	}
	for index := 0; index < fields.Len(); index++ {
		contains, ok := reflectedFieldContainsRelation(fields.Index(index))
		if !ok || contains {
			return contains, ok
		}
	}
	return false, true
}

func reflectedAddFieldContainsRelation(value reflect.Value) (bool, bool) {
	field := value.FieldByName("Field")
	if !field.IsValid() {
		return false, false
	}
	return reflectedFieldContainsRelation(field)
}

func reflectedFieldContainsRelation(value reflect.Value) (bool, bool) {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return false, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return false, false
	}
	kind := value.FieldByName("Kind")
	relation := value.FieldByName("Relation")
	if !kind.IsValid() || kind.Kind() != reflect.String || !relation.IsValid() || relation.Kind() != reflect.Pointer {
		return false, false
	}
	return kind.String() == string(ir.FieldForeignKey) || !relation.IsNil(), true
}

func modelContainsRelation(model ir.Model) bool {
	for index := range model.Fields {
		if fieldContainsRelation(model.Fields[index]) {
			return true
		}
	}
	return false
}

func fieldContainsRelation(field ir.Field) bool {
	return field.Kind == ir.FieldForeignKey || field.Relation != nil
}

func migrationHandoffDefinition(value Migration) (definitionhandoff.Definition, error) {
	definition := definitionhandoff.Definition{
		App:          value.App,
		Name:         value.Name,
		Dependencies: make([]definitionhandoff.Identity, len(value.Dependencies)),
		Operations:   make([]definitionhandoff.Operation, len(value.Operations)),
	}
	for index := range value.Dependencies {
		definition.Dependencies[index] = definitionhandoff.Identity{App: value.Dependencies[index].App, Name: value.Dependencies[index].Name}
	}
	for index, operation := range value.Operations {
		converted, err := migrationHandoffOperation(operation)
		if err != nil {
			return definitionhandoff.Definition{}, fmt.Errorf("operation %d: %w", index, err)
		}
		definition.Operations[index] = converted
	}
	return definition, nil
}

func migrationHandoffOperation(value Operation) (definitionhandoff.Operation, error) {
	switch operation := value.(type) {
	case CreateModel:
		return definitionhandoff.Operation{Kind: "create_model", AppLabel: operation.AppLabel, HasModel: true, Model: migrationHandoffModel(operation.Model)}, nil
	case *CreateModel:
		if operation == nil {
			return definitionhandoff.Operation{}, errors.New("nil *CreateModel")
		}
		return definitionhandoff.Operation{Kind: "create_model", AppLabel: operation.AppLabel, HasModel: true, Model: migrationHandoffModel(operation.Model)}, nil
	case AddField:
		return definitionhandoff.Operation{Kind: "add_field", AppLabel: operation.AppLabel, ModelName: operation.ModelName, HasField: true, Field: migrationHandoffField(operation.Field)}, nil
	case *AddField:
		if operation == nil {
			return definitionhandoff.Operation{}, errors.New("nil *AddField")
		}
		return definitionhandoff.Operation{Kind: "add_field", AppLabel: operation.AppLabel, ModelName: operation.ModelName, HasField: true, Field: migrationHandoffField(operation.Field)}, nil
	default:
		return definitionhandoff.Operation{}, fmt.Errorf("unsupported operation %T", value)
	}
}

func migrationHandoffModel(value ir.Model) definitionhandoff.Model {
	model := definitionhandoff.Model{Name: value.Name, GoName: value.GoName, DBTable: value.DBTable, Fields: make([]definitionhandoff.Field, len(value.Fields))}
	for index := range value.Fields {
		model.Fields[index] = migrationHandoffField(value.Fields[index])
	}
	return model
}

func migrationHandoffField(value ir.Field) definitionhandoff.Field {
	field := definitionhandoff.Field{
		Name: value.Name, GoName: value.GoName, Column: value.Column, Kind: string(value.Kind),
		PrimaryKey: value.PrimaryKey, Nullable: value.Nullable, MaxLength: int64(value.MaxLength),
	}
	if value.Default != nil {
		field.Default = definitionhandoff.Default{
			Present: true, Kind: string(value.Default.Kind), String: value.Default.String,
			Boolean: value.Default.Boolean, Integer: value.Default.Integer,
		}
	}
	if value.Relation != nil {
		field.Relation = definitionhandoff.Relation{
			Present: true, TargetApp: value.Relation.Target.AppLabel, TargetModel: value.Relation.Target.ModelName,
			Cardinality: string(value.Relation.Cardinality), ReverseName: value.Relation.Reverse.Name,
			ReverseDisabled: value.Relation.Reverse.Disabled, OnDelete: string(value.Relation.OnDelete),
		}
	}
	return field
}

func inspectLifecycleRequest(request LifecycleRequest) (lifecycleRequestKind, []Target, error) {
	switch request.kind {
	case lifecycleRequestLatest:
		if request.targets != nil {
			return 0, nil, invalidLifecycleRequest()
		}
		return lifecycleRequestLatest, nil, nil
	case lifecycleRequestTargeted:
		if len(request.targets) == 0 {
			return 0, nil, invalidLifecycleRequest()
		}
		return lifecycleRequestTargeted, request.targets, nil
	default:
		return 0, nil, invalidLifecycleRequest()
	}
}

func validateLoadedLifecycleTargets(definitions []Migration, targets []Target) error {
	if len(targets) > definitionhandoff.MaxDefinitions {
		return invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded lifecycle target count exceeds resource limit"))
	}
	budget := loadedLifecycleIdentityBudget{}
	if err := budget.consumeDefinitions(definitions); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	if err := budget.consumeNodes("targets", len(targets)); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	for index := range targets {
		if err := budget.consumeTarget(index, targets[index]); err != nil {
			return invalidLoadedState(Migration{}, NoOperation, "", err)
		}
	}
	return nil
}

func validateLoadedHistoryPlanResources(
	definitions []Migration,
	targetCount int,
	records []backend.AppliedMigration,
) error {
	if len(records) > definitionhandoff.MaxDefinitions {
		return invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded applied-history record count exceeds resource limit"))
	}
	budget := loadedLifecycleIdentityBudget{}
	if err := budget.consumeDefinitions(definitions); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	if err := budget.consumeNodes("targets", targetCount); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	if err := budget.consumeNodes("records", len(records)); err != nil {
		return invalidLoadedState(Migration{}, NoOperation, "", err)
	}
	for index := range records {
		if err := budget.consumeString(fmt.Sprintf("records[%d].app", index), records[index].App); err != nil {
			return invalidLoadedState(Migration{}, NoOperation, "", err)
		}
		if err := budget.consumeString(fmt.Sprintf("records[%d].name", index), records[index].Name); err != nil {
			return invalidLoadedState(Migration{}, NoOperation, "", err)
		}
	}
	return nil
}

type loadedLifecycleIdentityBudget struct {
	nodes uint64
	bytes uint64
}

func (budget *loadedLifecycleIdentityBudget) consumeDefinitions(definitions []Migration) error {
	if err := budget.consumeNodes("definitions", len(definitions)); err != nil {
		return err
	}
	for definitionIndex := range definitions {
		definition := definitions[definitionIndex]
		prefix := fmt.Sprintf("definitions[%d]", definitionIndex)
		if err := budget.consumeString(prefix+".app", definition.App); err != nil {
			return err
		}
		if err := budget.consumeString(prefix+".name", definition.Name); err != nil {
			return err
		}
		if err := budget.consumeNodes(prefix+".dependencies", len(definition.Dependencies)); err != nil {
			return err
		}
		for dependencyIndex := range definition.Dependencies {
			dependency := definition.Dependencies[dependencyIndex]
			dependencyPrefix := fmt.Sprintf("%s.dependencies[%d]", prefix, dependencyIndex)
			if err := budget.consumeString(dependencyPrefix+".app", dependency.App); err != nil {
				return err
			}
			if err := budget.consumeString(dependencyPrefix+".name", dependency.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (budget *loadedLifecycleIdentityBudget) consumeTarget(index int, target Target) error {
	prefix := fmt.Sprintf("targets[%d]", index)
	switch target.kind {
	case targetNamed:
		if err := budget.consumeString(prefix+".app", target.key.App); err != nil {
			return err
		}
		return budget.consumeString(prefix+".name", target.key.Name)
	case targetZero:
		return budget.consumeString(prefix+".app", target.app)
	default:
		// Planner owns target-shape errors. Resource validation only bounds
		// caller-controlled traversal before the request is copied.
		return nil
	}
}

func (budget *loadedLifecycleIdentityBudget) consumeNodes(path string, count int) error {
	if count < 0 || uint64(count) > loadedDerivedIntentMaxNodes-budget.nodes {
		return fmt.Errorf("loaded lifecycle %s exceeds aggregate node resource limit", path)
	}
	budget.nodes += uint64(count)
	return nil
}

func (budget *loadedLifecycleIdentityBudget) consumeString(path, value string) error {
	if len(value) > definitionhandoff.MaxDefinitionBytes {
		return fmt.Errorf("loaded lifecycle identity %s has %d bytes, maximum %d", path, len(value), definitionhandoff.MaxDefinitionBytes)
	}
	if uint64(len(value)) > uint64(definitionhandoff.MaxDefinitionSetBytes)-budget.bytes {
		return fmt.Errorf("loaded lifecycle identity bytes exceed aggregate resource limit")
	}
	budget.bytes += uint64(len(value))
	return nil
}

func invalidLifecycleRequest() error {
	return newPlanningError(CategoryPlan, CodeInvalidTarget, MigrationKey{}, MigrationKey{}, nil)
}

func executeFencedMigration(
	ctx context.Context,
	session backend.RevisionFencedSession,
	before ProjectState,
	prepared preparedPlanStep,
) (ProjectState, error) {
	// The outer loop owns the between-step gate. Repeat it immediately before
	// handing control to the backend so cancellation cannot open a transaction
	// in the small interval after that check.
	if err := ctx.Err(); err != nil {
		return before.Clone(), executionContextError(prepared.step, err)
	}

	transitionKind := backend.HistoryTransitionApply
	if prepared.step.Direction == DirectionBackward {
		transitionKind = backend.HistoryTransitionUnapply
	}
	transition := backend.HistoryTransition{
		Migration: backend.AppliedMigration{App: prepared.step.Key.App, Name: prepared.step.Key.Name},
		Kind:      transitionKind,
	}

	transaction, err := session.BeginFencedMigration(ctx, transition)
	if err != nil {
		return before.Clone(), classifyLifecycleError(err, CategoryTransaction, CodeBeginFailed, prepared.step)
	}
	if isNilInterface(transaction) {
		return before.Clone(), migrationError(
			CategoryTransaction,
			CodeBeginFailed,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			errors.New("backend returned a nil revision-fenced transaction"),
		)
	}

	if primary := executeMigrationBody(
		ctx,
		prepared.migration,
		prepared.step.Direction,
		prepared.operations,
		transaction,
	); primary != nil {
		return before.Clone(), rollbackFenced(ctx, transaction, primary)
	}

	outcome, commitErr := transaction.CommitFenced(ctx)
	switch outcome.Durability {
	case backend.CommitCommitted:
		if commitErr == nil {
			return prepared.after.Clone(), nil
		}
		return prepared.after.Clone(), migrationError(
			CategoryTransaction,
			CodeCommitCleanupFailed,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			commitErr,
		)
	case backend.CommitRolledBack:
		if commitErr == nil {
			commitErr = errors.New("backend reported a rolled-back commit without an error")
		}
		return before.Clone(), classifyLifecycleError(commitErr, CategoryTransaction, CodeCommitFailed, prepared.step)
	case backend.CommitUnknown:
		if commitErr == nil {
			commitErr = errors.New("backend reported an unknown commit outcome without an error")
		}
		return before.Clone(), migrationError(
			CategoryTransaction,
			CodeCommitOutcomeUnknown,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			commitErr,
		)
	default:
		if commitErr == nil {
			commitErr = fmt.Errorf("backend returned invalid commit durability %d", outcome.Durability)
		} else {
			commitErr = fmt.Errorf("backend returned invalid commit durability %d: %w", outcome.Durability, commitErr)
		}
		return before.Clone(), migrationError(
			CategoryTransaction,
			CodeCommitOutcomeUnknown,
			prepared.step.Direction,
			prepared.migration,
			NoOperation,
			"",
			commitErr,
		)
	}
}

func rollbackFenced(ctx context.Context, transaction backend.RevisionFencedTransaction, primary *Error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lifecycleCleanupTimeout)
	defer cancel()
	if err := transaction.Rollback(cleanupCtx); err != nil {
		primary.RollbackCause = err
	}
	return primary
}

func revisionFenceUnsupportedError(cause error) *Error {
	return migrationError(
		CategoryCapability,
		CodeRevisionFenceUnsupported,
		"",
		Migration{},
		NoOperation,
		"",
		cause,
	)
}

func classifyLifecycleError(err error, fallbackCategory ErrorCategory, fallbackCode ErrorCode, step PlanStep) *Error {
	category, code := fallbackCategory, fallbackCode
	if classifiedCategory, classifiedCode, ok := backendErrorClass(err); ok {
		category, code = classifiedCategory, classifiedCode
	}
	return migrationError(
		category,
		code,
		step.Direction,
		Migration{App: step.Key.App, Name: step.Key.Name},
		NoOperation,
		"",
		err,
	)
}

func revisionFenceErrorClass(err error) (ErrorCategory, ErrorCode, bool) {
	var fenceError *backend.RevisionFenceError
	if !errors.As(err, &fenceError) {
		return "", "", false
	}
	// A typed-nil carrier or an unknown/zero kind is itself malformed fence
	// state. Fail closed as integrity instead of degrading it according to the
	// operation stage that happened to expose it.
	if fenceError == nil {
		return CategoryHistory, CodeHistoryRevisionIntegrity, true
	}
	switch fenceError.Kind {
	case backend.RevisionFenceFailureAdoptionRequired:
		return CategoryCapability, CodeRevisionFenceAdoptionRequired, true
	case backend.RevisionFenceFailureStale:
		return CategoryConflict, CodeStaleHistoryRevision, true
	case backend.RevisionFenceFailureContended:
		return CategoryTransaction, CodeHistoryRevisionContended, true
	case backend.RevisionFenceFailureIntegrity:
		return CategoryHistory, CodeHistoryRevisionIntegrity, true
	default:
		return CategoryHistory, CodeHistoryRevisionIntegrity, true
	}
}
