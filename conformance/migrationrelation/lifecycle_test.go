package migrationrelation

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	_ "modernc.org/sqlite"
)

func TestLifecycleIntegrationCandidateUsesOneExistingFenceForMixedWork(t *testing.T) {
	t.Run("additive session port leaves scalar-only implementation compatible", func(t *testing.T) {
		session := &lifecycleScalarOnlySession{}
		intent := lifecycleNullableEditorIntent()
		_, err := lifecycleBeginRelationFenced(
			context.Background(),
			session,
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			intent,
		)
		if !errors.Is(err, lifecycleRelationErrCapability) {
			t.Fatalf("lifecycleBeginRelationFenced() error = %v, want capability", err)
		}
		if session.beginCalls != 0 {
			t.Fatalf("legacy BeginFencedMigration() calls = %d, want 0", session.beginCalls)
		}
	})

	t.Run("partial begin failure and post-begin cancellation roll back with a live bounded context", func(t *testing.T) {
		intent := lifecycleNullableEditorIntent()
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		tests := []struct {
			name      string
			beginErr  error
			cancelCtx bool
		}{
			{name: "partial begin error", beginErr: errors.New("partial begin sentinel")},
			{name: "post begin cancellation", cancelCtx: true},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				session := &lifecycleTraceSession{beginErr: test.beginErr}
				if test.cancelCtx {
					session.cancelBegin = cancel
				}
				transaction, err := lifecycleBeginRelationFenced(ctx, session, transition, intent)
				wantCause := test.beginErr
				if test.cancelCtx {
					wantCause = context.Canceled
				}
				if transaction != nil || !errors.Is(err, wantCause) {
					t.Fatalf("begin boundary = transaction:%#v error:%v, want nil/%v", transaction, err, wantCause)
				}
				partial := session.lastTransaction
				if partial == nil {
					t.Fatal("partial begin did not publish its cleanup handle")
				}
				if partial.rollbackCalls != 1 || partial.rollbackCtx != nil || !partial.rollbackLimit || session.closeCalls != 0 {
					t.Fatalf(
						"partial begin cleanup = transaction:%#v rollback:%d ctx:%v bounded:%t close:%d",
						partial, partial.rollbackCalls, partial.rollbackCtx, partial.rollbackLimit, session.closeCalls,
					)
				}
			})
		}
	})

	t.Run("mixed scalar and relation calls share transaction recorder and commit", func(t *testing.T) {
		intent := lifecycleNullableEditorIntent()
		transition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		operations := []lifecycleMixedOperation{
			{
				Kind:  lifecycleMixedScalarAdd,
				Model: lifecycleMixedScalarModel(),
				Field: ir.Field{
					Name: "published", GoName: "Published", Column: "published",
					Kind: ir.FieldBoolean,
				},
			},
			{Kind: lifecycleMixedRelationChange, Relation: intent.Changes[0]},
		}
		wantScalarModel := operations[0].Model.Clone()
		wantScalarField := operations[0].Field.Clone()
		stagedBeforeCommit := false
		session := &lifecycleTraceSession{}
		session.beginHook = func() {
			// Begin runs only after the complete mixed sequence was cloned. Mutating
			// every caller-owned arm here must not affect the prepared transaction.
			operations[0].Model.Fields[0].Name = "mutated_model"
			operations[0].Field.Name = "mutated_field"
			operations[1].Relation.Relation.Name = "mutated_relation"
		}
		session.preCommitHook = func(transaction *lifecycleTraceTransaction) {
			stagedBeforeCommit = transaction.recordStaged && len(transaction.stagedRecords) == 1 &&
				len(session.records) == 0
		}

		result, err := lifecycleExecuteMixedStep(context.Background(), session, transition, intent, operations)
		if err != nil {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v", err)
		}
		if !result.Committed || result.Outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("mixed result = %+v, want committed", result)
		}
		wantEvents := []string{"begin_relation", "scalar_add", "relation_change", "record", "commit_fenced"}
		if got := session.snapshotEvents(); !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("mixed events = %#v, want %#v", got, wantEvents)
		}
		transaction := session.lastTransaction
		if session.relationBeginCalls != 1 || session.legacyBeginCalls != 0 ||
			transaction.recorderCalls != 1 || transaction.commitCalls != 1 || transaction.rollbackCalls != 0 {
			t.Fatalf(
				"calls relation_begin=%d legacy_begin=%d record=%d commit=%d rollback=%d, want 1/0/1/1/0",
				session.relationBeginCalls, session.legacyBeginCalls,
				transaction.recorderCalls, transaction.commitCalls, transaction.rollbackCalls,
			)
		}
		if got, want := session.records, []migrationbackend.AppliedMigration{transition.Migration}; !reflect.DeepEqual(got, want) {
			t.Fatalf("generic fenced records = %#v, want %#v", got, want)
		}
		if !stagedBeforeCommit || !transaction.recordStaged || !transaction.stagePublished || transaction.stageDiscarded {
			t.Fatalf(
				"history staging = before_commit:%t staged:%t published:%t discarded:%t",
				stagedBeforeCommit, transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded,
			)
		}
		if !reflect.DeepEqual(transaction.lastScalarModel, wantScalarModel) ||
			!reflect.DeepEqual(transaction.lastScalarField, wantScalarField) {
			t.Fatalf("prepared scalar operation retained caller aliases: model=%+v field=%+v", transaction.lastScalarModel, transaction.lastScalarField)
		}
		if session.closeCalls != 0 {
			t.Fatalf("step helper closed outer-owned session %d times", session.closeCalls)
		}
		if err := session.Close(context.Background()); err != nil || session.closeCalls != 1 {
			t.Fatalf("outer session Close() = calls:%d error:%v, want 1/nil", session.closeCalls, err)
		}
	})

	t.Run("rolled-back and both unknown commit observations are terminal and never retried", func(t *testing.T) {
		tests := []struct {
			name           string
			durability     migrationbackend.CommitDurability
			unknownDurable bool
			wantRecords    int
		}{
			{name: "definite rollback", durability: migrationbackend.CommitRolledBack},
			{name: "unknown nondurable", durability: migrationbackend.CommitUnknown},
			{name: "unknown durable", durability: migrationbackend.CommitUnknown, unknownDurable: true, wantRecords: 1},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				cause := errors.New(test.name + " sentinel")
				intent := lifecycleNullableEditorIntent()
				session := &lifecycleTraceSession{
					outcome:        migrationbackend.CommitOutcome{Durability: test.durability},
					commitErr:      cause,
					unknownDurable: test.unknownDurable,
				}
				result, err := lifecycleExecuteMixedStep(
					context.Background(),
					session,
					migrationbackend.HistoryTransition{
						Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
						Kind:      migrationbackend.HistoryTransitionApply,
					},
					intent,
					[]lifecycleMixedOperation{{Kind: lifecycleMixedRelationChange, Relation: intent.Changes[0]}},
				)
				if !errors.Is(err, cause) || result.Committed || result.Outcome.Durability != test.durability {
					t.Fatalf("lifecycleExecuteMixedStep() = (%+v, %v), want terminal %d", result, err, test.durability)
				}
				transaction := session.lastTransaction
				if session.relationBeginCalls != 1 || transaction.commitCalls != 1 || transaction.rollbackCalls != 0 {
					t.Fatalf(
						"terminal outcome calls begin=%d commit=%d rollback=%d, want 1/1/0",
						session.relationBeginCalls, transaction.commitCalls, transaction.rollbackCalls,
					)
				}
				if len(session.records) != test.wantRecords {
					t.Fatalf("terminal outcome durable records = %#v, want %d", session.records, test.wantRecords)
				}
				if !transaction.recordStaged || transaction.stagePublished != (test.wantRecords == 1) ||
					transaction.stageDiscarded != (test.wantRecords == 0) {
					t.Fatalf(
						"terminal staging = staged:%t published:%t discarded:%t",
						transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded,
					)
				}
				if session.closeCalls != 0 {
					t.Fatalf("terminal outcome closed outer-owned session %d times", session.closeCalls)
				}
			})
		}
	})

	t.Run("committed cleanup error remains durable and is not retried", func(t *testing.T) {
		cause := errors.New("committed cleanup sentinel")
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{
			outcome:   migrationbackend.CommitOutcome{Durability: migrationbackend.CommitCommitted},
			commitErr: cause,
		}
		result, err := lifecycleExecuteMixedStep(
			context.Background(),
			session,
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			intent,
			[]lifecycleMixedOperation{{Kind: lifecycleMixedRelationChange, Relation: intent.Changes[0]}},
		)
		if !errors.Is(err, cause) || !result.Committed || result.Outcome.Durability != migrationbackend.CommitCommitted {
			t.Fatalf("lifecycleExecuteMixedStep() = (%+v, %v), want durable cleanup error", result, err)
		}
		transaction := session.lastTransaction
		if session.relationBeginCalls != 1 || transaction.commitCalls != 1 || transaction.rollbackCalls != 0 {
			t.Fatalf(
				"committed cleanup calls begin=%d commit=%d rollback=%d, want 1/1/0",
				session.relationBeginCalls, transaction.commitCalls, transaction.rollbackCalls,
			)
		}
		if got, want := session.records, []migrationbackend.AppliedMigration{{App: intent.App, Name: intent.Name}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("durable cleanup records = %#v, want %#v", got, want)
		}
		if !transaction.recordStaged || !transaction.stagePublished || transaction.stageDiscarded || session.closeCalls != 0 {
			t.Fatalf(
				"committed cleanup staging/ownership = staged:%t published:%t discarded:%t close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded, session.closeCalls,
			)
		}
	})

	t.Run("operation failure rolls back with detached bounded context", func(t *testing.T) {
		cause := errors.New("relation operation sentinel")
		ctx, cancel := context.WithCancel(context.Background())
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{relationErr: cause, cancelRelation: cancel}
		_, err := lifecycleExecuteMixedStep(
			ctx,
			session,
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			intent,
			[]lifecycleMixedOperation{{Kind: lifecycleMixedRelationChange, Relation: intent.Changes[0]}},
		)
		if !errors.Is(err, cause) {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v, want operation cause", err)
		}
		transaction := session.lastTransaction
		if transaction.rollbackCalls != 1 || transaction.rollbackCtx != nil || !transaction.rollbackLimit {
			t.Fatalf(
				"rollback calls=%d context_err=%v bounded=%t, want 1/nil/true",
				transaction.rollbackCalls, transaction.rollbackCtx, transaction.rollbackLimit,
			)
		}
		if transaction.recorderCalls != 0 || transaction.commitCalls != 0 {
			t.Fatalf("failed operation calls record=%d commit=%d, want 0/0", transaction.recorderCalls, transaction.commitCalls)
		}
		if transaction.recordStaged || transaction.stagePublished || transaction.stageDiscarded || session.closeCalls != 0 {
			t.Fatalf(
				"failed operation staging/ownership = staged:%t published:%t discarded:%t close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded, session.closeCalls,
			)
		}
	})

	t.Run("successful final operation cannot hide request cancellation before recorder", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{cancelRelation: cancel}
		_, err := lifecycleExecuteMixedStep(
			ctx,
			session,
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			intent,
			[]lifecycleMixedOperation{{Kind: lifecycleMixedRelationChange, Relation: intent.Changes[0]}},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v, want cancellation", err)
		}
		transaction := session.lastTransaction
		if transaction.rollbackCalls != 1 || transaction.rollbackCtx != nil || !transaction.rollbackLimit ||
			transaction.recorderCalls != 0 || transaction.commitCalls != 0 {
			t.Fatalf(
				"post-operation cancellation calls rollback=%d record=%d commit=%d context_err=%v bounded=%t",
				transaction.rollbackCalls, transaction.recorderCalls, transaction.commitCalls,
				transaction.rollbackCtx, transaction.rollbackLimit,
			)
		}
		if transaction.recordStaged || transaction.stagePublished || transaction.stageDiscarded || session.closeCalls != 0 {
			t.Fatalf(
				"post-operation cancellation staging/ownership = staged:%t published:%t discarded:%t close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded, session.closeCalls,
			)
		}
	})

	t.Run("successful recorder cannot hide request cancellation before commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		intent := lifecycleNullableEditorIntent()
		session := &lifecycleTraceSession{cancelRecorder: cancel}
		_, err := lifecycleExecuteMixedStep(
			ctx,
			session,
			migrationbackend.HistoryTransition{
				Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
				Kind:      migrationbackend.HistoryTransitionApply,
			},
			intent,
			[]lifecycleMixedOperation{{Kind: lifecycleMixedRelationChange, Relation: intent.Changes[0]}},
		)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lifecycleExecuteMixedStep() error = %v, want cancellation", err)
		}
		transaction := session.lastTransaction
		if transaction.rollbackCalls != 1 || transaction.rollbackCtx != nil || !transaction.rollbackLimit ||
			transaction.recorderCalls != 1 || transaction.commitCalls != 0 {
			t.Fatalf(
				"post-recorder cancellation calls rollback=%d record=%d commit=%d context_err=%v bounded=%t",
				transaction.rollbackCalls, transaction.recorderCalls, transaction.commitCalls,
				transaction.rollbackCtx, transaction.rollbackLimit,
			)
		}
		if !transaction.recordStaged || transaction.stagePublished || !transaction.stageDiscarded ||
			len(session.records) != 0 || session.closeCalls != 0 {
			t.Fatalf(
				"post-recorder cancellation staging/ownership = staged:%t published:%t discarded:%t records:%#v close:%d",
				transaction.recordStaged, transaction.stagePublished, transaction.stageDiscarded,
				session.records, session.closeCalls,
			)
		}
	})

	t.Run("invalid missing extra and reordered relation operations fail before begin", func(t *testing.T) {
		baseIntent := lifecycleNullableEditorIntent()
		baseTransition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: baseIntent.App, Name: baseIntent.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		createIntent := relationBackendArticleCreateIntent()
		createTransition := migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: createIntent.App, Name: createIntent.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		}
		halfDocument := migrationdefinition.MaxDocumentBytes/2 + 1
		largeScalarField := func(name string) ir.Field {
			return ir.Field{
				Name: name, GoName: "LargeScalar", Column: name, Kind: ir.FieldChar,
				MaxLength: halfDocument,
				Default:   &ir.ScalarDefault{Kind: ir.ScalarString, String: strings.Repeat("x", halfDocument)},
			}
		}
		tests := []struct {
			name       string
			intent     relationBackendStepIntent
			transition migrationbackend.HistoryTransition
			operations []lifecycleMixedOperation
		}{
			{
				name: "invalid kind", intent: baseIntent, transition: baseTransition,
				operations: []lifecycleMixedOperation{{Kind: lifecycleMixedOperationKind(99)}},
			},
			{
				name: "missing relation", intent: baseIntent, transition: baseTransition,
				operations: nil,
			},
			{
				name: "extra relation", intent: baseIntent, transition: baseTransition,
				operations: []lifecycleMixedOperation{
					{Kind: lifecycleMixedRelationChange, Relation: baseIntent.Changes[0]},
					{Kind: lifecycleMixedRelationChange, Relation: baseIntent.Changes[0]},
				},
			},
			{
				name: "reordered relation", intent: createIntent, transition: createTransition,
				operations: []lifecycleMixedOperation{
					{Kind: lifecycleMixedRelationChange, Relation: createIntent.Changes[1]},
					{Kind: lifecycleMixedRelationChange, Relation: createIntent.Changes[0]},
				},
			},
			{
				name: "oversized scalar string", intent: baseIntent, transition: baseTransition,
				operations: []lifecycleMixedOperation{
					{
						Kind:  lifecycleMixedScalarAdd,
						Model: lifecycleMixedScalarModel(),
						Field: ir.Field{
							Name: "summary", GoName: "Summary", Column: "summary", Kind: ir.FieldChar,
							MaxLength: migrationdefinition.MaxDocumentBytes + 1,
							Default: &ir.ScalarDefault{
								Kind:   ir.ScalarString,
								String: strings.Repeat("x", migrationdefinition.MaxDocumentBytes+1),
							},
						},
					},
					{Kind: lifecycleMixedRelationChange, Relation: baseIntent.Changes[0]},
				},
			},
			{
				name: "aggregate scalar document bytes", intent: baseIntent, transition: baseTransition,
				operations: []lifecycleMixedOperation{
					{Kind: lifecycleMixedScalarAdd, Model: lifecycleMixedScalarModel(), Field: largeScalarField("summary_a")},
					{Kind: lifecycleMixedScalarAdd, Model: lifecycleMixedScalarModel(), Field: largeScalarField("summary_b")},
					{Kind: lifecycleMixedRelationChange, Relation: baseIntent.Changes[0]},
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				session := &lifecycleTraceSession{}
				result, err := lifecycleExecuteMixedStep(
					context.Background(), session, test.transition, test.intent, test.operations,
				)
				if !errors.Is(err, lifecycleRelationErrIntent) || result != (lifecycleMixedResult{}) {
					t.Fatalf("invalid mixed sequence = result:%+v error:%v", result, err)
				}
				if session.relationBeginCalls != 0 || session.legacyBeginCalls != 0 || session.closeCalls != 0 ||
					session.lastTransaction != nil || len(session.snapshotEvents()) != 0 {
					t.Fatalf(
						"invalid mixed sequence touched lifecycle: relation=%d legacy=%d close=%d transaction=%#v events=%#v",
						session.relationBeginCalls, session.legacyBeginCalls, session.closeCalls,
						session.lastTransaction, session.snapshotEvents(),
					)
				}
			})
		}
	})
}

func TestLifecycleIntegrationCandidateRealSQLiteHistoryContinuityAndBlocker(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lifecycle-integration.sqlite3")
	dsn := "file:" + path + "?mode=rwc"
	definitions := lifecycleHistoryContinuityDefinitions()
	initialOnly := definitions[:1]

	backend := lifecycleOpenSQLiteBackend(t, ctx, dsn)
	executor := migrations.Executor{Backend: backend}
	state, err := executor.Migrate(ctx, initialOnly, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(0001) error = %v", err)
	}
	if _, exists := state.Model("blog", "author"); !exists {
		t.Fatal("Migrate(0001) state lacks blog.author")
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("Migrate(0001) state lacks blog.article")
	}
	if metadata := lifecycleReadRevisionMetadata(t, dsn); metadata.Revision != 1 {
		t.Fatalf("revision after 0001 = %d, want 1", metadata.Revision)
	}

	// The real session implements the existing fenced contract, but its
	// unexported concrete transaction exposes neither the pinned connection nor
	// the optional relation port. Capability selection must stop before calling
	// legacy BeginFencedMigration; a wrapper cannot safely bolt DDL onto it.
	realSession, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(blocker): %v", err)
	}
	records, err := realSession.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("ReadAppliedMigrations(blocker): %v", err)
	}
	if want := []migrationbackend.AppliedMigration{{App: "blog", Name: "0001_initial"}}; !reflect.DeepEqual(records, want) {
		t.Fatalf("history before relation blocker = %#v, want %#v", records, want)
	}
	intent := lifecycleNullableEditorIntent()
	_, err = lifecycleBeginRelationFenced(
		ctx,
		realSession,
		migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: intent.App, Name: intent.Name},
			Kind:      migrationbackend.HistoryTransitionApply,
		},
		intent,
	)
	if !errors.Is(err, lifecycleRelationErrCapability) {
		t.Fatalf("real SQLite relation begin error = %v, want explicit capability blocker", err)
	}
	if err := realSession.Close(ctx); err != nil {
		t.Fatalf("Close(blocker session): %v", err)
	}
	if metadata := lifecycleReadRevisionMetadata(t, dsn); metadata.Revision != 1 {
		t.Fatalf("capability rejection advanced revision to %d", metadata.Revision)
	}

	// Current StateReconstructor rejects the actual relation-bearing operation.
	// The empty 0002 definition below is therefore intentionally only a history
	// continuity stand-in; it is not evidence that relation state or DDL works.
	relationDefinition := lifecycleUnsupportedRelationDefinition()
	if _, err := migrations.NewStateReconstructor(initialOnly[0], relationDefinition); err == nil {
		t.Fatal("StateReconstructor unexpectedly accepted relation-bearing 0002")
	} else {
		var migrationErr *migrations.Error
		if !errors.As(err, &migrationErr) || migrationErr.Category != migrations.CategoryState ||
			migrationErr.Code != migrations.CodeInvalidState || migrationErr.Direction != migrations.DirectionForward ||
			migrationErr.App != "blog" || migrationErr.Migration != "0002_relation" ||
			migrationErr.OperationIndex != 0 || migrationErr.Operation != "AddField" || migrationErr.Cause == nil ||
			!strings.Contains(migrationErr.Cause.Error(), "Schema IR v2 migration state cannot represent relation-bearing field") {
			t.Fatalf("StateReconstructor blocker error = %#v (%v), want exact relation-bearing state error", migrationErr, err)
		}
	}

	state, err = executor.Migrate(ctx, definitions, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(history-only 0002) error = %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("history-only 0002 lost reconstructed 0001 state")
	}
	metadataAtTwo := lifecycleReadRevisionMetadata(t, dsn)
	if metadataAtTwo.Revision != 2 || len(metadataAtTwo.Epoch) != 16 || len(metadataAtTwo.Fingerprint) != 32 {
		t.Fatalf("metadata after 0002 = %+v, want revision 2 and 16/32-byte fence", metadataAtTwo)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close(first backend): %v", err)
	}

	reopened := lifecycleOpenSQLiteBackend(t, ctx, dsn)
	reopenedExecutor := migrations.Executor{Backend: reopened}
	staleSession, err := reopened.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(reopened): %v", err)
	}
	reopenedRecords, err := staleSession.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("ReadAppliedMigrations(reopened): %v", err)
	}
	wantBoth := []migrationbackend.AppliedMigration{
		{App: "blog", Name: "0001_initial"},
		{App: "blog", Name: "0002_relation"},
	}
	if !reflect.DeepEqual(reopenedRecords, wantBoth) {
		t.Fatalf("reopened history = %#v, want %#v", reopenedRecords, wantBoth)
	}

	reconstructor, err := migrations.NewStateReconstructor(definitions...)
	if err != nil {
		t.Fatalf("NewStateReconstructor(history continuity): %v", err)
	}
	applied, err := migrations.NewAppliedState(
		migrations.MigrationKey{App: "blog", Name: "0001_initial"},
		migrations.MigrationKey{App: "blog", Name: "0002_relation"},
	)
	if err != nil {
		t.Fatalf("NewAppliedState(): %v", err)
	}
	reconstructed, err := reconstructor.Reconstruct(migrations.AppliedStateRequest(applied))
	if err != nil {
		t.Fatalf("Reconstruct(reopened history): %v", err)
	}
	if !reconstructed.Equal(state) {
		t.Fatalf("reopened reconstructed state differs from pre-close state")
	}

	state, err = reopenedExecutor.Migrate(
		ctx,
		definitions,
		migrations.TargetedLifecycleRequest(
			migrations.NamedTarget(migrations.MigrationKey{App: "blog", Name: "0001_initial"}),
		),
	)
	if err != nil {
		t.Fatalf("Migrate(unapply 0002) error = %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("unapply 0002 did not preserve 0001 state")
	}
	metadataAtThree := lifecycleReadRevisionMetadata(t, dsn)
	if metadataAtThree.Revision != 3 || !bytes.Equal(metadataAtThree.Epoch, metadataAtTwo.Epoch) ||
		bytes.Equal(metadataAtThree.Fingerprint, metadataAtTwo.Fingerprint) {
		t.Fatalf("metadata after unapply = %+v, want revision 3, same epoch, changed fingerprint", metadataAtThree)
	}

	// staleSession still owns the revision-2 snapshot. The real backend must
	// reject its claim after revision 3 became durable.
	_, staleErr := staleSession.BeginFencedMigration(
		ctx,
		migrationbackend.HistoryTransition{
			Migration: migrationbackend.AppliedMigration{App: "blog", Name: "0003_stale_probe"},
			Kind:      migrationbackend.HistoryTransitionApply,
		},
	)
	var fenceErr *migrationbackend.RevisionFenceError
	if !errors.As(staleErr, &fenceErr) || fenceErr == nil || fenceErr.Kind != migrationbackend.RevisionFenceFailureStale {
		t.Fatalf("stale BeginFencedMigration() error = %#v, want stale fence", staleErr)
	}
	if err := staleSession.Close(ctx); err != nil {
		t.Fatalf("Close(stale session): %v", err)
	}

	remaining := lifecycleReadApplied(t, ctx, reopened)
	wantInitial := []migrationbackend.AppliedMigration{{App: "blog", Name: "0001_initial"}}
	if !reflect.DeepEqual(remaining, wantInitial) {
		t.Fatalf("history after unapply = %#v, want %#v", remaining, wantInitial)
	}

	state, err = reopenedExecutor.Migrate(ctx, definitions, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatalf("Migrate(reapply history-only 0002) error = %v", err)
	}
	if _, exists := state.Model("blog", "article"); !exists {
		t.Fatal("reapply 0002 lost 0001 state")
	}
	metadataAtFour := lifecycleReadRevisionMetadata(t, dsn)
	if metadataAtFour.Revision != 4 || !bytes.Equal(metadataAtFour.Epoch, metadataAtTwo.Epoch) ||
		!bytes.Equal(metadataAtFour.Fingerprint, metadataAtTwo.Fingerprint) {
		t.Fatalf("metadata after reapply = %+v, want revision 4 and restored history fingerprint", metadataAtFour)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close(second backend): %v", err)
	}

	finalBackend := lifecycleOpenSQLiteBackend(t, ctx, dsn)
	if got := lifecycleReadApplied(t, ctx, finalBackend); !reflect.DeepEqual(got, wantBoth) {
		t.Fatalf("final reopened history = %#v, want %#v", got, wantBoth)
	}
	if lifecycleSQLiteColumnExists(t, dsn, "article", "editor_id") {
		t.Fatal("history-only 0002 unexpectedly created relation column")
	}
	if count := lifecycleSQLiteForeignKeyCount(t, dsn, "article"); count != 0 {
		t.Fatalf("history-only article foreign keys = %d, want 0 (physical relation integration remains blocked)", count)
	}
}

func lifecycleHistoryContinuityDefinitions() []migrations.Migration {
	initial := migrations.Migration{
		App: "blog", Name: "0001_initial",
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "blog", Model: ir.Model{
				Name: "author", GoName: "Author", DBTable: "author",
				Fields: []ir.Field{
					{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
					{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 200},
				},
			}},
			migrations.CreateModel{AppLabel: "blog", Model: lifecycleMixedScalarModel()},
		},
	}
	return []migrations.Migration{
		initial,
		{
			App: "blog", Name: "0002_relation",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_initial"}},
			// Deliberately empty: this proves only that the real recorder,
			// revision token, planner, and reconstructor can carry the identity.
			// The relation-bearing equivalent is rejected below.
		},
	}
}

func lifecycleUnsupportedRelationDefinition() migrations.Migration {
	return migrations.Migration{
		App: "blog", Name: "0002_relation",
		Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_initial"}},
		Operations: []migrations.Operation{migrations.AddField{
			AppLabel: "blog", ModelName: "article",
			Field: ir.Field{
				Name: "editor", GoName: "EditorID", Column: "editor_id", Kind: ir.FieldForeignKey,
				Nullable: true,
				Relation: &ir.ForeignKeyRelation{
					Target:      ir.ModelIdentity{AppLabel: "blog", ModelName: "author"},
					Cardinality: ir.RelationManyToOne,
					Reverse:     ir.ReverseRelation{Name: "edited_articles"},
					OnDelete:    ir.DeleteSetNull,
				},
			},
		}},
	}
}

type lifecycleRevisionMetadata struct {
	Epoch       []byte
	Revision    int64
	Fingerprint []byte
}

func lifecycleOpenSQLiteBackend(t *testing.T, ctx context.Context, dsn string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("SQLite backend cleanup: %v", err)
		}
	})
	return backend
}

func lifecycleReadApplied(
	t *testing.T,
	ctx context.Context,
	backend *sqlite.Backend,
) []migrationbackend.AppliedMigration {
	t.Helper()
	session, err := backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		t.Fatalf("OpenRevisionFencedSession(): %v", err)
	}
	records, readErr := session.ReadAppliedMigrations(ctx)
	closeErr := session.Close(ctx)
	if readErr != nil || closeErr != nil {
		t.Fatalf("revision-fenced history read/close = (%v, %v)", readErr, closeErr)
	}
	return records
}

func lifecycleReadRevisionMetadata(t *testing.T, dsn string) lifecycleRevisionMetadata {
	t.Helper()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open(metadata): %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("metadata database Close(): %v", err)
		}
	}()
	var metadata lifecycleRevisionMetadata
	if err := database.QueryRow(
		`SELECT "epoch", "revision", "history_fingerprint" FROM "godj_migration_revision" WHERE "singleton" = 1`,
	).Scan(&metadata.Epoch, &metadata.Revision, &metadata.Fingerprint); err != nil {
		t.Fatalf("read migration revision metadata: %v", err)
	}
	metadata.Epoch = append([]byte(nil), metadata.Epoch...)
	metadata.Fingerprint = append([]byte(nil), metadata.Fingerprint...)
	return metadata
}

func lifecycleSQLiteColumnExists(t *testing.T, dsn, table, column string) bool {
	t.Helper()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(fmt.Sprintf(`PRAGMA main.table_xinfo(%q)`, table))
	if err != nil {
		t.Fatalf("inspect columns for %q: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func lifecycleSQLiteForeignKeyCount(t *testing.T, dsn, table string) int {
	t.Helper()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(fmt.Sprintf(`PRAGMA main.foreign_key_list(%q)`, table))
	if err != nil {
		t.Fatalf("inspect foreign keys for %q: %v", table, err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}
