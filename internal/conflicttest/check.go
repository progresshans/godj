// Package conflicttest exercises the same native insert and transaction
// contract on independently provisioned SQLite and PostgreSQL databases.
package conflicttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Backend interface {
	db.ConflictInserter
	db.Atomic
	db.RelationAtomic
	db.CoordinatedAtomic
	db.CoordinatedRelationAtomic
}

func Plan(owner, label, token int64) query.ConflictInsertPlan {
	source := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
	target := query.NewFieldRef("label", "label_id", query.FieldInteger, false)
	payload := query.NewFieldRef("token", "token", query.FieldInteger, false)
	return query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("conflict_link", []query.Assignment{
		query.NewAssignment(source, query.Integer(owner)), query.NewAssignment(target, query.Integer(label)),
		query.NewAssignment(payload, query.Integer(token)),
	}, query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{source, target})
}

// Check uses raw SQL reads as an independent oracle. The caller provisions six
// parents and a link table: ordered owner/label UNIQUE, token UNIQUE and > 0,
// owner FK deferred and label FK immediate. second owns another connection pool.
func Check(t *testing.T, backend Backend, second db.ConflictInserter, database *sql.DB, table, parent string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	snapshot := func(t *testing.T) [][4]int64 {
		t.Helper()
		rows, err := database.QueryContext(ctx, "SELECT id,owner_id,label_id,token FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result [][4]int64
		for rows.Next() {
			var row [4]int64
			if err := rows.Scan(&row[0], &row[1], &row[2], &row[3]); err != nil {
				t.Fatal(err)
			}
			result = append(result, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return result
	}
	reset := func(t *testing.T) [][4]int64 {
		t.Helper()
		if _, err := database.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
		inserted, err := backend.InsertOnConflict(ctx, Plan(1, 1, 10))
		if err != nil || !inserted.Inserted {
			t.Fatal("seed", inserted, err)
		}
		rows := snapshot(t)
		if len(rows) != 1 || rows[0][0] != inserted.Key {
			t.Fatal("returned key differs from native seed", inserted, rows)
		}
		return rows
	}
	unchanged := func(t *testing.T, before [][4]int64) {
		t.Helper()
		if after := snapshot(t); !reflect.DeepEqual(before, after) {
			t.Fatalf("rows changed: %v -> %v", before, after)
		}
		var count int
		if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+parent).Scan(&count); err != nil || count != 6 {
			t.Fatal("endpoints changed", count, err)
		}
	}
	t.Run("duplicate_preserves_identity_payload_and_transaction", func(t *testing.T) {
		before := reset(t)
		if inserted, err := backend.InsertOnConflict(ctx, Plan(1, 1, 99)); err != nil || inserted != (db.ConflictInsertResult{}) {
			t.Fatal(inserted, err)
		}
		unchanged(t, before)
		err := backend.Atomic(ctx, func(session db.Session) error {
			writer, ok := session.(db.ConflictInserter)
			if !ok {
				return errors.New("transaction lacks conflict insertion")
			}
			if inserted, err := writer.InsertOnConflict(ctx, Plan(1, 1, 99)); err != nil || inserted != (db.ConflictInsertResult{}) {
				return fmt.Errorf("duplicate: %v %w", inserted, err)
			}
			if inserted, err := writer.InsertOnConflict(ctx, Plan(2, 2, 20)); err != nil || !inserted.Inserted {
				return fmt.Errorf("following insert: %v %w", inserted, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		after := snapshot(t)
		if len(after) != 2 || after[0] != before[0] {
			t.Fatal("duplicate aborted or replaced row", after)
		}
	})
	for _, test := range []struct {
		name   string
		plan   query.ConflictInsertPlan
		unique bool
	}{
		{"other_unique", Plan(2, 2, 10), true},
		{"foreign_key", Plan(1, 999, 20), false},
		{"check", Plan(2, 2, -1), false},
		{"not_null", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("conflict_link", Plan(2, 2, 20).Assignments()[:2], query.NewFieldRef("id", "id", query.FieldInteger, false)), Plan(2, 2, 20).Target()), false},
		{"unowned_target", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("conflict_link", Plan(2, 2, 20).Assignments(), query.NewFieldRef("id", "id", query.FieldInteger, false)), Plan(2, 2, 20).Target()[:1]), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := reset(t)
			inserted, err := backend.InsertOnConflict(ctx, test.plan)
			if inserted != (db.ConflictInsertResult{}) || err == nil {
				t.Fatal("native error suppressed", inserted, err)
			}
			if test.unique && !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
				t.Fatal("unique cause lost", err)
			}
			unchanged(t, before)
		})
	}
	t.Run("two_connection_duplicate", func(t *testing.T) {
		reset(t)
		ready := make(chan struct{})
		results := make([]db.ConflictInsertResult, 2)
		errors := make([]error, 2)
		var done sync.WaitGroup
		for index, writer := range []db.ConflictInserter{backend, second} {
			done.Add(1)
			go func() {
				defer done.Done()
				<-ready
				results[index], errors[index] = writer.InsertOnConflict(ctx, Plan(2, 2, 20))
			}()
		}
		close(ready)
		done.Wait()
		if errors[0] != nil || errors[1] != nil || results[0].Inserted == results[1].Inserted {
			t.Fatal("concurrent outcomes", results, errors)
		}
		after := snapshot(t)
		if len(after) != 2 || after[1][1] != 2 || after[1][2] != 2 {
			t.Fatal("concurrent rows", after)
		}
		for _, result := range results {
			if result.Inserted {
				if result.Key != after[1][0] {
					t.Fatal("insert winner key differs", result, after)
				}
			} else if result != (db.ConflictInsertResult{}) {
				t.Fatal("conflict returned existing key", result)
			}
		}
	})
	t.Run("canceled_and_nil_context", func(t *testing.T) {
		before := reset(t)
		canceled, stop := context.WithCancel(ctx)
		stop()
		if inserted, err := backend.InsertOnConflict(canceled, Plan(2, 2, 20)); inserted != (db.ConflictInsertResult{}) || !errors.Is(err, context.Canceled) {
			t.Fatal(inserted, err)
		}
		if inserted, err := backend.InsertOnConflict(nil, Plan(2, 2, 20)); inserted != (db.ConflictInsertResult{}) || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal(inserted, err)
		}
		unchanged(t, before)
	})
	for _, mode := range []struct {
		name   string
		atomic func(context.Context, func(db.Session) error) error
	}{
		{"ordinary", backend.Atomic}, {"coordinated", backend.CoordinatedAtomic},
		{"relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
		{"coordinated_relation", func(ctx context.Context, callback func(db.Session) error) error {
			return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, outcome := range []string{"commit", "late_unique", "cancel", "deferred_commit"} {
				t.Run(outcome, func(t *testing.T) {
					before := reset(t)
					transactionContext, stop := context.WithCancel(ctx)
					defer stop()
					var retained db.ConflictInserter
					var provisional db.ConflictInsertResult
					calls := 0
					err := mode.atomic(transactionContext, func(session db.Session) error {
						calls++
						var ok bool
						retained, ok = session.(db.ConflictInserter)
						if !ok {
							return errors.New("borrowed session lacks conflict insertion")
						}
						plan := Plan(2, 2, 20)
						if outcome == "deferred_commit" {
							plan = Plan(999, 2, 20)
						}
						inserted, err := retained.InsertOnConflict(transactionContext, plan)
						provisional = inserted
						if !inserted.Inserted || err != nil {
							return fmt.Errorf("initial write: %v %w", inserted, err)
						}
						if outcome == "late_unique" {
							inserted, err := retained.InsertOnConflict(transactionContext, Plan(3, 3, 10))
							if inserted != (db.ConflictInsertResult{}) || !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
								return fmt.Errorf("late unique: %v %w", inserted, err)
							}
							return err
						}
						if outcome == "cancel" {
							stop()
						}
						return nil
					})
					if calls != 1 {
						t.Fatal("callback retried or missing", calls)
					}
					switch outcome {
					case "commit":
						if rows := snapshot(t); err != nil || len(rows) != 2 || rows[1][0] != provisional.Key {
							t.Fatal("commit", err)
						}
					case "late_unique":
						if !errors.Is(err, &query.Error{Code: query.CodeUniqueConstraint}) {
							t.Fatal(err)
						}
					case "cancel":
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
					case "deferred_commit":
						if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
							t.Fatal("unconfirmed commit lost owner", err)
						}
					}
					if outcome != "commit" {
						unchanged(t, before)
					}
					if inserted, err := retained.InsertOnConflict(ctx, Plan(4, 4, 40)); inserted != (db.ConflictInsertResult{}) || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
						t.Fatal("expired session usable", inserted, err)
					}
				})
			}
		})
	}
}

// CheckSignedKeys uses a different physical primary-key name and independently
// reads the stored key. The fixture must not use a generated-always identity.
func CheckSignedKeys(t *testing.T, backend db.ConflictInserter, database *sql.DB, table string) {
	t.Helper()
	key := query.NewFieldRef("identity", "record_key", query.FieldInteger, false)
	token := query.NewFieldRef("token", "token", query.FieldInteger, false)
	for index, want := range []int64{0, -7, 9007199254740993} {
		plan := query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("conflict_signed", []query.Assignment{query.NewAssignment(key, query.Integer(want)), query.NewAssignment(token, query.Integer(int64(index+1)))}, key), []query.FieldRef{token})
		result, err := backend.InsertOnConflict(t.Context(), plan)
		if err != nil || !result.Inserted || result.Key != want {
			t.Fatal("actual signed renamed key", result, err)
		}
		var stored int64
		if err := database.QueryRowContext(t.Context(), "SELECT record_key FROM "+table+" WHERE token="+fmt.Sprint(index+1)).Scan(&stored); err != nil || stored != want {
			t.Fatal("independent stored key", stored, err)
		}
		assignments := plan.Assignments()
		assignments[0] = query.NewAssignment(key, query.Integer(123456))
		result, err = backend.InsertOnConflict(t.Context(), query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("conflict_signed", assignments, key), plan.Target()))
		if err != nil || result != (db.ConflictInsertResult{}) {
			t.Fatal("duplicate reused last/existing key", result, err)
		}
	}
}
