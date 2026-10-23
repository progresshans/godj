package batchtest

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// CheckRoot requires the same fixture as Check, with UNIQUE(value) on writes.
// Backends limit the pool to one connection to prove callback affinity.
func CheckRoot(t *testing.T, root interface {
	db.Queryer
	db.BatchQueryer
	db.Mutator
}) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	plan, full := Plan(), []int64{1, 1, 2, 3, 4}
	var retained db.Queryer
	var got []int64
	checked := false
	err := root.QueryBatches(ctx, plan, 2, func(row db.Row) error {
		var value int64
		if err := row.Scan(&value); err != nil {
			return err
		}
		got = append(got, value)
		return nil
	}, func(affinity db.Queryer) (bool, error) {
		retained = affinity
		if _, borrowed := affinity.(db.SessionValidator); borrowed {
			t.Fatal("root affinity became a borrowed session")
		}
		if values, err := Read(ctx, affinity, plan); err != nil || !reflect.DeepEqual(values, full) {
			t.Fatalf("root nested read: %v %v", values, err)
		}
		if checked {
			return true, nil
		}
		checked = true
		for _, variant := range []struct {
			name  string
			owner Owner
		}{
			{"ordinary", affinity.(db.Atomic).Atomic},
			{"coordinated", affinity.(db.CoordinatedAtomic).CoordinatedAtomic},
			{"relation", func(ctx context.Context, cb func(db.Session) error) error {
				return affinity.(db.RelationAtomic).AtomicRelation(ctx, func(s db.RelationSession) error { return cb(s) })
			}},
			{"coordinated_relation", func(ctx context.Context, cb func(db.Session) error) error {
				return affinity.(db.CoordinatedRelationAtomic).CoordinatedAtomicRelation(ctx, func(s db.RelationSession) error { return cb(s) })
			}},
		} {
			t.Run(variant.name, func(t *testing.T) { Check(t, affinity, variant.owner, nil) })
		}
		return true, nil
	})
	if err != nil || !reflect.DeepEqual(got, full) || !checked {
		t.Fatalf("root stream: %v %v", got, err)
	}
	if values, err := Read(ctx, retained, plan); err != nil || !reflect.DeepEqual(values, full) {
		t.Fatalf("retained affinity did not return to root: %v %v", values, err)
	}
	t.Run("retained_scope_reenters_stream", func(t *testing.T) {
		var values []int64
		err := retained.(db.BatchQueryer).QueryBatches(ctx, plan, 3, func(row db.Row) error {
			var value int64
			if err := row.Scan(&value); err != nil {
				return err
			}
			values = append(values, value)
			return nil
		}, func(db.Queryer) (bool, error) { return true, nil })
		if err != nil || !reflect.DeepEqual(values, full) {
			t.Fatal(values, err)
		}
	})
	t.Run("root_writes_survive_stream_callback_failure", func(t *testing.T) {
		signal := errors.New("failure after autocommitted write")
		id := query.NewFieldRef("id", "id", query.FieldInteger, false)
		var key int64
		err := root.QueryBatches(ctx, plan, 2, func(row db.Row) error { var value int64; return row.Scan(&value) }, func(affinity db.Queryer) (bool, error) {
			var err error
			key, err = affinity.(db.Mutator).Insert(ctx, query.NewInsertPlanReturningKey("batch_writes", []query.Assignment{query.NewAssignment(Value, query.Integer(71))}, id))
			if err != nil {
				return false, err
			}
			return false, signal
		})
		if !errors.Is(err, signal) {
			t.Fatal(err)
		}
		values, err := Read(ctx, root, query.NewPlan("batch_writes", []query.FieldRef{Value}))
		if err != nil || !reflect.DeepEqual(values, []int64{71}) {
			t.Fatal("root stream silently owned all writes", values, err)
		}
		if _, err := root.Delete(ctx, query.NewDeletePlan("batch_writes", id, query.Integer(key))); err != nil {
			t.Fatal(err)
		}
	})
	for _, active := range []bool{true, false} {
		name := "active_mutators"
		if !active {
			name = "retained_mutators"
		}
		t.Run(name, func(t *testing.T) {
			mutate := func(affinity db.Queryer) (bool, error) {
				id := query.NewFieldRef("id", "id", query.FieldInteger, false)
				mutator := affinity.(db.Mutator)
				key, err := mutator.Insert(ctx, query.NewInsertPlanReturningKey("batch_writes", []query.Assignment{query.NewAssignment(Value, query.Integer(81))}, id))
				if err != nil {
					return false, err
				}
				if count, err := mutator.Update(ctx, query.NewUpdatePlan("batch_writes", []query.Assignment{query.NewAssignment(Value, query.Integer(82))}, id, query.Integer(key))); err != nil || count != 1 {
					t.Fatal(count, err)
				}
				conflict := query.NewConflictInsertPlan("batch_writes", []query.Assignment{query.NewAssignment(Value, query.Integer(82))}, []query.FieldRef{Value})
				if inserted, err := affinity.(db.ConflictInserter).InsertOnConflict(ctx, conflict); err != nil || inserted {
					t.Fatal(inserted, err)
				}
				if count, err := mutator.Delete(ctx, query.NewDeletePlan("batch_writes", id, query.Integer(key))); err != nil || count != 1 {
					t.Fatal(count, err)
				}
				return false, nil
			}
			if active {
				if err := root.QueryBatches(ctx, plan, 2, func(row db.Row) error { var v int64; return row.Scan(&v) }, mutate); err != nil {
					t.Fatal(err)
				}
			} else if _, err := mutate(retained); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"scan_error", "scan_panic", "scan_goexit", "yield_panic", "yield_goexit", "cancel_scan", "cancel_yield"} {
		t.Run("root_"+mode, func(t *testing.T) {
			signal := errors.New("authored root interruption")
			callCtx, stop := context.WithCancel(ctx)
			defer stop()
			done := make(chan struct{})
			var result error
			var recovered any
			returned, scans, yields := false, 0, 0
			go func() {
				defer close(done)
				defer func() { recovered = recover() }()
				result = root.QueryBatches(callCtx, plan, 2, func(row db.Row) error {
					scans++
					if scans == 3 {
						switch mode {
						case "scan_error":
							return signal
						case "scan_panic":
							panic(signal)
						case "scan_goexit":
							runtime.Goexit()
						case "cancel_scan":
							stop()
						}
					}
					var value int64
					return row.Scan(&value)
				}, func(affinity db.Queryer) (bool, error) {
					yields++
					if mode == "yield_panic" || mode == "yield_goexit" {
						// The root executor must close this before cursor cleanup.
						if _, err := affinity.Query(ctx, plan); err != nil {
							return false, err
						}
						if mode == "yield_panic" {
							panic(signal)
						}
						runtime.Goexit()
					}
					if mode == "cancel_yield" {
						stop()
					}
					return true, nil
				})
				returned = true
			}()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("root cleanup blocked", ctx.Err())
			}
			switch mode {
			case "scan_error":
				if !returned || !errors.Is(result, signal) {
					t.Fatal(result)
				}
			case "scan_panic", "yield_panic":
				if returned || recovered != signal {
					t.Fatal("panic changed", recovered, result)
				}
			case "scan_goexit", "yield_goexit":
				if returned || recovered != nil {
					t.Fatal("Goexit changed", recovered, result)
				}
			case "cancel_scan", "cancel_yield":
				if !returned || !errors.Is(result, context.Canceled) {
					t.Fatal(result)
				}
			}
			if yields != 1 || scans < 2 || scans > 3 {
				t.Fatalf("unexpected progress %d/%d", scans, yields)
			}
			if values, err := Read(ctx, root, plan); err != nil || !reflect.DeepEqual(values, full) {
				t.Fatal("root connection not reusable", values, err)
			}
		})
	}
	t.Run("callback_rows_expire_before_next_batch", func(t *testing.T) {
		var held db.Rows
		yields := 0
		err := root.QueryBatches(ctx, plan, 2, func(row db.Row) error { var value int64; return row.Scan(&value) }, func(affinity db.Queryer) (bool, error) {
			yields++
			if held != nil && (held.Next() || held.Err() == nil) {
				t.Fatal("previous callback rowset survived its batch")
			}
			var err error
			held, err = affinity.Query(ctx, plan)
			return true, err
		})
		if err != nil || yields != 3 {
			t.Fatal(yields, err)
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("abandoned_callback_rows_close_before_cursor_cleanup", func(t *testing.T) {
		var held db.Rows
		err := root.QueryBatches(ctx, plan, 2, func(row db.Row) error { var v int64; return row.Scan(&v) }, func(affinity db.Queryer) (bool, error) {
			var err error
			held, err = affinity.Query(ctx, plan)
			return false, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if held.Next() || held.Err() == nil {
			t.Fatal("abandoned callback rows remained active")
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		if values, err := Read(ctx, root, plan); err != nil || !reflect.DeepEqual(values, full) {
			t.Fatal("connection not reusable", values, err)
		}
	})
}
