package db_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type snapshotResultOwner struct {
	invoke func(context.Context, func(db.Queryer) error) error
}

func (owner *snapshotResultOwner) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	return owner.invoke(ctx, read)
}

type resultReadScope struct{ failure error }

func (*resultReadScope) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (scope *resultReadScope) ValidateSession(ctx context.Context) error {
	return errors.Join(ctx.Err(), scope.failure)
}

type resultQueryOnly struct{}

func (resultQueryOnly) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unexpected unscoped query")
}

type resultReadTransaction struct{ resultReadScope }

func (*resultReadTransaction) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	return errors.New("unexpected nested snapshot")
}

type resultReadMutation struct{ resultReadScope }

func (*resultReadMutation) Insert(context.Context, query.InsertPlan) (int64, error) {
	return 0, errors.New("unexpected mutation")
}
func (*resultReadMutation) Update(context.Context, query.UpdatePlan) (int64, error) {
	return 0, errors.New("unexpected mutation")
}
func (*resultReadMutation) Delete(context.Context, query.DeletePlan) (int64, error) {
	return 0, errors.New("unexpected mutation")
}

func TestReadSnapshotResultConfirmsCleanupAndCallbackOwnership(t *testing.T) {
	for _, mode := range []string{"success", "skipped", "retained", "repeated", "concurrent", "unfinished", "swallowed", "replaced_error", "wrapped_error", "begin_failure", "cleanup_failure", "callback_failure", "cancel_before_callback", "cancel_in_callback", "cancel_after_callback", "nil_reader", "typed_nil_reader", "unscoped_reader", "mutable_reader", "transaction_reader", "expired_reader", "expires_in_callback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure, other := errors.New("preparation failed"), errors.New("read owner failed")
			reader := &resultReadScope{}
			var retained func(db.Queryer) error
			var observed context.Context
			var entered atomic.Int32
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			owner := &snapshotResultOwner{invoke: func(_ context.Context, callback func(db.Queryer) error) error {
				switch mode {
				case "skipped":
					return nil
				case "retained":
					retained = callback
					return nil
				case "begin_failure":
					return other
				case "nil_reader":
					return callback(nil)
				case "typed_nil_reader":
					return callback((*resultReadScope)(nil))
				case "unscoped_reader":
					return callback(resultQueryOnly{})
				case "mutable_reader":
					return callback(&resultReadMutation{})
				case "transaction_reader":
					return callback(&resultReadTransaction{})
				case "expired_reader":
					reader.failure = other
				case "cancel_before_callback":
					cancel()
				case "repeated":
					return errors.Join(callback(reader), callback(reader))
				case "concurrent", "unfinished":
					go func() { finished <- callback(reader) }()
					<-started
					if mode == "unfinished" {
						return nil
					}
					err := callback(reader)
					close(release)
					return errors.Join(err, <-finished)
				}
				err := callback(reader)
				switch mode {
				case "swallowed":
					return nil
				case "replaced_error":
					return other
				case "wrapped_error":
					return fmt.Errorf("wrapped callback: %w", err)
				case "cleanup_failure":
					return errors.Join(err, other)
				case "cancel_after_callback":
					cancel()
				}
				return err
			}}
			result, err := db.ReadSnapshotResult(ctx, owner, func(work context.Context, handle db.Queryer) (int, error) {
				entered.Add(1)
				observed = work
				if handle != reader {
					t.Error("non-borrowed capability reached callback")
				}
				if mode == "concurrent" || mode == "unfinished" {
					close(started)
					<-release
				}
				if mode == "swallowed" || mode == "replaced_error" || mode == "wrapped_error" || mode == "callback_failure" {
					return 73, failure
				}
				if mode == "cancel_in_callback" {
					cancel()
				}
				if mode == "expires_in_callback" {
					reader.failure = other
				}
				return 73, nil
			})
			if mode == "unfinished" {
				close(release)
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Fatal("test callback did not join")
				}
			}
			if retained != nil && retained(reader) == nil {
				t.Fatal("callback retained after owner return was admitted")
			}
			if mode == "success" {
				if err != nil || result != 73 {
					t.Fatal("confirmed read was lost", result, err)
				}
			} else if err == nil || result != 0 {
				t.Fatal("unconfirmed read was published", result, err)
			}
			if entered.Load() > 1 || observed != nil && observed.Err() == nil {
				t.Fatal("read callback ownership was not bounded", entered.Load())
			}
			if mode == "swallowed" || mode == "replaced_error" || mode == "wrapped_error" || mode == "callback_failure" {
				if !errors.Is(err, failure) {
					t.Fatal("callback cause was lost", err)
				}
			}
			if mode == "cancel_in_callback" || mode == "cancel_after_callback" || mode == "cancel_before_callback" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("read cancellation was lost", err)
				}
			}
			if errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
				t.Fatal("read failure was classified as a write outcome", err)
			}
		})
	}
}

func TestReadSnapshotResultRejectsInvalidConfigurationBeforeOwner(t *testing.T) {
	calls := 0
	owner := &snapshotResultOwner{invoke: func(context.Context, func(db.Queryer) error) error { calls++; return nil }}
	read := func(context.Context, db.Queryer) (int, error) { t.Fatal("invalid read was invoked"); return 1, nil }
	for _, backend := range []db.SnapshotReader{nil, (*snapshotResultOwner)(nil)} {
		if value, err := db.ReadSnapshotResult(t.Context(), backend, read); err == nil || value != 0 {
			t.Fatal("invalid backend accepted", err)
		}
	}
	if value, err := db.ReadSnapshotResult[int](t.Context(), owner, nil); err == nil || value != 0 {
		t.Fatal("nil callback accepted", err)
	}
	if value, err := db.ReadSnapshotResult(nil, owner, read); err == nil || value != 0 {
		t.Fatal("nil context accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := db.ReadSnapshotResult(ctx, owner, read); !errors.Is(err, context.Canceled) || value != 0 {
		t.Fatal("canceled context accepted", err)
	}
	if calls != 0 {
		t.Fatal("invalid configuration reached snapshot owner", calls)
	}
}
