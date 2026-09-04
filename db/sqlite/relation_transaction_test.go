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

func TestUnconfirmedRelationCleanupQuarantinesSharedRawAdmission(t *testing.T) {
	t.Parallel()

	beginErr := errors.New("begin failure")
	retention := newRelationRetentionState()
	first := &relationFaultConnection{
		exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
			if statement == "BEGIN IMMEDIATE" {
				return nil, beginErr
			}
			return relationFaultResult(0), nil
		},
		raw: func(func(any) error) error { return nil },
	}
	firstErr := executeAtomicRelation(context.Background(), func(db.RelationSession) error {
		t.Fatal("first callback ran after BEGIN failure")
		return nil
	}, first, retention, nil)
	if !errors.Is(firstErr, beginErr) || retainedConnectionCount(retention) != 1 {
		t.Fatalf("first error/retained = %v/%d, want begin error/1", firstErr, retainedConnectionCount(retention))
	}

	second := &relationFaultConnection{}
	secondCalls := 0
	secondErr := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
		secondCalls++
		return nil
	}, second, retention, nil)
	recoveryRequired := &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}
	if !errors.Is(firstErr, recoveryRequired) {
		t.Fatalf("first error = %v, want backend recovery-required marker", firstErr)
	}
	if !errors.Is(secondErr, recoveryRequired) {
		t.Fatalf("second error = %v, want backend recovery-required marker", secondErr)
	}
	if secondCalls != 0 || len(second.statementSnapshot()) != 0 || second.rawCalls.Load() != 0 || second.closeCalls.Load() != 0 {
		t.Fatalf(
			"second callback/statements/raw/close = %d/%v/%d/%d, want 0/[]/0/0",
			secondCalls,
			second.statementSnapshot(),
			second.rawCalls.Load(),
			second.closeCalls.Load(),
		)
	}
	if retainedConnectionCount(retention) != 1 {
		t.Fatalf("retained connections = %d, want hard maximum 1", retainedConnectionCount(retention))
	}
	if err := retention.sealAndDrain(); err != nil {
		t.Fatalf("sealAndDrain() error = %v", err)
	}
}

func TestUnconfirmedCoordinatedCleanupQuarantinesRelationAdmission(t *testing.T) {
	t.Parallel()

	beginErr := errors.New("begin failure")
	state := newRelationRetentionState()
	first := &relationFaultConnection{
		exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
			if statement == "BEGIN IMMEDIATE" {
				return nil, beginErr
			}
			return relationFaultResult(0), nil
		},
		raw: func(func(any) error) error { return nil },
	}
	firstCalls := 0
	firstErr := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
		firstCalls++
		return nil
	}, first, state, nil)
	recoveryRequired := &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}
	if firstCalls != 0 || !errors.Is(firstErr, beginErr) || !errors.Is(firstErr, recoveryRequired) {
		t.Fatalf("first callback/error = %d/%v, want 0/begin+recovery-required", firstCalls, firstErr)
	}

	second := &relationFaultConnection{}
	secondCalls := 0
	secondErr := executeAtomicRelation(context.Background(), func(db.RelationSession) error {
		secondCalls++
		return nil
	}, second, state, nil)
	if !errors.Is(secondErr, recoveryRequired) {
		t.Fatalf("second error = %v, want backend recovery required", secondErr)
	}
	if secondCalls != 0 || len(second.statementSnapshot()) != 0 || second.rawCalls.Load() != 0 || second.closeCalls.Load() != 0 {
		t.Fatalf(
			"second callback/statements/raw/close = %d/%v/%d/%d, want 0/[]/0/0",
			secondCalls,
			second.statementSnapshot(),
			second.rawCalls.Load(),
			second.closeCalls.Load(),
		)
	}
	if retainedConnectionCount(state) != 1 {
		t.Fatalf("retained connections = %d, want hard maximum 1", retainedConnectionCount(state))
	}
	if err := state.sealAndDrain(); err != nil {
		t.Fatal(err)
	}
}

func TestUnconfirmedRelationCleanupQuarantinesBackendIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "relation-quarantine-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})

	beginErr := errors.New("begin failure")
	connection := &relationFaultConnection{
		exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
			if statement == "BEGIN IMMEDIATE" {
				return nil, beginErr
			}
			return relationFaultResult(0), nil
		},
		raw: func(func(any) error) error { return nil },
	}
	if err := executeAtomicRelation(ctx, func(db.RelationSession) error {
		t.Fatal("callback ran after BEGIN failure")
		return nil
	}, connection, backend.relationRetention, nil); !errors.Is(err, beginErr) {
		t.Fatalf("quarantine trigger error = %v, want begin error", err)
	}

	version, err := backend.SQLiteVersion(ctx)
	recoveryRequired := &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}
	if version != "" || !errors.Is(err, recoveryRequired) {
		t.Fatalf("SQLiteVersion() after quarantine = (%q, %v), want empty/recovery-required", version, err)
	}
}

func TestRelationTransactionAdmissionHonorsCancellationAndRecoversNormally(t *testing.T) {
	t.Parallel()

	state := newRelationRetentionState()
	holder, err := state.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	cancelWaiter()
	waiterConnection := &relationFaultConnection{}
	waiterCalls := 0
	waiterErr := executeCoordinatedAtomic(waiterCtx, func(db.Session) error {
		waiterCalls++
		return nil
	}, waiterConnection, state, nil)
	if !errors.Is(waiterErr, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", waiterErr)
	}
	if waiterCalls != 0 || len(waiterConnection.statementSnapshot()) != 0 || waiterConnection.rawCalls.Load() != 0 || waiterConnection.closeCalls.Load() != 0 {
		t.Fatalf(
			"canceled waiter callback/statements/raw/close = %d/%v/%d/%d, want 0/[]/0/0",
			waiterCalls,
			waiterConnection.statementSnapshot(),
			waiterConnection.rawCalls.Load(),
			waiterConnection.closeCalls.Load(),
		)
	}
	holder.release()

	successorConnection := &relationFaultConnection{}
	successorCalls := 0
	if err := executeAtomicRelation(context.Background(), func(db.RelationSession) error {
		successorCalls++
		return nil
	}, successorConnection, state, nil); err != nil {
		t.Fatalf("normal successor error = %v", err)
	}
	if successorCalls != 1 || fmt.Sprint(successorConnection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "COMMIT"}) || successorConnection.closeCalls.Load() != 1 {
		t.Fatalf(
			"normal successor callback/statements/close = %d/%v/%d, want 1/[BEGIN IMMEDIATE COMMIT]/1",
			successorCalls,
			successorConnection.statementSnapshot(),
			successorConnection.closeCalls.Load(),
		)
	}
}

func TestRelationTransactionAdmissionLiveWaiters(t *testing.T) {
	t.Parallel()

	t.Run("normal release", func(t *testing.T) {
		state := newRelationRetentionState()
		holder, err := state.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		observed := &doneObservedContext{Context: context.Background(), observed: make(chan struct{})}
		connection := &relationFaultConnection{}
		var callbacks atomic.Int32
		result := make(chan error, 1)
		go func() {
			result <- executeCoordinatedAtomic(observed, func(db.Session) error {
				callbacks.Add(1)
				return nil
			}, connection, state, nil)
		}()
		select {
		case <-observed.observed:
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not enter the blocking admission path")
		}
		holder.release()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("normal live waiter error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("normal live waiter did not wake after release")
		}
		if callbacks.Load() != 1 || fmt.Sprint(connection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "COMMIT"}) {
			t.Fatalf("normal live waiter callback/statements = %d/%v, want 1/[BEGIN IMMEDIATE COMMIT]", callbacks.Load(), connection.statementSnapshot())
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		state := newRelationRetentionState()
		holder, err := state.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		observed := &doneObservedContext{Context: ctx, observed: make(chan struct{})}
		result := make(chan error, 1)
		go func() {
			admission, err := state.acquire(observed)
			if admission != nil {
				admission.release()
			}
			result <- err
		}()
		select {
		case <-observed.observed:
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not enter the blocking admission path")
		}
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("live canceled waiter error = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("live canceled waiter did not wake")
		}
		holder.release()
	})

	t.Run("quarantine", func(t *testing.T) {
		state := newRelationRetentionState()
		holder, err := state.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		observed := &doneObservedContext{Context: context.Background(), observed: make(chan struct{})}
		result := make(chan error, 1)
		go func() {
			admission, err := state.acquire(observed)
			if admission != nil {
				admission.release()
			}
			result <- err
		}()
		select {
		case <-observed.observed:
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not enter the blocking admission path")
		}
		retained := &relationFaultConnection{}
		if err := holder.retain(retained); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
			t.Fatalf("retain error = %v, want backend recovery required", err)
		}
		select {
		case err := <-result:
			if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
				t.Fatalf("live quarantine waiter error = %v, want backend recovery required", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("live quarantine waiter did not wake")
		}
		holder.release()
		if err := state.sealAndDrain(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRelationTransactionAdmissionCloseRejectsWaitersButDefersRetainedClose(t *testing.T) {
	t.Parallel()

	state := newRelationRetentionState()
	holder, err := state.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.beginClose()

	if waiter, err := state.acquire(context.Background()); waiter != nil || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("post-beginClose acquire = (%v, %v), want nil/invalid_plan", waiter, err)
	}
	retained := &relationFaultConnection{}
	if err := holder.retain(retained); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
		t.Fatalf("retain while pool close is pending = %v, want backend recovery required", err)
	}
	if retained.closeCalls.Load() != 0 || retainedConnectionCount(state) != 1 {
		t.Fatalf("pre-seal close/retained = %d/%d, want 0/1", retained.closeCalls.Load(), retainedConnectionCount(state))
	}
	if err := state.sealAndDrain(); err != nil {
		t.Fatal(err)
	}
	holder.release()
	if retained.closeCalls.Load() != 1 || retainedConnectionCount(state) != 0 {
		t.Fatalf("post-seal close/retained = %d/%d, want 1/0", retained.closeCalls.Load(), retainedConnectionCount(state))
	}
}

func TestNestedRawTransactionRequiresBoundedContext(t *testing.T) {
	t.Parallel()

	state := newRelationRetentionState()
	outerConnection := &relationFaultConnection{}
	innerConnection := &relationFaultConnection{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	innerContext := &doneObservedContext{Context: ctx, observed: make(chan struct{})}
	innerCalls := 0
	result := make(chan error, 1)
	go func() {
		result <- executeAtomicRelation(ctx, func(db.RelationSession) error {
			return executeCoordinatedAtomic(innerContext, func(db.Session) error {
				innerCalls++
				return nil
			}, innerConnection, state, nil)
		}, outerConnection, state, nil)
	}()
	select {
	case <-innerContext.observed:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("nested raw transaction did not reach the shared admission wait")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("nested raw transaction error = %v, want context cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nested raw transaction did not stop after context cancellation")
	}
	if innerCalls != 0 || len(innerConnection.statementSnapshot()) != 0 || innerConnection.rawCalls.Load() != 0 || innerConnection.closeCalls.Load() != 0 {
		t.Fatalf(
			"nested callback/statements/raw/close = %d/%v/%d/%d, want 0/[]/0/0",
			innerCalls,
			innerConnection.statementSnapshot(),
			innerConnection.rawCalls.Load(),
			innerConnection.closeCalls.Load(),
		)
	}
	if fmt.Sprint(outerConnection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "ROLLBACK"}) || outerConnection.closeCalls.Load() != 1 {
		t.Fatalf("outer statements/close = %v/%d, want [BEGIN IMMEDIATE ROLLBACK]/1", outerConnection.statementSnapshot(), outerConnection.closeCalls.Load())
	}
	if err := state.availabilityError(); err != nil {
		t.Fatalf("availability after confirmed nested rollback = %v, want healthy", err)
	}
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
			!errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
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
		if !errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
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
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
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
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
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
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.Delete(context.Background(), relationDeleteTestPlan(1))
			return err
		}, connection, retention, nil)
		if !errors.Is(err, primaryCause) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("confirmed cleanup error = %v", err)
		}
		if connection.closeCalls.Load() != 1 || connection.rawCalls.Load() != 0 {
			t.Fatalf("close/raw calls = %d/%d, want 1/0", connection.closeCalls.Load(), connection.rawCalls.Load())
		}
		if err := retention.availabilityError(); err != nil {
			t.Fatalf("confirmed rollback availability = %v, want healthy", err)
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
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
			!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
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
		retention := newRelationRetentionState()
		err := executeAtomicRelation(context.Background(), func(session db.RelationSession) error {
			_, err := session.Delete(context.Background(), relationDeleteTestPlan(1))
			return err
		}, connection, retention, nil)
		if !errors.Is(err, primaryCause) || !errors.Is(err, rollbackErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("confirmed discard error = %v", err)
		}
		if connection.rawCalls.Load() != 1 || connection.closeCalls.Load() != 0 {
			t.Fatalf("raw/close calls = %d/%d, want 1/0", connection.rawCalls.Load(), connection.closeCalls.Load())
		}
		if err := retention.availabilityError(); err != nil {
			t.Fatalf("confirmed discard availability = %v, want healthy", err)
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
	if err := retention.availabilityError(); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
		t.Fatalf("availability after panic cleanup = %v, want backend recovery required", err)
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
	admission, err := state.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admission.release()
	if err := state.sealAndDrain(); err != nil {
		t.Fatal(err)
	}
	connection := &relationFaultConnection{}
	if err := admission.retain(connection); err != nil {
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
	admission, err := state.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := admission.retain(retained); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
		t.Fatalf("retain() error = %v, want backend recovery required", err)
	}
	admission.release()
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

func TestBackendCloseWakesAdmissionWaiterBeforePoolDrain(t *testing.T) {
	registerRelationCloseDriver.Do(func() {
		sql.Register(relationCloseDriverName, relationCloseDriver{})
	})
	closeStarted := make(chan struct{})
	allowClose := make(chan struct{})
	configuration := &relationCloseConfiguration{
		databaseCloseStarted: closeStarted,
		allowDatabaseClose:   allowClose,
	}
	dsn := fmt.Sprintf("close-barrier-%d", relationCloseSequence.Add(1))
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
	state := newRelationRetentionState()
	holder, err := state.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	backend := &Backend{database: database, relationRetention: state}

	waiterContext := &doneObservedContext{Context: context.Background(), observed: make(chan struct{})}
	waiterResult := make(chan error, 1)
	go func() {
		admission, err := state.acquire(waiterContext)
		if admission != nil {
			admission.release()
		}
		waiterResult <- err
	}()
	select {
	case <-waiterContext.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not enter the blocking admission path")
	}
	closeResult := make(chan error, 1)
	go func() { closeResult <- backend.Close() }()

	select {
	case <-closeStarted:
	case <-time.After(5 * time.Second):
		close(allowClose)
		t.Fatal("database close did not reach barrier")
	}
	select {
	case err := <-waiterResult:
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			close(allowClose)
			t.Fatalf("waiter error = %v, want closed invalid_plan", err)
		}
	case <-time.After(5 * time.Second):
		close(allowClose)
		t.Fatal("admission waiter did not wake while database close was blocked")
	}

	retained := &relationFaultConnection{closeHook: func() { configuration.record("retained") }}
	if err := holder.retain(retained); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
		close(allowClose)
		t.Fatalf("retain during database close = %v, want recovery required", err)
	}
	if retained.closeCalls.Load() != 0 || retainedConnectionCount(state) != 1 {
		close(allowClose)
		t.Fatalf("pre-pool-seal close/retained = %d/%d, want 0/1", retained.closeCalls.Load(), retainedConnectionCount(state))
	}
	holder.release()

	if err := backend.Close(); err != nil {
		close(allowClose)
		t.Fatalf("concurrent Close() loser = %v", err)
	}
	close(allowClose)
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() winner = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close() winner did not complete after the database close barrier was released")
	}
	if got := configuration.snapshot(); fmt.Sprint(got) != fmt.Sprint([]string{"database", "retained"}) {
		t.Fatalf("close order = %v, want [database retained]", got)
	}
	if retained.closeCalls.Load() != 1 || retainedConnectionCount(state) != 0 {
		t.Fatalf("post-pool-seal close/retained = %d/%d, want 1/0", retained.closeCalls.Load(), retainedConnectionCount(state))
	}
}

func TestRelationRetentionRaceWithSealClosesAdmittedConnectionOnce(t *testing.T) {
	state := newRelationRetentionState()
	admission, err := state.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	connection := &relationFaultConnection{}
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		err := admission.retain(connection)
		if err != nil && !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeBackendRecoveryRequired}) {
			t.Errorf("retain() error = %v", err)
		}
	}()
	go func() {
		defer group.Done()
		<-start
		if err := state.sealAndDrain(); err != nil {
			t.Errorf("sealAndDrain() error = %v", err)
		}
	}()
	close(start)
	group.Wait()
	admission.release()
	if got := connection.closeCalls.Load(); got != 1 {
		t.Fatalf("connection close calls = %d, want 1", got)
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
	if state.retained != nil {
		return 1
	}
	return 0
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

type doneObservedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (ctx *doneObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
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
	mu                   sync.Mutex
	order                []string
	databaseCloseErr     error
	databaseCloseStarted chan struct{}
	allowDatabaseClose   <-chan struct{}
	databaseCloseOnce    sync.Once
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
	if started := connection.configuration.databaseCloseStarted; started != nil {
		connection.configuration.databaseCloseOnce.Do(func() { close(started) })
	}
	if allowed := connection.configuration.allowDatabaseClose; allowed != nil {
		<-allowed
	}
	connection.configuration.record("database")
	return connection.configuration.databaseCloseErr
}

func (*relationCloseDriverConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("relation close driver does not support transactions")
}

func (*relationCloseDriverConnection) Ping(context.Context) error { return nil }

var _ driver.Pinger = (*relationCloseDriverConnection)(nil)
