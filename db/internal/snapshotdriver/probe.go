package snapshotdriver

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Backend interface {
	db.SnapshotReader
	Close() error
}

func Probe(t *testing.T, open func(*State) Backend, begin string) {
	t.Helper()
	for _, mode := range []string{"ok", "callback", "cancel", "panic", "goexit", "begin_failure", "rollback_failure"} {
		t.Run(mode, func(t *testing.T) {
			fault := errors.New("injected read-snapshot failure")
			state := &State{}
			if mode == "begin_failure" {
				state.BeginError = fault
			}
			if mode == "rollback_failure" {
				state.RollbackError = fault
			}
			backend := open(state)
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var retained db.Queryer
			var result error
			var panicValue any
			var calls int
			plan := query.NewPlan("snapshot_probe", []query.FieldRef{query.NewFieldRef("id", "id", query.FieldInteger, false)})
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer func() { panicValue = recover() }()
				result = backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
					calls++
					retained = reader
					if _, writable := reader.(db.Mutator); writable {
						return errors.New("read snapshot exposed mutation capability")
					}
					// Deliberately abandon an open rowset. Cleanup must close it
					// before rollback/discard and must not hang on Conn.Raw.
					if _, err := reader.Query(ctx, plan); err != nil {
						return err
					}
					switch mode {
					case "callback":
						return fault
					case "cancel":
						cancel()
					case "panic":
						panic(fault)
					case "goexit":
						runtime.Goexit()
					}
					return nil
				})
			}()
			<-done
			got := state.Snapshot()
			assertFreshConnection := func() {
				state.BeginError, state.RollbackError = nil, nil
				if err := backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
					rows, err := reader.Query(t.Context(), plan)
					if err != nil {
						return err
					}
					return rows.Close()
				}); err != nil {
					t.Fatal("read snapshot did not recover on a fresh connection", err)
				}
				after := state.Snapshot()
				if after.Connections != 2 || after.Closed != 1 || after.OpenRows != 0 {
					t.Fatal("failed transaction connection was reused or leaked", after)
				}
			}
			if got.OpenRows != 0 {
				t.Fatal("read snapshot retained rows", got)
			}
			if mode == "begin_failure" {
				if calls != 0 || !errors.Is(result, fault) || got.Closed != 1 || !reflect.DeepEqual(got.Statements, []string{begin}) {
					t.Fatal("begin failure ownership", calls, result, got)
				}
				assertFreshConnection()
				return
			}
			if calls != 1 || !reflect.DeepEqual(got.Statements, []string{begin, "ROLLBACK"}) {
				t.Fatal("unexpected retry or terminal SQL", calls, got)
			}
			if _, err := retained.Query(t.Context(), plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("expired snapshot remained usable", err)
			}
			switch mode {
			case "callback", "rollback_failure":
				if !errors.Is(result, fault) {
					t.Fatal("lost callback/cleanup failure", result)
				}
			case "cancel":
				if !errors.Is(result, context.Canceled) {
					t.Fatal("lost cancellation", result)
				}
			case "panic":
				if panicValue != fault {
					t.Fatal("panic changed", panicValue)
				}
			default:
				if result != nil {
					t.Fatal(result)
				}
			}
			if errors.Is(result, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(result, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
				t.Fatal("read error claimed an unknown write", result)
			}
			if mode == "rollback_failure" && got.Closed != 1 {
				t.Fatal("uncertain read connection returned to pool", got)
			}
			if mode == "rollback_failure" {
				assertFreshConnection()
			}
		})
	}
}
