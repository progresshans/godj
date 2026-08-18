package migrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/migrations/backend"
)

type Direction string

const (
	DirectionForward  Direction = "forward"
	DirectionBackward Direction = "backward"
)

type ErrorCategory string

const (
	CategoryState       ErrorCategory = "migration_state_error"
	CategoryCapability  ErrorCategory = "migration_capability_error"
	CategoryExecution   ErrorCategory = "migration_execution_error"
	CategoryRecorder    ErrorCategory = "migration_recorder_error"
	CategoryTransaction ErrorCategory = "migration_transaction_error"
	CategoryPlan        ErrorCategory = "migration_plan_error"
	CategoryHistory     ErrorCategory = "migration_history_error"
	CategoryGraph       ErrorCategory = "migration_graph_error"
	CategoryConflict    ErrorCategory = "migration_conflict_error"
)

type ErrorCode string

const (
	CodeInvalidState                  ErrorCode = "invalid_state"
	CodeUnsupported                   ErrorCode = "unsupported_operation"
	CodeOperationFailed               ErrorCode = "operation_failed"
	CodeRecordFailed                  ErrorCode = "record_failed"
	CodeBeginFailed                   ErrorCode = "begin_failed"
	CodeCommitFailed                  ErrorCode = "commit_failed"
	CodeInvalidNode                   ErrorCode = "invalid_node"
	CodeDuplicateNode                 ErrorCode = "duplicate_node"
	CodeInvalidDependency             ErrorCode = "invalid_dependency"
	CodeDuplicateDependency           ErrorCode = "duplicate_dependency"
	CodeDependencyNotFound            ErrorCode = "dependency_not_found"
	CodeDependencyCycle               ErrorCode = "dependency_cycle"
	CodeInvalidAppliedState           ErrorCode = "invalid_applied_state"
	CodeDuplicateApplied              ErrorCode = "duplicate_applied"
	CodeInconsistentAppliedHistory    ErrorCode = "inconsistent_applied_history"
	CodeInvalidTarget                 ErrorCode = "invalid_target"
	CodeTargetNotFound                ErrorCode = "target_not_found"
	CodeInvalidExecutionPlan          ErrorCode = "invalid_execution_plan"
	CodeMixedDirections               ErrorCode = "mixed_directions"
	CodeRevisionFenceUnsupported      ErrorCode = "revision_fence_unsupported"
	CodeRevisionFenceAdoptionRequired ErrorCode = "revision_fence_adoption_required"
	CodeStaleHistoryRevision          ErrorCode = "stale_history_revision"
	CodeHistoryRevisionContended      ErrorCode = "history_revision_contended"
	CodeCommitOutcomeUnknown          ErrorCode = "commit_outcome_unknown"
	CodeCommitCleanupFailed           ErrorCode = "commit_cleanup_failed"
	CodeSessionCloseFailed            ErrorCode = "session_close_failed"
	CodeHistoryRevisionIntegrity      ErrorCode = "history_revision_integrity"
)

const NoOperation = -1

type Error struct {
	Category       ErrorCategory
	Code           ErrorCode
	Direction      Direction
	App            string
	Migration      string
	OperationIndex int
	Operation      string
	Cause          error
	RollbackCause  error
}

func (e *Error) Error() string {
	if e == nil {
		return "migration error"
	}
	message := fmt.Sprintf("%s/%s %s %s.%s", e.Category, e.Code, e.Direction, e.App, e.Migration)
	if e.OperationIndex != NoOperation {
		message += fmt.Sprintf(" operation[%d]=%s", e.OperationIndex, e.Operation)
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	if e.RollbackCause != nil {
		message += "; rollback failed: " + e.RollbackCause.Error()
	}
	return message
}

// Unwrap joins the primary and rollback causes so errors.Is/errors.As can
// inspect both failures without losing the primary error classification.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return errors.Join(e.Cause, e.RollbackCause)
}

type Migration struct {
	App          string
	Name         string
	Dependencies []MigrationKey
	Operations   []Operation
}

// Key returns the stable identity used by the migration planner. It does not
// validate the migration; NewPlanner owns graph validation and diagnostics.
func (m Migration) Key() MigrationKey {
	return MigrationKey{App: m.App, Name: m.Name}
}

type Executor struct {
	Backend backend.AtomicBackend
}

func (e Executor) Apply(ctx context.Context, before ProjectState, migration Migration) (ProjectState, error) {
	return e.execute(ctx, before, migration, DirectionForward)
}

func (e Executor) Unapply(ctx context.Context, before ProjectState, migration Migration) (ProjectState, error) {
	return e.execute(ctx, before, migration, DirectionBackward)
}

type preparedOperation struct {
	index int
	op    Operation
	from  ProjectState
	to    ProjectState
}

func (e Executor) execute(ctx context.Context, before ProjectState, migration Migration, direction Direction) (ProjectState, error) {
	if ctx == nil {
		return before.Clone(), migrationError(CategoryExecution, CodeOperationFailed, direction, migration, NoOperation, "", errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), migrationError(CategoryExecution, CodeOperationFailed, direction, migration, NoOperation, "", err)
	}
	migrationSnapshot := cloneMigrationDefinitions([]Migration{migration})[0]
	var err error
	ctx, err = consumeDefinitionHandoff(ctx, []Migration{migrationSnapshot}, direction)
	if err != nil {
		return before.Clone(), err
	}
	if err := ctx.Err(); err != nil {
		return before.Clone(), migrationError(CategoryExecution, CodeOperationFailed, direction, migrationSnapshot, NoOperation, "", err)
	}
	prepared, after, err := preflight(before, migrationSnapshot, direction)
	if err != nil {
		return before.Clone(), err
	}
	// State preflight is intentionally pure, but it can be non-trivial. Honor
	// cancellation that arrives while it runs before touching the backend.
	if err := ctx.Err(); err != nil {
		return before.Clone(), migrationError(CategoryExecution, CodeOperationFailed, direction, migrationSnapshot, NoOperation, "", err)
	}
	if isNilInterface(e.Backend) {
		return before.Clone(), migrationError(CategoryTransaction, CodeBeginFailed, direction, migrationSnapshot, NoOperation, "", errors.New("backend is nil"))
	}
	transaction, err := e.Backend.BeginMigration(ctx)
	if err != nil {
		category, code := beginErrorClass(err)
		return before.Clone(), migrationError(category, code, direction, migrationSnapshot, NoOperation, "", err)
	}
	if isNilInterface(transaction) {
		return before.Clone(), migrationError(CategoryTransaction, CodeBeginFailed, direction, migrationSnapshot, NoOperation, "", errors.New("backend returned a nil transaction"))
	}

	if primary := executeMigrationBody(ctx, migrationSnapshot, direction, prepared, transaction); primary != nil {
		return before.Clone(), rollback(ctx, transaction, primary)
	}
	if err := transaction.Commit(ctx); err != nil {
		primary := migrationError(CategoryTransaction, CodeCommitFailed, direction, migrationSnapshot, NoOperation, "", err)
		return before.Clone(), rollback(ctx, transaction, primary)
	}
	return after.Clone(), nil
}

// migrationBodyTransaction is the common operation/recorder boundary shared
// by legacy and revision-fenced transactions. Begin, commit, rollback, and
// durability classification deliberately stay outside this kernel.
type migrationBodyTransaction interface {
	backend.SchemaEditor
	backend.Recorder
}

func executeMigrationBody(
	ctx context.Context,
	migration Migration,
	direction Direction,
	prepared []preparedOperation,
	transaction migrationBodyTransaction,
) *Error {
	for _, preparedOperation := range prepared {
		var err error
		if direction == DirectionForward {
			err = preparedOperation.op.databaseForward(ctx, transaction, preparedOperation.from, preparedOperation.to)
		} else {
			err = preparedOperation.op.databaseBackward(ctx, transaction, preparedOperation.from, preparedOperation.to)
		}
		if err != nil {
			category, code := operationErrorClass(err)
			return migrationError(category, code, direction, migration, preparedOperation.index, preparedOperation.op.Kind(), err)
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
	return nil
}

func preflight(before ProjectState, migration Migration, direction Direction) ([]preparedOperation, ProjectState, error) {
	if err := before.validate(); err != nil {
		return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", err)
	}
	if migration.App == "" {
		return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", errors.New("migration app is empty"))
	}
	if migration.Name == "" {
		return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, NoOperation, "", errors.New("migration name is empty"))
	}
	state := before.Clone()
	indices := operationIndices(len(migration.Operations), direction)
	prepared := make([]preparedOperation, 0, len(indices))
	for _, index := range indices {
		op := migration.Operations[index]
		if isNilOperation(op) {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, "", errors.New("operation is nil"))
		}
		if op.App() != migration.App {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, op.Kind(), fmt.Errorf("operation app %q does not match migration app %q", op.App(), migration.App))
		}
		from := state.Clone()
		var next ProjectState
		var err error
		if direction == DirectionForward {
			next, err = op.stateForward(from)
		} else {
			next, err = op.stateBackward(from)
		}
		if err != nil {
			return nil, before.Clone(), migrationError(CategoryState, CodeInvalidState, direction, migration, index, op.Kind(), err)
		}
		prepared = append(prepared, preparedOperation{index: index, op: op, from: from, to: next.Clone()})
		state = next
	}
	return prepared, state.Clone(), nil
}

func isNilOperation(operation Operation) bool {
	return isNilInterface(operation)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func operationIndices(length int, direction Direction) []int {
	indices := make([]int, length)
	if direction == DirectionForward {
		for index := range indices {
			indices[index] = index
		}
		return indices
	}
	for index := range indices {
		indices[index] = length - index - 1
	}
	return indices
}

func operationErrorClass(err error) (ErrorCategory, ErrorCode) {
	if category, code, ok := backendErrorClass(err); ok {
		return category, code
	}
	return CategoryExecution, CodeOperationFailed
}

func recorderErrorClass(err error) (ErrorCategory, ErrorCode) {
	// Recorder failures have an established public category/code contract.
	// Only the dedicated raw revision-fence carrier may override it: a generic
	// backend CapabilityError at this stage is still a failed recorder write,
	// not an unsupported schema operation.
	if category, code, ok := revisionFenceErrorClass(err); ok {
		return category, code
	}
	return CategoryRecorder, CodeRecordFailed
}

func beginErrorClass(err error) (ErrorCategory, ErrorCode) {
	if category, code, ok := backendErrorClass(err); ok {
		return category, code
	}
	return CategoryTransaction, CodeBeginFailed
}

func backendErrorClass(err error) (ErrorCategory, ErrorCode, bool) {
	if category, code, ok := revisionFenceErrorClass(err); ok {
		return category, code, true
	}
	if backend.IsCapabilityError(err) {
		return CategoryCapability, CodeUnsupported, true
	}
	return "", "", false
}

func migrationError(category ErrorCategory, code ErrorCode, direction Direction, migration Migration, operationIndex int, operation string, cause error) *Error {
	return &Error{
		Category:       category,
		Code:           code,
		Direction:      direction,
		App:            migration.App,
		Migration:      migration.Name,
		OperationIndex: operationIndex,
		Operation:      operation,
		Cause:          cause,
	}
}

func rollback(ctx context.Context, transaction backend.Transaction, primary *Error) error {
	// Cleanup must still run after the operation context is canceled. Values
	// remain available to backend logging while cancellation and deadlines are
	// deliberately removed for the best-effort terminal I/O.
	cleanupContext := context.WithoutCancel(ctx)
	if err := transaction.Rollback(cleanupContext); err != nil {
		primary.RollbackCause = err
	}
	return primary
}
