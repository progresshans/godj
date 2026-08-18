package migrations

import (
	"context"
	"errors"
	"fmt"
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

	requestKind, requestTargets, err := snapshotLifecycleRequest(request)
	if err != nil {
		return resultState, err
	}

	// Snapshot caller-owned definitions before any backend I/O. The
	// reconstructor then validates the graph and deep-copies every supported
	// operation so neither planning nor execution retains caller aliases.
	definitionSnapshot := cloneMigrationDefinitions(definitions)
	ctx, err = consumeDefinitionHandoff(ctx, definitionSnapshot, "")
	if err != nil {
		return resultState, err
	}
	if err := ctx.Err(); err != nil {
		return resultState, executionContextError(PlanStep{}, err)
	}
	reconstructor, err := NewStateReconstructor(definitionSnapshot...)
	if err != nil {
		return resultState, err
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

func consumeDefinitionHandoff(
	ctx context.Context,
	definitions []Migration,
	direction Direction,
) (context.Context, error) {
	baseContext, handoff, found := definitionhandoff.Take(ctx)
	hasRelation := definitionsContainRelation(definitions)
	if !found {
		if hasRelation {
			return baseContext, relationMigrationUnsupported(definitions, direction, errors.New("loader definition handoff is missing"))
		}
		return baseContext, nil
	}

	visible := make([]definitionhandoff.Definition, len(definitions))
	for index := range definitions {
		converted, err := migrationHandoffDefinition(definitions[index])
		if err != nil {
			return baseContext, relationMigrationUnsupported(definitions, direction, fmt.Errorf("definition[%d]: %w", index, err))
		}
		visible[index] = converted
	}
	if err := handoff.ValidateVisible(visible); err != nil {
		return baseContext, relationMigrationUnsupported(definitions, direction, err)
	}
	if _, err := NewPlanner(definitions...); err != nil {
		return baseContext, relationMigrationUnsupported(definitions, direction, fmt.Errorf("sealed full graph is invalid: %w", err))
	}
	if hasRelation {
		return baseContext, relationMigrationUnsupported(
			definitions,
			direction,
			errors.New("relation historical state handoff is not implemented"),
		)
	}
	return baseContext, nil
}

func relationMigrationUnsupported(definitions []Migration, direction Direction, cause error) *Error {
	migration := Migration{}
	found := false
	for index := range definitions {
		if migrationContainsRelation(definitions[index]) {
			migration = definitions[index]
			found = true
			break
		}
	}
	if !found && len(definitions) != 0 {
		migration = definitions[0]
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
		}
	}
	return false
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

func snapshotLifecycleRequest(request LifecycleRequest) (lifecycleRequestKind, []Target, error) {
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
		return lifecycleRequestTargeted, append([]Target(nil), request.targets...), nil
	default:
		return 0, nil, invalidLifecycleRequest()
	}
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
