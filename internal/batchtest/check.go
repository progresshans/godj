// Package batchtest checks native batch execution through ordinary public DB
// operations. Expected values are independent of each backend's cursor code.
package batchtest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Owner func(context.Context, func(db.Session) error) error

var Value = query.NewFieldRef("value", "value", query.FieldInteger, false)

func Plan() query.Plan {
	return query.NewPlan("batch_source", []query.FieldRef{Value}).WithOrderings(query.NewOrdering(Value, query.Ascending))
}

func Read(ctx context.Context, backend db.Queryer, plan query.Plan) (values []int64, err error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Err(), rows.Close()) }()
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// Check requires batch_source(value) containing 1,1,2,3,4 and an empty
// batch_writes(id generated primary key, value). idle verifies cursor cleanup.
func Check(t *testing.T, root db.Queryer, owner Owner, idle func(db.Session) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	plan := Plan()
	full := []int64{1, 1, 2, 3, 4}
	checkIdle := func(t *testing.T, session db.Session) {
		t.Helper()
		if idle != nil {
			if err := idle(session); err != nil {
				t.Fatal(err)
			}
		}
		if values, err := Read(ctx, session, plan); err != nil || !reflect.DeepEqual(values, full) {
			t.Fatalf("session unusable after stream: %v %v", values, err)
		}
	}
	for _, size := range []int{1, 2, 3, 8} {
		t.Run(fmt.Sprintf("boundaries_%d", size), func(t *testing.T) {
			err := owner(ctx, func(session db.Session) error {
				stream, ok := session.(db.BatchQueryer)
				if !ok {
					t.Fatal("borrowed session lacks BatchQueryer")
				}
				var batch, got []int64
				var sizes []int
				err := stream.QueryBatches(ctx, plan, size, func(row db.Row) error {
					var value int64
					if err := row.Scan(&value); err != nil {
						return err
					}
					batch = append(batch, value)
					return nil
				}, func(affinity db.Queryer) (bool, error) {
					if affinity != session {
						t.Fatal("borrowed session identity changed")
					}
					values, err := Read(ctx, affinity, plan)
					if err != nil || !reflect.DeepEqual(values, full) {
						t.Fatalf("nested query: %v %v", values, err)
					}
					sizes = append(sizes, len(batch))
					got = append(got, batch...)
					batch = nil
					return true, nil
				})
				if err != nil || !reflect.DeepEqual(got, full) {
					t.Fatalf("batch values: %v %v", got, err)
				}
				var want []int
				for remaining := len(full); remaining > 0; remaining -= size {
					want = append(want, min(size, remaining))
				}
				if !reflect.DeepEqual(sizes, want) {
					t.Fatalf("batch sizes: %v want %v", sizes, want)
				}
				checkIdle(t, session)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("slice_nested_and_empty", func(t *testing.T) {
		err := owner(ctx, func(session db.Session) error {
			stream := session.(db.BatchQueryer)
			sliced := plan.WithOrderings(query.NewOrdering(Value, query.Descending))
			sliced, _ = sliced.WithOffset(1)
			sliced, _ = sliced.WithLimit(3)
			var got []int64
			yields := 0
			err := stream.QueryBatches(ctx, sliced, 2, func(row db.Row) error {
				var value int64
				if err := row.Scan(&value); err != nil {
					return err
				}
				got = append(got, value)
				return nil
			}, func(db.Queryer) (bool, error) {
				yields++
				var nested []int64
				err := stream.QueryBatches(ctx, plan, 3, func(row db.Row) error {
					var value int64
					if err := row.Scan(&value); err != nil {
						return err
					}
					nested = append(nested, value)
					return nil
				}, func(db.Queryer) (bool, error) { return true, nil })
				if err != nil || !reflect.DeepEqual(nested, full) {
					t.Fatalf("nested stream: %v %v", nested, err)
				}
				return true, nil
			})
			if err != nil || yields != 2 || !reflect.DeepEqual(got, []int64{3, 2, 1}) {
				t.Fatalf("slice: %v %d %v", got, yields, err)
			}
			empty, _ := plan.WithLimit(0)
			unexpectedScan := func(db.Row) error { t.Fatal("empty scan"); return nil }
			unexpectedYield := func(db.Queryer) (bool, error) { t.Fatal("empty yield"); return false, nil }
			if err := stream.QueryBatches(ctx, empty, 2, unexpectedScan, unexpectedYield); err != nil {
				return err
			}
			empty, _ = plan.WithConditions(query.NewCondition(Value, query.LookupGreaterThan, query.Integer(99)))
			if err := stream.QueryBatches(ctx, empty, 2, unexpectedScan, unexpectedYield); err != nil {
				return err
			}
			// A proven-empty source still has its aggregate COUNT row.
			empty, _ = plan.WithLimit(0)
			shape, _ := query.NewAggregateResult(query.CountAllResult())
			aggregate, _ := empty.WithResultShape(shape)
			counts, batches := 0, 0
			if err := stream.QueryBatches(ctx, aggregate, 2, func(row db.Row) error {
				var count int64
				if err := row.Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					t.Fatal("empty aggregate count", count)
				}
				counts++
				return nil
			}, func(db.Queryer) (bool, error) { batches++; return true, nil }); err != nil || counts != 1 || batches != 1 {
				t.Fatalf("empty aggregate: %d %d %v", counts, batches, err)
			}
			checkIdle(t, session)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	for _, failure := range []string{"stop", "scan_error", "yield_error", "scan_panic", "yield_panic", "cancel_yield"} {
		t.Run(failure, func(t *testing.T) {
			err := owner(ctx, func(session db.Session) error {
				stream := session.(db.BatchQueryer)
				callCtx, stop := context.WithCancel(ctx)
				defer stop()
				signal := errors.New("authored callback failure")
				scans, yields := 0, 0
				var streamErr error
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					streamErr = stream.QueryBatches(callCtx, plan, 2, func(row db.Row) error {
						scans++
						if scans == 4 {
							if failure == "scan_error" {
								return signal
							}
							if failure == "scan_panic" {
								panic(signal)
							}
						}
						var value int64
						return row.Scan(&value)
					}, func(db.Queryer) (bool, error) {
						yields++
						if failure == "stop" {
							return false, nil
						}
						if failure == "cancel_yield" {
							stop()
							return true, nil
						}
						if yields == 2 {
							if failure == "yield_error" {
								return false, signal
							}
							if failure == "yield_panic" {
								panic(signal)
							}
						}
						return true, nil
					})
				}()
				wantScans, wantYields := 4, 2
				switch failure {
				case "stop":
					wantScans, wantYields = 2, 1
					if streamErr != nil {
						t.Fatal(streamErr)
					}
				case "cancel_yield":
					wantScans, wantYields = 2, 1
					if !errors.Is(streamErr, context.Canceled) {
						t.Fatal(streamErr)
					}
				case "scan_error":
					wantYields = 1
					if !errors.Is(streamErr, signal) {
						t.Fatal(streamErr)
					}
				case "yield_error":
					if !errors.Is(streamErr, signal) {
						t.Fatal(streamErr)
					}
				case "scan_panic":
					wantYields = 1
					if recovered != signal {
						t.Fatal("lost scan panic", recovered)
					}
				case "yield_panic":
					if recovered != signal {
						t.Fatal("lost yield panic", recovered)
					}
				}
				if scans != wantScans || yields != wantYields {
					t.Fatalf("publication boundary: scans=%d yields=%d", scans, yields)
				}
				checkIdle(t, session)
				return nil // The caller can recover and keep its own transaction.
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("validation_and_expired_session", func(t *testing.T) {
		var retained db.BatchQueryer
		noScan := func(db.Row) error { t.Fatal("invalid request scanned a row"); return nil }
		noYield := func(db.Queryer) (bool, error) { t.Fatal("invalid request yielded a batch"); return false, nil }
		err := owner(ctx, func(session db.Session) error {
			retained = session.(db.BatchQueryer)
			for _, size := range []int{0, -1} {
				if err := retained.QueryBatches(ctx, plan, size, noScan, noYield); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal(err)
				}
			}
			for _, err := range []error{
				retained.QueryBatches(ctx, plan, 2, nil, noYield), retained.QueryBatches(ctx, plan, 2, noScan, nil),
				retained.QueryBatches(ctx, query.Plan{}, 2, noScan, noYield), retained.QueryBatches(nil, plan, 2, noScan, noYield),
			} {
				if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal(err)
				}
			}
			canceled, stop := context.WithCancel(ctx)
			stop()
			if err := retained.QueryBatches(canceled, plan, 2, noScan, noYield); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			checkIdle(t, session)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		empty, _ := plan.WithLimit(0)
		if err := retained.QueryBatches(context.WithoutCancel(ctx), empty, 2, noScan, noYield); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("expired session accepted", err)
		}
	})
	t.Run("parent_cancellation_with_detached_read", func(t *testing.T) {
		parent, stop := context.WithCancel(ctx)
		defer stop()
		detached := context.WithoutCancel(parent)
		scans, yields := 0, 0
		err := owner(parent, func(session db.Session) error {
			return session.(db.BatchQueryer).QueryBatches(detached, plan, 2, func(row db.Row) error {
				scans++
				var value int64
				return row.Scan(&value)
			}, func(db.Queryer) (bool, error) {
				yields++
				stop()
				return true, nil
			})
		})
		if !errors.Is(err, context.Canceled) || scans != 2 || yields != 1 {
			t.Fatalf("detached query bypassed session cancellation: scans=%d yields=%d err=%v", scans, yields, err)
		}
		values, err := Read(ctx, root, plan)
		if err != nil || !reflect.DeepEqual(values, full) {
			t.Fatalf("canceled owner did not release connection: %v %v", values, err)
		}
	})
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("outer_commit_%t", commit), func(t *testing.T) {
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			writes := query.NewPlan("batch_writes", []query.FieldRef{Value})
			signal := errors.New("outer rollback after successful batch write")
			var createdID int64
			err := owner(ctx, func(session db.Session) error {
				err := session.(db.BatchQueryer).QueryBatches(ctx, plan, 2, func(row db.Row) error { var value int64; return row.Scan(&value) }, func(affinity db.Queryer) (bool, error) {
					var err error
					createdID, err = affinity.(db.Session).Insert(ctx, query.NewInsertPlanReturningKey("batch_writes", []query.Assignment{query.NewAssignment(Value, query.Integer(42))}, id))
					return false, err
				})
				if err != nil {
					return err
				}
				checkIdle(t, session)
				values, err := Read(ctx, session, writes)
				if err != nil || !reflect.DeepEqual(values, []int64{42}) {
					t.Fatalf("write not owned by session: %v %v", values, err)
				}
				if !commit {
					return signal
				}
				return nil
			})
			if commit && err != nil || !commit && !errors.Is(err, signal) {
				t.Fatal(err)
			}
			values, err := Read(ctx, root, writes)
			if err != nil || commit && !reflect.DeepEqual(values, []int64{42}) || !commit && len(values) != 0 {
				t.Fatalf("outer transaction lost ownership: %v %v", values, err)
			}
			if commit {
				if _, err := root.(db.Mutator).Delete(ctx, query.NewDeletePlan("batch_writes", id, query.Integer(createdID))); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, duringScan := range []bool{false, true} {
		t.Run(fmt.Sprintf("goexit_scan_%t", duringScan), func(t *testing.T) {
			done := make(chan struct{})
			var returned bool
			var outcome error
			var retained db.Session
			go func() {
				defer close(done)
				outcome = owner(ctx, func(session db.Session) error {
					retained = session
					id := query.NewFieldRef("id", "id", query.FieldInteger, false)
					if _, err := session.Insert(ctx, query.NewInsertPlanReturningKey("batch_writes", []query.Assignment{query.NewAssignment(Value, query.Integer(99))}, id)); err != nil {
						return err
					}
					return session.(db.BatchQueryer).QueryBatches(ctx, plan, 2, func(row db.Row) error {
						if duringScan {
							runtime.Goexit()
						}
						var value int64
						return row.Scan(&value)
					}, func(db.Queryer) (bool, error) { runtime.Goexit(); return false, nil })
				})
				returned = true
			}()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("Goexit cleanup blocked", ctx.Err())
			}
			if returned || outcome != nil || retained == nil {
				t.Fatal("Goexit did not propagate", returned, outcome)
			}
			if err := retained.(db.SessionValidator).ValidateSession(ctx); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("Goexit left session active", err)
			}
			values, err := Read(ctx, root, query.NewPlan("batch_writes", []query.FieldRef{Value}))
			if err != nil || len(values) != 0 {
				t.Fatalf("Goexit leaked writes or connection: %v %v", values, err)
			}
		})
	}
}
