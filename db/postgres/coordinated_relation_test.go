package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestPostgresCoordinatedRelationUsesExistingFenceAndFailureOwner(t *testing.T) {
	for _, outcome := range []string{"commit", "callback", "acquire", "rollback", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			failure := errors.New("injected failure")
			callbackErr := errors.New("callback rejection")
			state := &coordinatedTransactionTestState{}
			switch outcome {
			case "acquire":
				state.acquireErr = failure
			case "rollback":
				state.rollbackErr = failure
			case "unknown":
				state.commitErr = failure
			}
			backend := newCoordinatedTransactionTestBackend(state)
			t.Cleanup(func() { _ = backend.Close() })
			plan := query.NewRelationSetNullPlan("article", query.NewFieldRef("author", "author_id", query.FieldInteger, true), query.Integer(7))
			var retained db.RelationSession
			calls := 0
			err := backend.CoordinatedAtomicRelation(context.Background(), func(session db.RelationSession) error {
				calls++
				state.record("callback")
				retained = session
				if n, err := session.RelationSetNull(context.Background(), plan); err != nil || n != 1 {
					t.Fatalf("SET_NULL: %d %v", n, err)
				}
				if outcome == "callback" || outcome == "rollback" {
					return callbackErr
				}
				return nil
			})
			want := []string{"begin", "acquire", "callback", "session-exec", "commit"}
			switch outcome {
			case "commit":
				if err != nil {
					t.Fatal(err)
				}
			case "callback":
				want[4] = "rollback"
				if err != callbackErr {
					t.Fatal("confirmed rollback changed callback error", err)
				}
			case "acquire":
				want = []string{"begin", "acquire", "rollback"}
				if !errors.Is(err, failure) || calls != 0 {
					t.Fatal("acquire failure crossed callback", err, calls)
				}
			case "rollback":
				want[4] = "rollback"
				if !errors.Is(err, callbackErr) || !errors.Is(err, failure) || err == callbackErr {
					t.Fatal("rollback failure was hidden", err)
				}
			case "unknown":
				if !errors.Is(err, failure) || !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
					t.Fatal("commit uncertainty hidden", err)
				}
			}
			snapshot := state.snapshot()
			if !reflect.DeepEqual(snapshot.events, want) || len(snapshot.acquireArguments) != 1 || snapshot.acquireArguments[0].Value != postgresCoordinatedAtomicAdvisoryLockKey("godj_test") {
				t.Fatal("fence or lifecycle changed", snapshot)
			}
			if outcome != "acquire" {
				if calls != 1 {
					t.Fatal("callback retried", calls)
				}
				if n, err := retained.RelationSetNull(context.Background(), plan); n != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("expired relation session usable", n, err)
				}
				if !reflect.DeepEqual(state.snapshot().events, want) {
					t.Fatal("expired session reached driver")
				}
			}
		})
	}
}
