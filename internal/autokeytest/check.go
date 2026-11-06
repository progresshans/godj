// Package autokeytest checks the native reservation contract through public
// query/session APIs. Backend fixtures create their own physical allocators.
package autokeytest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var Key = query.NewFieldRef("key", "record_key", query.FieldInteger, false)
var Token = query.NewFieldRef("token", "token", query.FieldInteger, false)

type Backend interface {
	db.Mutator
	db.Queryer
	db.Atomic
	db.RelationAtomic
	db.CoordinatedAtomic
	db.CoordinatedRelationAtomic
	db.SnapshotReader
}

func Plan(t *testing.T, count int) query.AutoKeyPlan {
	t.Helper()
	plan, err := query.NewAutoKeyPlan("reserved_keys", Key, count)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func Insert(t *testing.T, session db.Session, keys, tokens []int64) (db.BulkInsertResult, error) {
	t.Helper()
	rows := make([][]query.Value, len(keys))
	for index, key := range keys {
		rows[index] = []query.Value{query.Integer(key), query.Integer(tokens[index])}
	}
	plan, err := query.NewBulkInsertPlan("reserved_keys", []query.FieldRef{Key, Token}, rows, Key, query.BulkConflict{})
	if err != nil {
		t.Fatal(err)
	}
	return session.(db.BulkInserter).BulkInsert(t.Context(), plan)
}

func read(t *testing.T, source db.Queryer) [][2]int64 {
	t.Helper()
	rows, err := source.Query(t.Context(), query.NewPlan("reserved_keys", []query.FieldRef{Key, Token}))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [][2]int64
	for rows.Next() {
		var row [2]int64
		if err := rows.Scan(&row[0], &row[1]); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(result, func(a, b [2]int64) int {
		if a[0] < b[0] {
			return -1
		}
		if a[0] > b[0] {
			return 1
		}
		return 0
	})
	return result
}

// Check expects reserved_keys(record_key native AutoField, token unique > 0),
// and two independently opened backends on that same database and namespace.
func Check(t *testing.T, backend, second Backend, database *sql.DB, table string, postgres bool) {
	t.Helper()
	ctx := t.Context()
	reset := func() {
		if _, err := database.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("capability_is_borrowed_write_only", func(t *testing.T) {
		if _, ok := backend.(db.AutoKeyAllocator); ok {
			t.Fatal("root backend advertises provisional reservation")
		}
		if err := backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
			if _, ok := reader.(db.AutoKeyAllocator); ok {
				t.Fatal("read scope advertises reservation")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	owners := []struct {
		name string
		run  func(context.Context, func(db.Session) error) error
	}{
		{"ordinary", backend.Atomic}, {"coordinated", backend.CoordinatedAtomic},
		{"relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
		{"coordinated_relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
	}
	for _, owner := range owners {
		t.Run("scope_"+owner.name, func(t *testing.T) {
			reset()
			var parent, child db.AutoKeyAllocator
			var expected [][2]int64
			if err := owner.run(ctx, func(session db.Session) error {
				parent = session.(db.AutoKeyAllocator)
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if keys, err := parent.ReserveAutoKeys(canceled, query.AutoKeyPlan{}); keys != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation must precede invalid plan", keys, err)
				}
				if keys, err := parent.ReserveAutoKeys(nil, Plan(t, 1)); keys != nil || err == nil {
					t.Fatal("nil context accepted", keys, err)
				}
				if keys, err := parent.ReserveAutoKeys(ctx, query.AutoKeyPlan{}); keys != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("zero plan accepted", keys, err)
				}
				keys, err := parent.ReserveAutoKeys(ctx, Plan(t, 3))
				if err != nil || len(keys) != 3 || keys[0] == keys[1] || keys[1] == keys[2] || keys[0] == keys[2] || len(read(t, session)) != 0 {
					t.Fatal("reservation inserted rows or lost keys", keys, err)
				}
				inserted, err := Insert(t, session, keys, []int64{1, 2, 3})
				if err != nil || inserted.RowsAffected != 3 || !slices.Equal(inserted.Keys, keys) {
					t.Fatal("reserved keys differ from actual native INSERT", inserted, keys, err)
				}
				for index, key := range keys {
					expected = append(expected, [2]int64{key, int64(index + 1)})
				}
				ordinary, err := session.Insert(ctx, query.NewInsertPlanReturningKey("reserved_keys", []query.Assignment{query.NewAssignment(Token, query.Integer(4))}, Key))
				if err != nil || slices.Contains(keys, ordinary) {
					t.Fatal("ordinary INSERT reused a reserved key", ordinary, err)
				}
				expected = append(expected, [2]int64{ordinary, 4})
				before := read(t, session)
				rollback := errors.New("rollback reserved child")
				var childKeys []int64
				err = db.WithSavepoint(ctx, session, func(inner db.Session) error {
					child = inner.(db.AutoKeyAllocator)
					if keys, err := parent.ReserveAutoKeys(ctx, Plan(t, 1)); keys != nil || err == nil {
						t.Fatal("parent usable while child owns transaction", keys, err)
					}
					var err error
					childKeys, err = child.ReserveAutoKeys(ctx, Plan(t, 2))
					if err != nil {
						return err
					}
					if _, err := Insert(t, inner, childKeys[:1], []int64{5}); err != nil {
						return err
					}
					return rollback
				})
				if !errors.Is(err, rollback) || !reflect.DeepEqual(before, read(t, session)) {
					t.Fatal("savepoint failed to roll back inserted rows", err)
				}
				if keys, err := child.ReserveAutoKeys(ctx, Plan(t, 1)); keys != nil || err == nil {
					t.Fatal("expired child allocated", keys, err)
				}
				after, err := parent.ReserveAutoKeys(ctx, Plan(t, 1))
				want := childKeys[0]
				if postgres {
					want = childKeys[len(childKeys)-1] + 1
				}
				if err != nil || len(after) != 1 || after[0] != want {
					t.Fatal("native savepoint allocation semantics changed", after, want, err)
				}
				cursor, err := session.Query(ctx, query.NewPlan("reserved_keys", []query.FieldRef{Key}))
				if err != nil {
					return err
				}
				if err := db.WithSavepoint(ctx, session, func(db.Session) error { t.Fatal("open cursor crossed a savepoint boundary"); return nil }); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("open cursor lifetime was not preserved", err)
				}
				if err := cursor.Close(); err != nil {
					return err
				}
				_, err = parent.ReserveAutoKeys(ctx, Plan(t, 1))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if keys, err := parent.ReserveAutoKeys(context.Background(), Plan(t, 1)); keys != nil || err == nil {
				t.Fatal("expired root allocated", keys, err)
			}
			if !reflect.DeepEqual(read(t, backend), expected) {
				t.Fatal("committed rows changed", read(t, backend), expected)
			}
		})
	}
	t.Run("root_rollback_preserves_native_gaps", func(t *testing.T) {
		reset()
		rollback := errors.New("rollback whole reservation")
		var reserved []int64
		err := backend.CoordinatedAtomic(ctx, func(session db.Session) error {
			var err error
			reserved, err = session.(db.AutoKeyAllocator).ReserveAutoKeys(ctx, Plan(t, 3))
			if err != nil {
				return err
			}
			if _, err := Insert(t, session, reserved[:1], []int64{1}); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) || len(read(t, backend)) != 0 {
			t.Fatal("root rollback left rows", err)
		}
		key, err := backend.Insert(ctx, query.NewInsertPlanReturningKey("reserved_keys", []query.Assignment{query.NewAssignment(Token, query.Integer(2))}, Key))
		want := reserved[0]
		if postgres {
			want = reserved[2] + 1
		}
		if err != nil || key != want {
			t.Fatal("native rollback allocator semantics changed", key, want, err)
		}
	})
	t.Run("native_constraint_failure_rolls_back", func(t *testing.T) {
		reset()
		err := backend.Atomic(ctx, func(session db.Session) error {
			keys, err := session.(db.AutoKeyAllocator).ReserveAutoKeys(ctx, Plan(t, 2))
			if err != nil {
				return err
			}
			result, err := Insert(t, session, keys, []int64{1, 1})
			if len(result.Keys) != 0 || result.RowsAffected != 0 {
				t.Fatal("failed INSERT published partial evidence", result)
			}
			return err
		})
		if !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) || len(read(t, backend)) != 0 {
			t.Fatal("reservation bypassed native uniqueness or rollback", err)
		}
	})
	t.Run("maximum_batch_is_complete_and_owned", func(t *testing.T) {
		reset()
		if err := backend.Atomic(ctx, func(session db.Session) error {
			allocator := session.(db.AutoKeyAllocator)
			keys, err := allocator.ReserveAutoKeys(ctx, Plan(t, query.MaximumAutoKeyReservations))
			if err != nil || len(keys) != query.MaximumAutoKeyReservations {
				t.Fatal("bounded reservation truncated", len(keys), err)
			}
			for index := 1; index < len(keys); index++ {
				if keys[index] != keys[index-1]+1 {
					t.Fatal("native batch is incomplete", index)
				}
			}
			end := keys[len(keys)-1]
			keys[len(keys)-1] = -99
			next, err := allocator.ReserveAutoKeys(ctx, Plan(t, 1))
			if err != nil || len(next) != 1 || next[0] != end+1 || len(read(t, session)) != 0 {
				t.Fatal("result ownership or allocator endpoint changed", next, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("independent_connections_reserve_disjoint_keys", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		type result struct {
			keys []int64
			err  error
		}
		start, completed := make(chan struct{}), make(chan result, 4)
		plan := Plan(t, 8)
		for index := range 4 {
			owner := backend
			if index%2 != 0 {
				owner = second
			}
			go func() {
				<-start
				var keys []int64
				err := owner.CoordinatedAtomic(ctx, func(session db.Session) error {
					var err error
					keys, err = session.(db.AutoKeyAllocator).ReserveAutoKeys(ctx, plan)
					return err
				})
				completed <- result{keys, err}
			}()
		}
		close(start)
		seen := make(map[int64]bool)
		var failures []error
		for range 4 {
			value := <-completed
			if value.err != nil || len(value.keys) != 8 {
				failures = append(failures, fmt.Errorf("reserve independent batch: keys=%v error=%w", value.keys, value.err))
			}
			for _, key := range value.keys {
				if seen[key] {
					failures = append(failures, fmt.Errorf("key %d was allocated twice", key))
				}
				seen[key] = true
			}
		}
		if len(failures) != 0 || len(seen) != 32 || len(read(t, backend)) != 0 {
			t.Fatal("independent allocator owners diverged", errors.Join(failures...))
		}
	})
}

// CheckInFlightReservation blocks at the native allocator statement, after
// metadata reads have completed. A live rowset alone is not a scope operation;
// the exclusion contract covers actual I/O, including the whole reservation.
func CheckInFlightReservation(t *testing.T, session db.Session, entered <-chan struct{}, resume func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	finished := make(chan struct{})
	var keys []int64
	var reservationErr error
	plan := Plan(t, 2)
	go func() {
		defer close(finished)
		keys, reservationErr = session.(db.AutoKeyAllocator).ReserveAutoKeys(ctx, plan)
	}()
	defer func() {
		resume()
		<-finished
	}()
	select {
	case <-entered:
	case <-finished:
		t.Fatal("reservation finished before the native I/O checkpoint", reservationErr)
	case <-ctx.Done():
		t.Fatal("reservation did not enter native I/O", ctx.Err())
	}
	insert := query.NewInsertPlanReturningKey("reserved_keys", []query.Assignment{query.NewAssignment(Token, query.Integer(1))}, Key)
	if key, err := session.Insert(ctx, insert); key != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("another write overlapped native allocation", key, err)
	}
	rows, err := session.Query(ctx, query.NewPlan("reserved_keys", []query.FieldRef{Key}))
	if rows != nil {
		resume()
		_ = rows.Close()
	}
	if rows != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("another query overlapped native allocation", err)
	}
	if err := db.WithSavepoint(ctx, session, func(db.Session) error { t.Fatal("savepoint entered during native allocation"); return nil }); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("savepoint overlapped native allocation", err)
	}
	resume()
	<-finished
	if reservationErr != nil || len(keys) != 2 || keys[0] == keys[1] || len(read(t, session)) != 0 {
		t.Fatal("serialized native reservation lost its result", keys, reservationErr)
	}
	// The identical valid write must execute once the reservation releases its
	// operation. An invalid plan or permanently unusable session cannot satisfy
	// only the rejection controls above.
	key, err := session.Insert(ctx, insert)
	if err != nil || slices.Contains(keys, key) || !reflect.DeepEqual(read(t, session), [][2]int64{{key, 1}}) {
		t.Fatal("session did not resume after native allocation", key, err)
	}
}
