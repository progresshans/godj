package migrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

func TestExecutorPreflightsAllStateBeforeBeginningTransaction(t *testing.T) {
	t.Parallel()

	fake := &fakeBackend{transaction: newFakeTransaction()}
	migration := Migration{
		App:  "news",
		Name: "0001_invalid",
		Operations: []Operation{
			CreateModel{AppLabel: "news", Model: articleSchema().Models[0]},
			AddField{AppLabel: "news", ModelName: "missing", Field: summaryField()},
		},
	}
	before := EmptyProjectState()
	after, err := (Executor{Backend: fake}).Apply(context.Background(), before, migration)
	assertMigrationError(t, err, CategoryState, CodeInvalidState, 1, "AddField")
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
	if !after.Equal(before) {
		t.Fatal("invalid preflight changed returned state")
	}
}

func TestExecutorRejectsCanceledContextBeforeIO(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeBackend{transaction: newFakeTransaction()}
	before := EmptyProjectState()
	after, err := (Executor{Backend: fake}).Apply(ctx, before, articleMigration())
	assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context.Canceled", err)
	}
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
	if !after.Equal(before) {
		t.Fatal("canceled execution changed returned state")
	}
}

func TestExecutorRejectsContextCanceledDuringStatePreflightBeforeIO(t *testing.T) {
	t.Parallel()

	for _, direction := range []Direction{DirectionForward, DirectionBackward} {
		direction := direction
		t.Run(string(direction), func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			fake := &fakeBackend{transaction: newFakeTransaction()}
			migration := Migration{
				App:  "news",
				Name: "0001_cancel_during_preflight",
				Operations: []Operation{
					cancelDuringPreflightOperation{cancel: cancel},
				},
			}
			before := EmptyProjectState()
			var (
				after ProjectState
				err   error
			)
			if direction == DirectionForward {
				after, err = (Executor{Backend: fake}).Apply(ctx, before, migration)
			} else {
				after, err = (Executor{Backend: fake}).Unapply(ctx, before, migration)
			}
			assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, NoOperation, "")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("execute after preflight cancellation error = %v, want context.Canceled", err)
			}
			if fake.beginCount != 0 {
				t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
			}
			if !after.Equal(before) {
				t.Fatal("preflight cancellation changed returned state")
			}
		})
	}
}

type cancelDuringPreflightOperation struct {
	cancel context.CancelFunc
}

func (op cancelDuringPreflightOperation) Kind() string { return "CancelDuringPreflight" }

func (cancelDuringPreflightOperation) operation() {}

func (cancelDuringPreflightOperation) App() string { return "news" }

func (op cancelDuringPreflightOperation) stateForward(state ProjectState) (ProjectState, error) {
	op.cancel()
	return state.Clone(), nil
}

func (op cancelDuringPreflightOperation) stateBackward(state ProjectState) (ProjectState, error) {
	op.cancel()
	return state.Clone(), nil
}

func (cancelDuringPreflightOperation) databaseForward(context.Context, backend.SchemaEditor, ProjectState, ProjectState) error {
	return nil
}

func (cancelDuringPreflightOperation) databaseBackward(context.Context, backend.SchemaEditor, ProjectState, ProjectState) error {
	return nil
}

func TestExecutorApplyCommitsOperationsAndRecordInOrder(t *testing.T) {
	t.Parallel()

	transaction := newFakeTransaction()
	fake := &fakeBackend{transaction: transaction}
	migration := articleMigration()
	before := EmptyProjectState()
	after, err := (Executor{Backend: fake}).Apply(context.Background(), before, migration)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	assertCalls(t, transaction.calls, "create_model", "add_field", "record_applied:news.0001_article", "commit")
	model, exists := after.Model("news", "article")
	if !exists || len(model.Fields) != 4 || model.Fields[3].Name != "summary" {
		t.Fatalf("applied model = %#v, exists = %t", model, exists)
	}
	if !before.Equal(EmptyProjectState()) {
		t.Fatal("Apply() mutated original state")
	}
}

func TestExecutorUnapplyUsesReverseOrderAndUnrecords(t *testing.T) {
	t.Parallel()

	migration := articleMigration()
	applied, err := preflightState(EmptyProjectState(), migration, DirectionForward)
	if err != nil {
		t.Fatal(err)
	}
	transaction := newFakeTransaction()
	after, err := (Executor{Backend: &fakeBackend{transaction: transaction}}).Unapply(context.Background(), applied, migration)
	if err != nil {
		t.Fatalf("Unapply() error = %v", err)
	}
	assertCalls(t, transaction.calls, "remove_field", "delete_model", "record_unapplied:news.0001_article", "commit")
	if !after.Equal(EmptyProjectState()) {
		t.Fatalf("Unapply() state apps = %v, want empty", after.Apps())
	}
	if _, exists := applied.Model("news", "article"); !exists {
		t.Fatal("Unapply() mutated original state")
	}
}

func TestExecutorOperationFailureRollsBackAndPreservesOriginalState(t *testing.T) {
	t.Parallel()

	operationCause := errors.New("forced add failure")
	transaction := newFakeTransaction()
	transaction.failures["add_field"] = operationCause
	before := EmptyProjectState()
	after, err := (Executor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), before, articleMigration())
	assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 1, "AddField")
	if !errors.Is(err, operationCause) {
		t.Fatalf("Apply() error = %v, want operation cause", err)
	}
	assertCalls(t, transaction.calls, "create_model", "add_field", "rollback")
	if !after.Equal(before) {
		t.Fatal("operation failure changed returned state")
	}
}

func TestExecutorCapabilityFailureIsStructuredAndRollsBack(t *testing.T) {
	t.Parallel()

	capabilityCause := backend.NewCapabilityError("drop_column", "indexed field", nil)
	transaction := newFakeTransaction()
	transaction.failures["remove_field"] = capabilityCause
	migration := articleMigration()
	applied, err := preflightState(EmptyProjectState(), migration, DirectionForward)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (Executor{Backend: &fakeBackend{transaction: transaction}}).Unapply(context.Background(), applied, migration)
	assertMigrationError(t, err, CategoryCapability, CodeUnsupported, 1, "AddField")
	if !errors.Is(err, capabilityCause) {
		t.Fatalf("Unapply() error = %v, want capability cause", err)
	}
	assertCalls(t, transaction.calls, "remove_field", "rollback")
}

func TestExecutorRecorderFailureRollsBackAndPreservesOriginalState(t *testing.T) {
	t.Parallel()

	recorderCause := errors.New("forced recorder failure")
	transaction := newFakeTransaction()
	transaction.failures["record_applied"] = recorderCause
	before := EmptyProjectState()
	after, err := (Executor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), before, articleMigration())
	assertMigrationError(t, err, CategoryRecorder, CodeRecordFailed, NoOperation, "")
	if !errors.Is(err, recorderCause) {
		t.Fatalf("Apply() error = %v, want recorder cause", err)
	}
	assertCalls(t, transaction.calls, "create_model", "add_field", "record_applied:news.0001_article", "rollback")
	if !after.Equal(before) {
		t.Fatal("recorder failure changed returned state")
	}
}

func TestExecutorReverseRecorderFailureRollsBackAndPreservesOriginalState(t *testing.T) {
	t.Parallel()

	recorderCause := errors.New("forced reverse recorder failure")
	transaction := newFakeTransaction()
	transaction.failures["record_unapplied"] = recorderCause
	migration := articleMigration()
	before, err := preflightState(EmptyProjectState(), migration, DirectionForward)
	if err != nil {
		t.Fatal(err)
	}
	after, err := (Executor{Backend: &fakeBackend{transaction: transaction}}).Unapply(context.Background(), before, migration)
	assertMigrationError(t, err, CategoryRecorder, CodeRecordFailed, NoOperation, "")
	if !errors.Is(err, recorderCause) {
		t.Fatalf("Unapply() error = %v, want recorder cause", err)
	}
	assertCalls(t, transaction.calls, "remove_field", "delete_model", "record_unapplied:news.0001_article", "rollback")
	if !after.Equal(before) {
		t.Fatal("reverse recorder failure changed returned state")
	}
}

func TestExecutorCommitAndRollbackFailuresPreserveBothCauses(t *testing.T) {
	t.Parallel()

	commitCause := errors.New("forced commit failure")
	rollbackCause := errors.New("forced rollback failure")
	transaction := newFakeTransaction()
	transaction.failures["commit"] = commitCause
	transaction.failures["rollback"] = rollbackCause
	before := EmptyProjectState()
	after, err := (Executor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), before, articleMigration())
	assertMigrationError(t, err, CategoryTransaction, CodeCommitFailed, NoOperation, "")
	if !errors.Is(err, commitCause) || !errors.Is(err, rollbackCause) {
		t.Fatalf("Apply() error = %v, want commit and rollback causes", err)
	}
	var migrationError *Error
	if !errors.As(err, &migrationError) || migrationError.RollbackCause != rollbackCause {
		t.Fatalf("RollbackCause = %#v, want %#v", migrationError.RollbackCause, rollbackCause)
	}
	assertCalls(t, transaction.calls, "create_model", "add_field", "record_applied:news.0001_article", "commit", "rollback")
	if !after.Equal(before) {
		t.Fatal("commit failure changed returned state")
	}
}

func TestExecutorOperationAndRollbackFailuresPreserveBothCauses(t *testing.T) {
	t.Parallel()

	operationCause := errors.New("forced operation failure")
	rollbackCause := errors.New("forced rollback failure")
	transaction := newFakeTransaction()
	transaction.failures["create_model"] = operationCause
	transaction.failures["rollback"] = rollbackCause
	_, err := (Executor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), EmptyProjectState(), articleMigration())
	assertMigrationError(t, err, CategoryExecution, CodeOperationFailed, 0, "CreateModel")
	if !errors.Is(err, operationCause) || !errors.Is(err, rollbackCause) {
		t.Fatalf("Apply() error = %v, want operation and rollback causes", err)
	}
	assertCalls(t, transaction.calls, "create_model", "rollback")
}

func TestExecutorBeginFailureDoesNotAttemptRollback(t *testing.T) {
	t.Parallel()

	beginCause := errors.New("forced begin failure")
	fake := &fakeBackend{beginError: beginCause, transaction: newFakeTransaction()}
	_, err := (Executor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), articleMigration())
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
	if !errors.Is(err, beginCause) {
		t.Fatalf("Apply() error = %v, want begin cause", err)
	}
	if len(fake.transaction.calls) != 0 {
		t.Fatalf("transaction calls = %v, want empty", fake.transaction.calls)
	}
}

func TestExecutorRejectsTypedNilBackendAndTransaction(t *testing.T) {
	t.Parallel()

	var nilBackend *fakeBackend
	_, err := (Executor{Backend: nilBackend}).Apply(context.Background(), EmptyProjectState(), articleMigration())
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")

	fake := &fakeBackend{}
	_, err = (Executor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), articleMigration())
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
	if fake.beginCount != 1 {
		t.Fatalf("BeginMigration() calls = %d, want 1", fake.beginCount)
	}
}

func TestExecutorRejectsOperationFromAnotherAppBeforeIO(t *testing.T) {
	t.Parallel()

	fake := &fakeBackend{transaction: newFakeTransaction()}
	migration := articleMigration()
	migration.Operations[1] = AddField{AppLabel: "other", ModelName: "article", Field: summaryField()}
	_, err := (Executor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), migration)
	assertMigrationError(t, err, CategoryState, CodeInvalidState, 1, "AddField")
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
}

func TestExecutorRejectsTypedNilOperationBeforeIO(t *testing.T) {
	t.Parallel()

	var operation *CreateModel
	fake := &fakeBackend{transaction: newFakeTransaction()}
	migration := Migration{App: "news", Name: "0001_nil", Operations: []Operation{operation}}
	_, err := (Executor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), migration)
	assertMigrationError(t, err, CategoryState, CodeInvalidState, 0, "")
	if fake.beginCount != 0 {
		t.Fatalf("BeginMigration() calls = %d, want 0", fake.beginCount)
	}
}

func articleMigration() Migration {
	return Migration{
		App:  "news",
		Name: "0001_article",
		Operations: []Operation{
			CreateModel{AppLabel: "news", Model: articleSchema().Models[0]},
			AddField{AppLabel: "news", ModelName: "article", Field: summaryField()},
		},
	}
}

func preflightState(before ProjectState, migration Migration, direction Direction) (ProjectState, error) {
	_, after, err := preflight(before, migration, direction)
	return after, err
}

type fakeBackend struct {
	transaction *fakeTransaction
	beginError  error
	beginCount  int
}

func (b *fakeBackend) BeginMigration(context.Context) (backend.Transaction, error) {
	b.beginCount++
	if b.beginError != nil {
		return nil, b.beginError
	}
	return b.transaction, nil
}

type fakeTransaction struct {
	calls    []string
	failures map[string]error
}

func newFakeTransaction() *fakeTransaction {
	return &fakeTransaction{failures: make(map[string]error)}
}

func (t *fakeTransaction) CreateModel(context.Context, ir.Model) error {
	return t.call("create_model")
}

func (t *fakeTransaction) DeleteModel(context.Context, ir.Model) error {
	return t.call("delete_model")
}

func (t *fakeTransaction) AddField(context.Context, ir.Model, ir.Field) error {
	return t.call("add_field")
}

func (t *fakeTransaction) RemoveField(context.Context, ir.Model, ir.Field) error {
	return t.call("remove_field")
}

func (t *fakeTransaction) RecordApplied(_ context.Context, app, name string) error {
	t.calls = append(t.calls, fmt.Sprintf("record_applied:%s.%s", app, name))
	return t.failures["record_applied"]
}

func (t *fakeTransaction) RecordUnapplied(_ context.Context, app, name string) error {
	t.calls = append(t.calls, fmt.Sprintf("record_unapplied:%s.%s", app, name))
	return t.failures["record_unapplied"]
}

func (t *fakeTransaction) Commit(context.Context) error {
	return t.call("commit")
}

func (t *fakeTransaction) Rollback(context.Context) error {
	return t.call("rollback")
}

func (t *fakeTransaction) call(name string) error {
	t.calls = append(t.calls, name)
	return t.failures[name]
}

func assertMigrationError(t *testing.T, err error, category ErrorCategory, code ErrorCode, operationIndex int, operation string) {
	t.Helper()
	var migrationError *Error
	if !errors.As(err, &migrationError) {
		t.Fatalf("error = %#v, want *migrations.Error", err)
	}
	if migrationError.Category != category || migrationError.Code != code || migrationError.OperationIndex != operationIndex || migrationError.Operation != operation {
		t.Fatalf("migration error = %#v, want category=%s code=%s operation[%d]=%s", migrationError, category, code, operationIndex, operation)
	}
}

func assertCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
