package migrations

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

type stagedRawCancellationContext struct {
	context.Context
	calls    atomic.Int32
	cancelAt int32
}

type interfaceEmbeddedOperation struct {
	Operation
}

type nestedEmbeddedRelation struct {
	AddField
}

type shadowedEmbeddedOperation struct {
	CreateModel
	nestedEmbeddedRelation
}

type wideScalarEmbeddedOperation struct {
	f00 uint8
	f01 uint8
	f02 uint8
	f03 uint8
	f04 uint8
	f05 uint8
	f06 uint8
	f07 uint8
	f08 uint8
	f09 uint8
	f10 uint8
	f11 uint8
	f12 uint8
	f13 uint8
	f14 uint8
	f15 uint8
	f16 uint8
	f17 uint8
	f18 uint8
	f19 uint8
	f20 uint8
	f21 uint8
	f22 uint8
	f23 uint8
	f24 uint8
	f25 uint8
	f26 uint8
	f27 uint8
	f28 uint8
	f29 uint8
	f30 uint8
	f31 uint8
	f32 uint8
	f33 uint8
	f34 uint8
	f35 uint8
	f36 uint8
	f37 uint8
	f38 uint8
	f39 uint8
	f40 uint8
	f41 uint8
	f42 uint8
	f43 uint8
	f44 uint8
	f45 uint8
	f46 uint8
	f47 uint8
	f48 uint8
	f49 uint8
	f50 uint8
	f51 uint8
	f52 uint8
	f53 uint8
	f54 uint8
	f55 uint8
	f56 uint8
	f57 uint8
	f58 uint8
	f59 uint8
	f60 uint8
	f61 uint8
	f62 uint8
	f63 uint8
	f64 uint8
	CreateModel
}

func (value *stagedRawCancellationContext) Err() error {
	if value.calls.Add(1) >= value.cancelAt {
		return context.Canceled
	}
	return nil
}

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
	after, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), before, migration)
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
	after, err := (DirectExecutor{Backend: fake}).Apply(ctx, before, articleMigration())
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

func TestExecutorRawRelationApplyAndUnapplyFailCapabilityBeforeIO(t *testing.T) {
	t.Parallel()

	relation := relationMigrationField()
	hidden := summaryField()
	hidden.Relation = relation.Relation
	tests := []struct {
		name      string
		migration Migration
	}{
		{
			name: "ForeignKey kind",
			migration: Migration{App: "blog", Name: "0002_author", Operations: []Operation{AddField{
				AppLabel: "blog", ModelName: "post", Field: relation,
			}}},
		},
		{
			name: "hidden relation arm on scalar kind",
			migration: Migration{App: "blog", Name: "0002_hidden", Operations: []Operation{AddField{
				AppLabel: "blog", ModelName: "post", Field: hidden,
			}}},
		},
		{
			name: "typed nil operations before relation",
			migration: Migration{App: "blog", Name: "0002_nil_then_relation", Operations: []Operation{
				(*CreateModel)(nil), (*AddField)(nil), AddField{AppLabel: "blog", ModelName: "post", Field: relation},
			}},
		},
		{
			name: "embedded built-in relation cannot bypass raw guard",
			migration: Migration{App: "blog", Name: "0002_embedded", Operations: []Operation{
				unsupportedStateOperation{CreateModel: CreateModel{AppLabel: "blog", Model: relationMigrationSchema().Models[0]}},
			}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, direction := range []Direction{DirectionForward, DirectionBackward} {
				direction := direction
				t.Run(string(direction), func(t *testing.T) {
					fake := &fakeBackend{transaction: newFakeTransaction()}
					before := EmptyProjectState()
					var after ProjectState
					var err error
					if direction == DirectionForward {
						after, err = (DirectExecutor{Backend: fake}).Apply(context.Background(), before, test.migration)
					} else {
						after, err = (DirectExecutor{Backend: fake}).Unapply(context.Background(), before, test.migration)
					}
					assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
					var capability *backend.CapabilityError
					if !errors.As(err, &capability) || capability.Feature != "relation_migration" {
						t.Fatalf("raw relation error = %#v capability=%#v", err, capability)
					}
					if fake.beginCount != 0 || !after.Equal(before) {
						t.Fatalf("raw relation touched I/O/state: begin=%d apps=%v", fake.beginCount, after.Apps())
					}
				})
			}
		})
	}
}

func TestExecutorRawScalarOperationsRejectPrivateRelationStateBeforeIO(t *testing.T) {
	t.Parallel()

	relationState := relationExecutorProjectState(t)
	scalarMigration := Migration{App: "blog", Name: "0003_summary", Operations: []Operation{AddField{
		AppLabel: "blog", ModelName: "post", Field: summaryField(),
	}}}
	for _, direction := range []Direction{DirectionForward, DirectionBackward} {
		fake := &fakeBackend{transaction: newFakeTransaction()}
		var err error
		if direction == DirectionForward {
			_, err = (DirectExecutor{Backend: fake}).Apply(context.Background(), relationState, scalarMigration)
		} else {
			_, err = (DirectExecutor{Backend: fake}).Unapply(context.Background(), relationState, scalarMigration)
		}
		assertMigrationError(t, err, CategoryCapability, CodeUnsupported, NoOperation, "")
		var capability *backend.CapabilityError
		if !errors.As(err, &capability) || capability.Feature != "relation_migration" || fake.beginCount != 0 {
			t.Fatalf("%s scalar/raw relation-state boundary = error:%v begin:%d", direction, err, fake.beginCount)
		}
	}
}

func TestExecutorRawRelationPostScanCancellationBeatsCapability(t *testing.T) {
	t.Parallel()

	migration := Migration{App: "blog", Name: "0002_author", Operations: []Operation{AddField{
		AppLabel: "blog", ModelName: "post", Field: relationMigrationField(),
	}}}
	ctx := &stagedRawCancellationContext{Context: context.Background(), cancelAt: 2}
	fake := &fakeBackend{transaction: newFakeTransaction()}
	_, err := (DirectExecutor{Backend: fake}).Apply(ctx, EmptyProjectState(), migration)
	var capability *backend.CapabilityError
	if !errors.Is(err, context.Canceled) || errors.As(err, &capability) || fake.beginCount != 0 || ctx.calls.Load() < 2 {
		t.Fatalf("raw relation post-scan cancellation = error:%v capability:%#v begin:%d calls:%d", err, capability, fake.beginCount, ctx.calls.Load())
	}
}

func TestEmbeddedRelationWrapperTraversalIsCycleSafeAndBounded(t *testing.T) {
	t.Parallel()

	cyclic := &interfaceEmbeddedOperation{}
	cyclic.Operation = cyclic
	assertRawWrapperRelationCapability(t, cyclic)

	var deep Operation = CreateModel{AppLabel: "blog", Model: relationMigrationSchema().Models[0]}
	for index := 0; index < 80; index++ {
		deep = interfaceEmbeddedOperation{Operation: deep}
	}
	assertRawWrapperRelationCapability(t, deep)

	scalarModel := relationMigrationSchema().Models[0].Clone()
	scalarModel.Fields = scalarModel.Fields[:1]
	var deepScalar Operation = CreateModel{AppLabel: "blog", Model: scalarModel}
	for index := 0; index < 80; index++ {
		deepScalar = interfaceEmbeddedOperation{Operation: deepScalar}
	}
	assertRawWrapperScalarPath(t, deepScalar)
	assertRawWrapperScalarPath(t, wideScalarEmbeddedOperation{CreateModel: CreateModel{AppLabel: "blog", Model: scalarModel}})

	var typedNil *interfaceEmbeddedOperation
	_, err := (DirectExecutor{}).Apply(context.Background(), EmptyProjectState(), Migration{
		App: "blog", Name: "0001_nil", Operations: []Operation{typedNil},
	})
	assertMigrationError(t, err, CategoryState, CodeInvalidState, 0, "")
}

func assertRawWrapperScalarPath(t *testing.T, operation Operation) {
	t.Helper()
	_, err := (DirectExecutor{}).Apply(context.Background(), EmptyProjectState(), Migration{
		App: "blog", Name: "0001_wrapped_scalar", Operations: []Operation{operation},
	})
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
	var capability *backend.CapabilityError
	if errors.As(err, &capability) {
		t.Fatalf("scalar wrapper selected relation capability = %#v", capability)
	}
}

func TestEmbeddedRelationScannerFollowsEffectiveMinimumDepthProvider(t *testing.T) {
	t.Parallel()

	scalarModel := relationMigrationSchema().Models[0].Clone()
	scalarModel.Fields = scalarModel.Fields[:1]
	shadowed := shadowedEmbeddedOperation{
		CreateModel: CreateModel{AppLabel: "blog", Model: scalarModel},
		nestedEmbeddedRelation: nestedEmbeddedRelation{AddField: AddField{
			AppLabel: "blog", ModelName: "post", Field: relationMigrationField(),
		}},
	}
	_, err := (DirectExecutor{}).Apply(context.Background(), EmptyProjectState(), Migration{
		App: "blog", Name: "0001_shadowed", Operations: []Operation{shadowed},
	})
	var capability *backend.CapabilityError
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
	if errors.As(err, &capability) {
		t.Fatalf("shadowed relation selected capability = %#v", capability)
	}

	ambiguous := struct {
		CreateModel
		AddField
	}{
		CreateModel: CreateModel{AppLabel: "blog", Model: scalarModel},
		AddField:    AddField{AppLabel: "blog", ModelName: "post", Field: relationMigrationField()},
	}
	if !embeddedBuiltinValueContainsRelation(reflect.ValueOf(ambiguous)) {
		t.Fatal("ambiguous same-depth providers did not fail closed")
	}
}

func assertRawWrapperRelationCapability(t *testing.T, operation Operation) {
	t.Helper()
	fake := &fakeBackend{transaction: newFakeTransaction()}
	_, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), Migration{
		App: "blog", Name: "0001_wrapped", Operations: []Operation{operation},
	})
	var capability *backend.CapabilityError
	if !errors.As(err, &capability) || capability.Feature != "relation_migration" || fake.beginCount != 0 {
		t.Fatalf("wrapped relation boundary = error:%v capability:%#v begin:%d", err, capability, fake.beginCount)
	}
}

func relationExecutorProjectState(t *testing.T) ProjectState {
	t.Helper()
	author := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion, AppLabel: "authors",
		Models: []ir.Model{{
			Name: "author", GoName: "Author", DBTable: "authors_author",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	post := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion, AppLabel: "blog",
		Models: []ir.Model{{
			Name: "post", GoName: "Post", DBTable: "blog_post",
			Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}},
		}},
	}
	state, err := NewProjectState(author, post)
	if err != nil {
		t.Fatalf("NewProjectState(): %v", err)
	}
	state, err = (AddField{AppLabel: "blog", ModelName: "post", Field: relationMigrationField()}).stateForward(state)
	if err != nil {
		t.Fatalf("relation stateForward(): %v", err)
	}
	return state
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
				after, err = (DirectExecutor{Backend: fake}).Apply(ctx, before, migration)
			} else {
				after, err = (DirectExecutor{Backend: fake}).Unapply(ctx, before, migration)
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
	after, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), before, migration)
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
	after, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Unapply(context.Background(), applied, migration)
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
	after, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), before, articleMigration())
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
	_, err = (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Unapply(context.Background(), applied, migration)
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
	after, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), before, articleMigration())
	assertMigrationError(t, err, CategoryRecorder, CodeRecordFailed, NoOperation, "")
	if !errors.Is(err, recorderCause) {
		t.Fatalf("Apply() error = %v, want recorder cause", err)
	}
	assertCalls(t, transaction.calls, "create_model", "add_field", "record_applied:news.0001_article", "rollback")
	if !after.Equal(before) {
		t.Fatal("recorder failure changed returned state")
	}
}

func TestExecutorRecorderCapabilityFailurePreservesRecorderTaxonomy(t *testing.T) {
	t.Parallel()

	recorderCause := backend.NewCapabilityError("migration_recorder", "forced recorder capability failure", nil)
	transaction := newFakeTransaction()
	transaction.failures["record_applied"] = recorderCause
	before := EmptyProjectState()
	after, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Apply(
		context.Background(), before, articleMigration(),
	)
	assertMigrationError(t, err, CategoryRecorder, CodeRecordFailed, NoOperation, "")
	if !errors.Is(err, recorderCause) {
		t.Fatalf("Apply() error = %v, want recorder capability cause", err)
	}
	assertCalls(t, transaction.calls, "create_model", "add_field", "record_applied:news.0001_article", "rollback")
	if !after.Equal(before) {
		t.Fatal("recorder capability failure changed returned state")
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
	after, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Unapply(context.Background(), before, migration)
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
	after, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), before, articleMigration())
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
	_, err := (DirectExecutor{Backend: &fakeBackend{transaction: transaction}}).Apply(context.Background(), EmptyProjectState(), articleMigration())
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
	_, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), articleMigration())
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")
	if !errors.Is(err, beginCause) {
		t.Fatalf("Apply() error = %v, want begin cause", err)
	}
	if len(fake.transaction.calls) != 0 {
		t.Fatalf("transaction calls = %v, want empty", fake.transaction.calls)
	}
}

func TestExecutorBeginClassifiesBackendCapabilityAndRevisionFenceFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cause    error
		category ErrorCategory
		code     ErrorCode
	}{
		{
			name:     "capability",
			cause:    backend.NewCapabilityError("migration_writer", "revision metadata is active", nil),
			category: CategoryCapability,
			code:     CodeUnsupported,
		},
		{
			name: "revision stale",
			cause: &backend.RevisionFenceError{
				Kind:  backend.RevisionFenceFailureStale,
				Cause: errors.New("stale sentinel"),
			},
			category: CategoryConflict,
			code:     CodeStaleHistoryRevision,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeBackend{beginError: test.cause, transaction: newFakeTransaction()}
			state, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), articleMigration())
			assertMigrationError(t, err, test.category, test.code, NoOperation, "")
			if !errors.Is(err, test.cause) {
				t.Fatalf("Apply() error = %v, want backend cause", err)
			}
			if fake.beginCount != 1 || len(fake.transaction.calls) != 0 || len(state.Apps()) != 0 {
				t.Fatalf("Apply() = state:%v begin:%d calls:%v", state.Apps(), fake.beginCount, fake.transaction.calls)
			}
		})
	}
}

func TestExecutorRejectsTypedNilBackendAndTransaction(t *testing.T) {
	t.Parallel()

	var nilBackend *fakeBackend
	_, err := (DirectExecutor{Backend: nilBackend}).Apply(context.Background(), EmptyProjectState(), articleMigration())
	assertMigrationError(t, err, CategoryTransaction, CodeBeginFailed, NoOperation, "")

	fake := &fakeBackend{}
	_, err = (DirectExecutor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), articleMigration())
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
	_, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), migration)
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
	_, err := (DirectExecutor{Backend: fake}).Apply(context.Background(), EmptyProjectState(), migration)
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
