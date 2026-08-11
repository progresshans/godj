package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestAtomicRelationCommitsAndExpiresSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-commit-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	provisionRelationTransactionFixture(t, backend)

	var retained db.RelationSession
	err = backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		retained = session
		rows, err := session.RelationSetNull(ctx, relationSetNullTestPlan(2))
		if err != nil || rows != 2 {
			return fmt.Errorf("SET_NULL = (%d, %v), want (2, nil)", rows, err)
		}
		rows, err = session.Delete(ctx, query.NewDeletePlan(
			"authors_author",
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.Integer(2),
		))
		if err != nil || rows != 1 {
			return fmt.Errorf("Delete = (%d, %v), want (1, nil)", rows, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("AtomicRelation() error = %v", err)
	}
	assertRelationFixtureState(t, backend, 1, 0)
	if rows, err := retained.RelationSetNull(ctx, relationSetNullTestPlan(1)); rows != 0 ||
		!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("expired session SET_NULL = (%d, %v), want invalid_plan", rows, err)
	}
	assertRelationFixtureState(t, backend, 1, 0)
}

func TestAtomicRelationRollsBackCallbackErrorAndCanceledContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-rollback-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	provisionRelationTransactionFixture(t, backend)

	callbackErr := errors.New("callback failure")
	err = backend.AtomicRelation(ctx, func(session db.RelationSession) error {
		if rows, err := session.RelationSetNull(ctx, relationSetNullTestPlan(2)); err != nil || rows != 2 {
			return fmt.Errorf("SET_NULL = (%d, %v)", rows, err)
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
		t.Fatalf("callback failure error = %v", err)
	}
	assertRelationFixtureState(t, backend, 2, 2)

	canceled, cancel := context.WithCancel(ctx)
	err = backend.AtomicRelation(canceled, func(session db.RelationSession) error {
		if rows, err := session.RelationSetNull(canceled, relationSetNullTestPlan(2)); err != nil || rows != 2 {
			return fmt.Errorf("SET_NULL = (%d, %v)", rows, err)
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
		t.Fatalf("canceled transaction error = %v", err)
	}
	assertRelationFixtureState(t, backend, 2, 2)
}

func TestAtomicRelationPanicRollsBackAndRepanicsExactValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-panic-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	provisionRelationTransactionFixture(t, backend)

	panicValue := &struct{ marker string }{marker: "exact"}
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Fatalf("recovered panic = %#v, want exact %#v", got, panicValue)
			}
		}()
		_ = backend.AtomicRelation(ctx, func(session db.RelationSession) error {
			if _, err := session.RelationSetNull(ctx, relationSetNullTestPlan(2)); err != nil {
				t.Fatal(err)
			}
			panic(panicValue)
		})
	}()
	assertRelationFixtureState(t, backend, 2, 2)
}

func TestAtomicRelationPreconditionsAndForeignKeyVerification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-preconditions-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	if err := backend.AtomicRelation(ctx, nil); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("AtomicRelation(nil callback) error = %v", err)
	}
	called := 0
	if err := backend.AtomicRelation(nil, func(db.RelationSession) error { called++; return nil }); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("AtomicRelation(nil context) error = %v", err)
	}
	if err := backend.AtomicRelation(ctx, func(db.RelationSession) error { called++; return nil }); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("AtomicRelation(FK off) error = %v", err)
	}
	if called != 0 {
		t.Fatalf("precondition callbacks = %d, want 0", called)
	}

	zero := &Backend{database: backend.database}
	if err := zero.AtomicRelation(ctx, func(db.RelationSession) error { called++; return nil }); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("AtomicRelation(nil retention) error = %v", err)
	}
	if called != 0 {
		t.Fatalf("nil-retention callbacks = %d, want 0", called)
	}
	var nilBackend *Backend
	if err := nilBackend.AtomicRelation(ctx, func(db.RelationSession) error { called++; return nil }); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("typed-nil backend error = %v", err)
	}
	var nilSession *relationSession
	if rows, err := nilSession.RelationSetNull(ctx, relationSetNullTestPlan(1)); rows != 0 ||
		!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("typed-nil session SET_NULL = (%d, %v)", rows, err)
	}
}

func TestExecuteAtomicRelationBeginFailureDiscardAndRetention(t *testing.T) {
	t.Parallel()

	beginErr := errors.New("begin failure")
	t.Run("confirmed_discard", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return nil, beginErr
				}
				return relationFaultResult(0), nil
			},
		}
		called := 0
		err := executeAtomicRelation(context.Background(), func(db.RelationSession) error {
			called++
			return nil
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, beginErr) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("begin error = %v", err)
		}
		if called != 0 || connection.rawCalls.Load() != 1 || connection.closeCalls.Load() != 0 {
			t.Fatalf("calls callback/raw/close = %d/%d/%d", called, connection.rawCalls.Load(), connection.closeCalls.Load())
		}
	})

	t.Run("unconfirmed_discard_retained_until_close", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return nil, beginErr
				}
				return relationFaultResult(0), nil
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(db.RelationSession) error {
			t.Fatal("callback ran after BEGIN failure")
			return nil
		}, connection, retention, nil)
		if !errors.Is(err, beginErr) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("begin error = %v", err)
		}
		if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("operation close/retained = %d/%d, want 0/1", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		if err := retention.sealAndDrain(); err != nil {
			t.Fatalf("sealAndDrain() error = %v", err)
		}
		if connection.closeCalls.Load() != 1 || retainedConnectionCount(retention) != 0 {
			t.Fatalf("terminal close/retained = %d/%d, want 1/0", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
	})
}

func TestExecuteAtomicRelationOutcomeMarkers(t *testing.T) {
	t.Parallel()

	primaryCause := errors.New("primary mutation failure")
	primary := &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnexpectedRows,
		Detail:   "primary typed failure",
		Cause:    primaryCause,
	}
	rollbackErr := errors.New("rollback failure")

	t.Run("mutation_unconfirmed_termination", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				switch statement {
				case "BEGIN IMMEDIATE":
					return relationFaultResult(0), nil
				case "ROLLBACK":
					return nil, rollbackErr
				default:
					return nil, primary
				}
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.Delete(context.Background(), relationDeleteTestPlan(1))
			return err
		}, connection, retention, nil)
		var marker *query.Error
		if !errors.As(err, &marker) || marker.Code != query.CodeTransactionOutcomeUnknown ||
			!errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) {
			t.Fatalf("mutation failure error = %v", err)
		}
		if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("operation close/retained = %d/%d", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})

	t.Run("pre_mutation_unconfirmed_termination", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return relationFaultResult(0), nil
				}
				return nil, rollbackErr
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(db.RelationSession) error { return primary }, connection, retention, nil)
		if !errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("pre-mutation error = %v", err)
		}
		_ = retention.sealAndDrain()
	})

	t.Run("invalid_mutation_plan_stays_pre_mutation", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return relationFaultResult(0), nil
				}
				if statement == "ROLLBACK" {
					return nil, rollbackErr
				}
				return nil, fmt.Errorf("unexpected statement %q", statement)
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.RelationSetNull(context.Background(), query.RelationSetNullPlan{})
			return err
		}, connection, retention, nil)
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("invalid mutation plan error = %v", err)
		}
		if got := connection.statementSnapshot(); fmt.Sprint(got) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "ROLLBACK"}) {
			t.Fatalf("invalid mutation statements = %v, want only BEGIN/ROLLBACK", got)
		}
		if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("invalid mutation close/retained = %d/%d, want 0/1", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})

	t.Run("query_failure_stays_pre_mutation", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return relationFaultResult(0), nil
				}
				if statement == "ROLLBACK" {
					return nil, rollbackErr
				}
				return nil, fmt.Errorf("unexpected statement %q", statement)
			},
			query: func(context.Context, string, []any) (*sql.Rows, error) { return nil, primary },
			raw:   func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.Query(context.Background(), query.NewPlan(
				"authors_author",
				[]query.FieldRef{query.NewFieldRef("id", "id", query.FieldInteger, false)},
			))
			return err
		}, connection, retention, nil)
		if !errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("query failure error = %v", err)
		}
		if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("query failure close/retained = %d/%d, want 0/1", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})

	t.Run("confirmed_rollback", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				switch statement {
				case "BEGIN IMMEDIATE", "ROLLBACK":
					return relationFaultResult(0), nil
				default:
					return nil, primary
				}
			},
		}
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.Delete(context.Background(), relationDeleteTestPlan(1))
			return err
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, primaryCause) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("confirmed cleanup error = %v", err)
		}
		if connection.closeCalls.Load() != 1 || connection.rawCalls.Load() != 0 {
			t.Fatalf("close/raw calls = %d/%d, want 1/0", connection.closeCalls.Load(), connection.rawCalls.Load())
		}
	})

	t.Run("literal_commit_error", func(t *testing.T) {
		commitErr := errors.New("literal commit failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "COMMIT" {
					return nil, commitErr
				}
				return relationFaultResult(0), nil
			},
		}
		err := executeAtomicRelation(context.Background(), func(db.RelationSession) error { return nil }, connection, newRelationRetentionState(), nil)
		var marker *query.Error
		if !errors.As(err, &marker) || marker.Code != query.CodeCommitOutcomeUnknown || !errors.Is(err, commitErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("commit error = %v", err)
		}
		if connection.closeCalls.Load() != 1 {
			t.Fatalf("rollback close calls = %d, want 1", connection.closeCalls.Load())
		}
	})

	t.Run("literal_commit_error_preserves_unconfirmed_cleanup", func(t *testing.T) {
		commitErr := errors.New("literal commit failure with cleanup fault")
		discardErr := errors.New("discard confirmation failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				switch statement {
				case "COMMIT":
					return nil, commitErr
				case "ROLLBACK":
					return nil, rollbackErr
				default:
					return relationFaultResult(0), nil
				}
			},
			raw: func(func(any) error) error { return discardErr },
		}
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(db.RelationSession) error { return nil }, connection, retention, nil)
		var marker *query.Error
		if !errors.As(err, &marker) || marker.Code != query.CodeCommitOutcomeUnknown ||
			!errors.Is(err, commitErr) || !errors.Is(err, rollbackErr) || !errors.Is(err, discardErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("commit cleanup fault error = %v", err)
		}
		if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("commit cleanup close/retained = %d/%d, want 0/1", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})

	t.Run("rollback_close_error_is_terminated", func(t *testing.T) {
		closeErr := errors.New("rollback connection return failure")
		connection := &relationFaultConnection{closeErr: closeErr}
		err := executeAtomicRelation(context.Background(), func(db.RelationSession) error { return primary }, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, primaryCause) || !errors.Is(err, closeErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
			errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
			t.Fatalf("rollback close error = %v", err)
		}
		if connection.closeCalls.Load() != 1 || connection.rawCalls.Load() != 0 {
			t.Fatalf("rollback close/raw calls = %d/%d, want 1/0", connection.closeCalls.Load(), connection.rawCalls.Load())
		}
	})

	t.Run("failed_rollback_confirmed_discard", func(t *testing.T) {
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				switch statement {
				case "BEGIN IMMEDIATE":
					return relationFaultResult(0), nil
				case "ROLLBACK":
					return nil, rollbackErr
				default:
					return nil, primary
				}
			},
		}
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.Delete(context.Background(), relationDeleteTestPlan(1))
			return err
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("confirmed discard error = %v", err)
		}
		if connection.rawCalls.Load() != 1 || connection.closeCalls.Load() != 0 {
			t.Fatalf("raw/close calls = %d/%d, want 1/0", connection.rawCalls.Load(), connection.closeCalls.Load())
		}
	})

	t.Run("successful_commit_ignores_connection_close_error", func(t *testing.T) {
		closeErr := errors.New("post-commit close failure")
		connection := &relationFaultConnection{closeErr: closeErr}
		if err := executeAtomicRelation(
			context.Background(),
			func(db.RelationSession) error { return nil },
			connection,
			newRelationRetentionState(),
			nil,
		); err != nil {
			t.Fatalf("successful COMMIT error = %v", err)
		}
		if connection.closeCalls.Load() != 1 {
			t.Fatalf("post-commit close calls = %d, want 1", connection.closeCalls.Load())
		}
	})

	t.Run("successful_commit_ignores_context_transition_during_commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "COMMIT" {
					cancel()
				}
				return relationFaultResult(0), nil
			},
		}
		if err := executeAtomicRelation(
			ctx,
			func(db.RelationSession) error { return nil },
			connection,
			newRelationRetentionState(),
			nil,
		); err != nil {
			t.Fatalf("successful COMMIT with context transition error = %v", err)
		}
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("context after COMMIT = %v, want canceled", ctx.Err())
		}
		if got := connection.statementSnapshot(); fmt.Sprint(got) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "COMMIT"}) {
			t.Fatalf("successful context-transition statements = %v, want BEGIN/COMMIT", got)
		}
		if connection.closeCalls.Load() != 1 || connection.rawCalls.Load() != 0 {
			t.Fatalf("successful context-transition close/raw calls = %d/%d, want 1/0", connection.closeCalls.Load(), connection.rawCalls.Load())
		}
	})
}

func TestForceDiscardRelationConnectionTreatsSQLConnDoneAsConfirmed(t *testing.T) {
	t.Parallel()

	connection := &relationFaultConnection{
		raw: func(func(any) error) error { return sql.ErrConnDone },
	}
	confirmed, err := forceDiscardRelationConnection(connection)
	if !confirmed || err != nil {
		t.Fatalf("forceDiscardRelationConnection() = (%v, %v), want (true, nil)", confirmed, err)
	}
	if connection.rawCalls.Load() != 1 || connection.closeCalls.Load() != 0 {
		t.Fatalf("force-discard raw/close calls = %d/%d, want 1/0", connection.rawCalls.Load(), connection.closeCalls.Load())
	}
}

func TestAtomicRelationCanceledCallbackUsesDetachedBoundedRollback(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var rollbackContextErr error
	var rollbackDeadline time.Time
	connection := &relationFaultConnection{
		exec: func(execCtx context.Context, statement string, _ []any) (sql.Result, error) {
			switch statement {
			case "ROLLBACK":
				rollbackContextErr = execCtx.Err()
				rollbackDeadline, _ = execCtx.Deadline()
				return relationFaultResult(0), nil
			default:
				return relationFaultResult(1), nil
			}
		},
	}
	started := time.Now()
	err := executeAtomicRelation(ctx, func(session db.RelationSession) error {
		if _, err := session.RelationSetNull(ctx, relationSetNullTestPlan(2)); err != nil {
			return err
		}
		cancel()
		return nil
	}, connection, newRelationRetentionState(), nil)
	if !errors.Is(err, context.Canceled) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
		t.Fatalf("canceled callback error = %v", err)
	}
	if rollbackContextErr != nil {
		t.Fatalf("ROLLBACK inherited cancellation: %v", rollbackContextErr)
	}
	if rollbackDeadline.IsZero() || rollbackDeadline.Before(started) || rollbackDeadline.After(started.Add(relationCleanupTimeout+time.Second)) {
		t.Fatalf("ROLLBACK deadline = %v, want bounded detached cleanup", rollbackDeadline)
	}
	if connection.closeCalls.Load() != 1 {
		t.Fatalf("rollback close calls = %d, want 1", connection.closeCalls.Load())
	}
}

func TestRelationSessionMarksMutationPossibleImmediatelyBeforeExecution(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	tests := []struct {
		name   string
		invoke func(*relationSession) (int64, error)
	}{
		{
			name: "insert",
			invoke: func(session *relationSession) (int64, error) {
				return session.Insert(context.Background(), query.NewInsertPlan(
					"authors_author",
					[]query.Assignment{query.NewAssignment(name, query.String("Ada"))},
				))
			},
		},
		{
			name: "update",
			invoke: func(session *relationSession) (int64, error) {
				return session.Update(context.Background(), query.NewUpdatePlan(
					"authors_author",
					[]query.Assignment{query.NewAssignment(name, query.String("Grace"))},
					id,
					query.Integer(1),
				))
			},
		},
		{
			name: "delete",
			invoke: func(session *relationSession) (int64, error) {
				return session.Delete(context.Background(), relationDeleteTestPlan(1))
			},
		},
		{
			name: "relation_set_null",
			invoke: func(session *relationSession) (int64, error) {
				return session.RelationSetNull(context.Background(), relationSetNullTestPlan(2))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := &relationSession{active: true}
			connection := &relationFaultConnection{}
			connection.exec = func(context.Context, string, []any) (sql.Result, error) {
				if !session.mutationPossible {
					t.Error("executor observed mutationPossible=false")
				}
				return relationFaultResult(1), nil
			}
			session.connection = connection
			rows, err := test.invoke(session)
			if err != nil || rows != 1 {
				t.Fatalf("mutation = (%d, %v), want (1, nil)", rows, err)
			}
			if !session.mutationPossible || len(connection.statementSnapshot()) != 1 {
				t.Fatalf("mutationPossible/statements = %v/%v", session.mutationPossible, connection.statementSnapshot())
			}
		})
	}
}

func TestRelationSessionValidationFailureDoesNotMarkMutationPossible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		invoke func(*relationSession) (int64, error)
	}{
		{name: "insert", invoke: func(session *relationSession) (int64, error) {
			return session.Insert(context.Background(), query.InsertPlan{})
		}},
		{name: "update", invoke: func(session *relationSession) (int64, error) {
			return session.Update(context.Background(), query.UpdatePlan{})
		}},
		{name: "delete", invoke: func(session *relationSession) (int64, error) {
			return session.Delete(context.Background(), query.DeletePlan{})
		}},
		{name: "relation_set_null", invoke: func(session *relationSession) (int64, error) {
			return session.RelationSetNull(context.Background(), query.RelationSetNullPlan{})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connection := &relationFaultConnection{}
			session := &relationSession{connection: connection, active: true}
			rows, err := test.invoke(session)
			if rows != 0 || !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("invalid mutation = (%d, %v), want invalid_plan", rows, err)
			}
			if session.mutationPossible || len(connection.statementSnapshot()) != 0 {
				t.Fatalf("invalid mutationPossible/statements = %v/%v", session.mutationPossible, connection.statementSnapshot())
			}
		})
	}
}

func TestExecuteAtomicRelationPanicWithUnconfirmedCleanupRetainsAndRepanics(t *testing.T) {
	t.Parallel()

	rollbackErr := errors.New("rollback failure")
	connection := &relationFaultConnection{
		exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
			if statement == "ROLLBACK" {
				return nil, rollbackErr
			}
			return relationFaultResult(1), nil
		},
		raw: func(func(any) error) error { return nil },
	}
	retention := newRelationRetentionState()
	panicValue := &struct{ value string }{value: "exact"}
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Fatalf("recovered panic = %#v, want exact %#v", got, panicValue)
			}
		}()
		_ = executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			if _, err := session.Delete(context.Background(), relationDeleteTestPlan(1)); err != nil {
				t.Fatal(err)
			}
			panic(panicValue)
		}, connection, retention, nil)
	}()
	if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
		t.Fatalf("panic cleanup close/retained = %d/%d, want 0/1", connection.closeCalls.Load(), retainedConnectionCount(retention))
	}
	if err := retention.sealAndDrain(); err != nil {
		t.Fatalf("sealAndDrain() error = %v", err)
	}
	if connection.closeCalls.Load() != 1 {
		t.Fatalf("terminal panic close calls = %d, want 1", connection.closeCalls.Load())
	}
}

func TestRelationRetentionPostSealAndIdempotentDrain(t *testing.T) {
	t.Parallel()

	state := newRelationRetentionState()
	if err := state.sealAndDrain(); err != nil {
		t.Fatal(err)
	}
	connection := &relationFaultConnection{}
	if err := state.retain(connection); err != nil {
		t.Fatalf("post-seal retain error = %v", err)
	}
	if connection.closeCalls.Load() != 1 || retainedConnectionCount(state) != 0 {
		t.Fatalf("post-seal close/retained = %d/%d", connection.closeCalls.Load(), retainedConnectionCount(state))
	}
	if err := state.sealAndDrain(); err != nil {
		t.Fatalf("second sealAndDrain error = %v", err)
	}
	if connection.closeCalls.Load() != 1 {
		t.Fatalf("second drain close calls = %d, want 1", connection.closeCalls.Load())
	}
}

func TestBackendCloseClosesDatabaseBeforeDrainingRetainedConnections(t *testing.T) {
	t.Parallel()

	registerRelationCloseDriver.Do(func() {
		sql.Register(relationCloseDriverName, relationCloseDriver{})
	})
	databaseCloseErr := errors.New("database close failure")
	retainedCloseErr := errors.New("retained close failure")
	configuration := &relationCloseConfiguration{databaseCloseErr: databaseCloseErr}
	dsn := fmt.Sprintf("close-%d", relationCloseSequence.Add(1))
	relationCloseConfigurations.Store(dsn, configuration)
	t.Cleanup(func() { relationCloseConfigurations.Delete(dsn) })

	database, err := sql.Open(relationCloseDriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxIdleConns(1)
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	retained := &relationFaultConnection{
		closeErr: retainedCloseErr,
		closeHook: func() {
			configuration.record("retained")
		},
	}
	state := newRelationRetentionState()
	if err := state.retain(retained); err != nil {
		t.Fatal(err)
	}
	backend := &Backend{database: database, relationRetention: state}
	err = backend.Close()
	if !errors.Is(err, databaseCloseErr) || !errors.Is(err, retainedCloseErr) {
		t.Fatalf("Backend.Close() error = %v, want both close failures", err)
	}
	if got := configuration.snapshot(); fmt.Sprint(got) != fmt.Sprint([]string{"database", "retained"}) {
		t.Fatalf("close order = %v, want [database retained]", got)
	}
	if retained.closeCalls.Load() != 1 {
		t.Fatalf("retained close calls = %d, want 1", retained.closeCalls.Load())
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("second Backend.Close() error = %v", err)
	}
	if got := configuration.snapshot(); fmt.Sprint(got) != fmt.Sprint([]string{"database", "retained"}) {
		t.Fatalf("second close changed order = %v", got)
	}
}

func TestRelationRetentionRaceWithSealClosesEveryConnectionOnce(t *testing.T) {
	state := newRelationRetentionState()
	connections := make([]*relationFaultConnection, 128)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range connections {
		connection := &relationFaultConnection{}
		connections[index] = connection
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if err := state.retain(connection); err != nil {
				t.Errorf("retain() error = %v", err)
			}
		}()
	}
	close(start)
	if err := state.sealAndDrain(); err != nil {
		t.Fatalf("sealAndDrain() error = %v", err)
	}
	group.Wait()
	for index, connection := range connections {
		if got := connection.closeCalls.Load(); got != 1 {
			t.Fatalf("connection[%d] close calls = %d, want 1", index, got)
		}
	}
	if count := retainedConnectionCount(state); count != 0 {
		t.Fatalf("retained connections = %d, want 0", count)
	}
}

func provisionRelationTransactionFixture(t *testing.T, backend *Backend) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" TEXT NOT NULL)`,
		`CREATE TABLE "blog_post" (` +
			`"id" INTEGER NOT NULL PRIMARY KEY, ` +
			`"author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE NO ACTION, ` +
			`"reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id") ON DELETE NO ACTION)`,
		`INSERT INTO "authors_author" ("id", "name") VALUES (1, 'Ada'), (2, 'Bob')`,
		`INSERT INTO "blog_post" ("id", "author_id", "reviewer_id") VALUES (10, 1, 2), (11, 1, 2)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("provision relation fixture: %v", err)
		}
	}
}

func relationSetNullTestPlan(target int64) query.RelationSetNullPlan {
	return query.NewRelationSetNullPlan(
		"blog_post",
		query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true),
		query.Integer(target),
	)
}

func relationDeleteTestPlan(target int64) query.DeletePlan {
	return query.NewDeletePlan(
		"authors_author",
		query.NewFieldRef("id", "id", query.FieldInteger, false),
		query.Integer(target),
	)
}

func assertRelationFixtureState(t *testing.T, backend *Backend, authors, reviewerTwo int) {
	t.Helper()
	ctx := context.Background()
	var gotAuthors int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "authors_author"`).Scan(&gotAuthors); err != nil {
		t.Fatal(err)
	}
	var gotReviewers int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "blog_post" WHERE "reviewer_id" = 2`).Scan(&gotReviewers); err != nil {
		t.Fatal(err)
	}
	if gotAuthors != authors || gotReviewers != reviewerTwo {
		t.Fatalf("fixture state authors/reviewer2 = %d/%d, want %d/%d", gotAuthors, gotReviewers, authors, reviewerTwo)
	}
}

func retainedConnectionCount(state *relationRetentionState) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.retained)
}

type relationFaultConnection struct {
	exec       func(context.Context, string, []any) (sql.Result, error)
	query      func(context.Context, string, []any) (*sql.Rows, error)
	raw        func(func(any) error) error
	closeErr   error
	closeHook  func()
	mu         sync.Mutex
	statements []string
	rawCalls   atomic.Int64
	closeCalls atomic.Int64
}

func (connection *relationFaultConnection) statementSnapshot() []string {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return append([]string(nil), connection.statements...)
}

func (connection *relationFaultConnection) ExecContext(ctx context.Context, statement string, arguments ...any) (sql.Result, error) {
	connection.mu.Lock()
	connection.statements = append(connection.statements, statement)
	connection.mu.Unlock()
	if connection.exec != nil {
		return connection.exec(ctx, statement, arguments)
	}
	return relationFaultResult(1), nil
}

func (connection *relationFaultConnection) QueryContext(ctx context.Context, statement string, arguments ...any) (*sql.Rows, error) {
	if connection.query != nil {
		return connection.query(ctx, statement, arguments)
	}
	return nil, errors.New("relation fault connection does not support QueryContext")
}

func (connection *relationFaultConnection) Raw(callback func(any) error) error {
	connection.rawCalls.Add(1)
	if connection.raw != nil {
		return connection.raw(callback)
	}
	return callback(struct{}{})
}

func (connection *relationFaultConnection) Close() error {
	connection.closeCalls.Add(1)
	if connection.closeHook != nil {
		connection.closeHook()
	}
	return connection.closeErr
}

type relationFaultResult int64

func (result relationFaultResult) LastInsertId() (int64, error) { return int64(result), nil }
func (result relationFaultResult) RowsAffected() (int64, error) { return int64(result), nil }

var _ relationPinnedConnection = (*relationFaultConnection)(nil)

var _ driver.Result = relationFaultResult(0)

const relationCloseDriverName = "godj-relation-close-fault"

var (
	registerRelationCloseDriver sync.Once
	relationCloseSequence       atomic.Uint64
	relationCloseConfigurations sync.Map
)

type relationCloseConfiguration struct {
	mu               sync.Mutex
	order            []string
	databaseCloseErr error
}

func (configuration *relationCloseConfiguration) record(value string) {
	configuration.mu.Lock()
	configuration.order = append(configuration.order, value)
	configuration.mu.Unlock()
}

func (configuration *relationCloseConfiguration) snapshot() []string {
	configuration.mu.Lock()
	defer configuration.mu.Unlock()
	return append([]string(nil), configuration.order...)
}

type relationCloseDriver struct{}

func (relationCloseDriver) Open(name string) (driver.Conn, error) {
	value, ok := relationCloseConfigurations.Load(name)
	if !ok {
		return nil, fmt.Errorf("unknown relation close DSN %q", name)
	}
	return &relationCloseDriverConnection{configuration: value.(*relationCloseConfiguration)}, nil
}

type relationCloseDriverConnection struct {
	configuration *relationCloseConfiguration
}

func (*relationCloseDriverConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("relation close driver does not support Prepare")
}

func (connection *relationCloseDriverConnection) Close() error {
	connection.configuration.record("database")
	return connection.configuration.databaseCloseErr
}

func (*relationCloseDriverConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("relation close driver does not support transactions")
}

func (*relationCloseDriverConnection) Ping(context.Context) error { return nil }

var _ driver.Pinger = (*relationCloseDriverConnection)(nil)
