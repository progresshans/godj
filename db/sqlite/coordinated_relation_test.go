package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestCoordinatedRelationAtomicOwnsMutationsRollbackAndSessionLifetime(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend, err := OpenMemory(ctx, "coordinated-relation-"+outcome)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			provisionRelationTransactionFixture(t, backend)
			callbackErr := errors.New("callback rejected")
			calls := 0
			var retained db.RelationSession
			err = backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error {
				calls++
				retained = session
				if n, err := session.RelationSetNull(ctx, relationSetNullTestPlan(2)); err != nil || n != 2 {
					t.Fatalf("SET_NULL: %d %v", n, err)
				}
				if n, err := session.Delete(ctx, relationDeleteTestPlan(2)); err != nil || n != 1 {
					t.Fatalf("Delete: %d %v", n, err)
				}
				if outcome == "rollback" {
					return callbackErr
				}
				if outcome == "cancel" {
					cancel()
				}
				return nil
			})
			if calls != 1 {
				t.Fatal("callback count", calls)
			}
			if outcome == "commit" {
				if err != nil {
					t.Fatal(err)
				}
				assertRelationFixtureState(t, backend, 1, 0)
			} else {
				cause := callbackErr
				if outcome == "cancel" {
					cause = context.Canceled
				}
				if !errors.Is(err, cause) {
					t.Fatal("failure lost", err)
				}
				assertRelationFixtureState(t, backend, 2, 2)
			}
			if n, err := retained.RelationSetNull(context.Background(), relationSetNullTestPlan(1)); n != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("expired relation session usable", n, err)
			}
			if n, err := retained.Delete(context.Background(), relationDeleteTestPlan(1)); n != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("expired ordinary mutator usable", n, err)
			}
		})
	}
}

func TestCoordinatedRelationAtomicRejectsPreconditionsBeforeCallback(t *testing.T) {
	ctx := context.Background()
	backend, err := OpenMemory(ctx, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	calls := 0
	callback := func(db.RelationSession) error { calls++; return nil }
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, invoke := range []func() error{func() error { return backend.CoordinatedAtomicRelation(nil, callback) }, func() error { return backend.CoordinatedAtomicRelation(ctx, nil) }, func() error { return backend.CoordinatedAtomicRelation(canceled, callback) }, func() error { return (*Backend)(nil).CoordinatedAtomicRelation(ctx, callback) }} {
		if err := invoke(); err == nil {
			t.Fatal("invalid call accepted")
		}
	}
	if _, err := backend.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomicRelation(ctx, callback); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("disabled FK checks accepted", err)
	}
	if calls != 0 {
		t.Fatal("precondition failure invoked callback", calls)
	}
	if _, err := backend.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomicRelation(ctx, callback); err != nil || calls != 1 {
		t.Fatal("admission was not released after rejection", err, calls)
	}
}
