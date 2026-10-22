package db_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type resultOwner struct {
	invoke func(context.Context, func(db.Session) error) error
}

func (owner *resultOwner) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return owner.invoke(ctx, callback)
}

func TestAtomicResultRequiresOneJoinedCallbackAndConfirmedCommit(t *testing.T) {
	for _, mode := range []string{"success", "skipped", "repeated", "concurrent", "retained", "unfinished", "swallowed", "replaced_error", "wrapped_error", "begin_failure", "unknown_commit", "post_commit_cancel", "nil_session", "typed_nil_session", "expired_session"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure, other := errors.New("preparation failed"), errors.New("backend failed")
			unknown := &query.Error{Code: query.CodeCommitOutcomeUnknown}
			var entered atomic.Int32
			var retained func(db.Session) error
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var workContext context.Context
			owner := &resultOwner{invoke: func(_ context.Context, callback func(db.Session) error) error {
				session := &plainSession{}
				switch mode {
				case "skipped":
					return nil
				case "retained":
					retained = callback
					return nil
				case "begin_failure":
					return other
				case "nil_session":
					return callback(nil)
				case "typed_nil_session":
					return callback((*plainSession)(nil))
				case "expired_session":
					return callback(&savepointSession{validation: other})
				case "repeated":
					return errors.Join(callback(session), callback(session))
				case "concurrent", "unfinished":
					go func() { finished <- callback(session) }()
					<-started
					if mode == "unfinished" {
						return nil
					}
					err := callback(session)
					close(release)
					return errors.Join(err, <-finished)
				case "swallowed":
					_ = callback(session)
					return nil
				case "replaced_error":
					_ = callback(session)
					return other
				case "wrapped_error":
					return errors.Join(other, callback(session))
				}
				if err := callback(session); err != nil {
					return err
				}
				if mode == "unknown_commit" {
					return unknown
				}
				if mode == "post_commit_cancel" {
					cancel()
				}
				return nil
			}}
			value, err := db.AtomicResult(ctx, owner, func(work context.Context, _ db.Session) (string, error) {
				entered.Add(1)
				workContext = work
				if mode == "concurrent" || mode == "unfinished" {
					close(started)
					if mode == "concurrent" {
						<-release
					} else {
						<-work.Done()
						return "discarded", work.Err()
					}
				}
				if mode == "swallowed" || mode == "replaced_error" || mode == "wrapped_error" {
					return "discarded", failure
				}
				return "prepared", nil
			})
			if mode == "success" || mode == "post_commit_cancel" {
				if err != nil || value != "prepared" || entered.Load() != 1 {
					t.Fatal("confirmed result was lost", err, value, entered.Load())
				}
			} else if err == nil || value != "" {
				t.Fatal("failure published a partial result", err, value)
			}
			if mode == "skipped" || mode == "repeated" || mode == "concurrent" || mode == "retained" || mode == "unfinished" || mode == "swallowed" || mode == "replaced_error" {
				if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
					t.Fatal("invalid callback ownership lost unknown outcome", err)
				}
			}
			if mode == "unknown_commit" && !errors.Is(err, unknown) || mode == "wrapped_error" && !errors.Is(err, failure) {
				t.Fatal("terminal cause was hidden", err)
			}
			if mode == "unfinished" {
				select {
				case failure := <-finished:
					if !errors.Is(failure, context.Canceled) {
						t.Fatal("unfinished work did not retain cancellation", failure)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("unfinished work escaped its canceled owner")
				}
			}
			if retained != nil && (retained(&plainSession{}) == nil || entered.Load() != 0) {
				t.Fatal("retained callback ran after its owner")
			}
			if entered.Load() > 1 || workContext != nil && !errors.Is(workContext.Err(), context.Canceled) {
				t.Fatal("callback ran repeatedly or retained a live context")
			}
		})
	}
}

type resultRelationSession struct{ plainSession }

func (*resultRelationSession) RelationSetNull(context.Context, query.RelationSetNullPlan) (int64, error) {
	return 0, errors.New("unexpected mutation")
}

type resultRelationOwner struct{ session db.RelationSession }

func (owner *resultRelationOwner) AtomicRelation(_ context.Context, callback func(db.RelationSession) error) error {
	return callback(owner.session)
}

func TestAtomicResultRejectsInvalidConfigurationAndPreservesRelationCapability(t *testing.T) {
	calls := 0
	owner := &resultOwner{invoke: func(context.Context, func(db.Session) error) error { calls++; return nil }}
	callback := func(context.Context, db.Session) (int, error) {
		t.Fatal("invalid configuration ran callback")
		return 0, nil
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if _, err := db.AtomicResult(ctx, owner, callback); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	if _, err := db.AtomicResult[int](t.Context(), owner, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if _, err := db.AtomicResult(t.Context(), (*resultOwner)(nil), callback); err == nil || calls != 0 {
		t.Fatal("invalid owner entered transaction")
	}
	session := &resultRelationSession{}
	value, err := db.RelationAtomicResult(t.Context(), &resultRelationOwner{session}, func(_ context.Context, actual db.RelationSession) (int, error) {
		if actual != session {
			t.Fatal("relation session was replaced")
		}
		return 7, nil
	})
	if err != nil || value != 7 {
		t.Fatal("relation result failed", err)
	}
}
