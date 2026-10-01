package bulkinserttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Backend interface {
	db.BulkInserter
	db.Atomic
	db.RelationAtomic
	db.CoordinatedAtomic
	db.CoordinatedRelationAtomic
	db.SnapshotReader
	db.BatchQueryer
}

type stored struct {
	ID, Amount int64
	Name       string
	Parent     sql.NullInt64
	Note       sql.NullString
}

// Check expects bulk_items with an auto key, unique non-null name, nonnegative
// amount, nullable parent_id FK and note; parent 1 and auto-key-only bulk_auto.
func Check(t *testing.T, backend Backend, database *sql.DB, table, autoTable string, postgres bool) {
	t.Helper()
	ctx := t.Context()
	snapshot := func() []stored {
		rows, err := database.QueryContext(ctx, "SELECT id,name,amount,parent_id,note FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := make([]stored, 0)
		for rows.Next() {
			var value stored
			if err := rows.Scan(&value.ID, &value.Name, &value.Amount, &value.Parent, &value.Note); err != nil {
				t.Fatal(err)
			}
			result = append(result, value)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return result
	}
	reset := func() {
		if _, err := database.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(rows [][]query.Value, mode query.BulkConflictMode) db.BulkInsertResult {
		value, err := backend.BulkInsert(ctx, Plan(t, rows, Policy(t, mode)))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	t.Run("context_precedes_invalid_plan", func(t *testing.T) {
		before := snapshot()
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if result, err := backend.BulkInsert(canceled, query.BulkInsertPlan{}); !errors.Is(err, context.Canceled) || len(result.Keys) != 0 || result.RowsAffected != 0 {
			t.Fatal("canceled insert", result, err)
		}
		if _, err := backend.BulkInsertLimits(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled limits", err)
		}
		if _, err := backend.BulkInsertLimits(nil); err == nil {
			t.Fatal("nil-context limits admitted")
		}
		if _, err := backend.BulkInsert(nil, query.BulkInsertPlan{}); err == nil {
			t.Fatal("nil-context insert admitted")
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("rejected context changed rows")
		}
	})
	t.Run("ordered_returning_and_nullable_values", func(t *testing.T) {
		reset()
		first := Row("second lexically", 9223372036854775807)
		first[2], first[3] = query.Null(), query.String("literal ' and <text>")
		value := insert([][]query.Value{first, Row("first lexically", 2)}, query.BulkConflictError)
		rows := snapshot()
		if value.RowsAffected != 2 || len(value.Keys) != 2 || len(rows) != 2 || rows[0].ID != value.Keys[0] || rows[1].ID != value.Keys[1] || rows[0].Name != "second lexically" || rows[0].Amount != 9223372036854775807 || rows[0].Parent.Valid || rows[0].Note.String != "literal ' and <text>" || !rows[1].Parent.Valid || rows[1].Note.Valid {
			t.Fatal(value, rows)
		}
		value.Keys[0] = -555
		if !reflect.DeepEqual(snapshot(), rows) {
			t.Fatal("result mutation reached stored rows")
		}
	})
	t.Run("explicit_keys_remain_exact", func(t *testing.T) {
		reset()
		keys := []int64{9007199254740993, 0, -9}
		rows := make([][]query.Value, len(keys))
		for index, key := range keys {
			rows[index] = append([]query.Value{query.Integer(key)}, Row(fmt.Sprint("explicit", index), int64(index))...)
		}
		plan, err := query.NewBulkInsertPlan("bulk_items", []query.FieldRef{ID, Name, Amount, Parent, Note}, rows, ID, query.BulkConflict{})
		if err != nil {
			t.Fatal(err)
		}
		value, err := backend.BulkInsert(ctx, plan)
		if err != nil || !slices.Equal(value.Keys, keys) || value.RowsAffected != 3 || len(snapshot()) != 3 {
			t.Fatal(value, err)
		}
	})
	t.Run("auto_only_multiple_rows", func(t *testing.T) {
		plan, err := query.NewBulkInsertPlan("bulk_auto", nil, [][]query.Value{nil, nil, nil}, ID, query.BulkConflict{})
		if err != nil {
			t.Fatal(err)
		}
		value, err := backend.BulkInsert(ctx, plan)
		if err != nil || value.RowsAffected != 3 || len(value.Keys) != 3 || value.Keys[0] == value.Keys[1] || value.Keys[1] == value.Keys[2] {
			t.Fatal(value, err)
		}
		var count int
		if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+autoTable).Scan(&count); err != nil || count != 3 {
			t.Fatal(count, err)
		}
	})
	t.Run("ignore_unique_preserves_existing", func(t *testing.T) {
		reset()
		original := insert([][]query.Value{Row("existing", 3)}, query.BulkConflictError)
		value := insert([][]query.Value{Row("existing", 8), Row("new", 9)}, query.BulkConflictIgnore)
		rows := snapshot()
		if value.RowsAffected != 1 || len(value.Keys) != 0 || len(rows) != 2 || rows[0].ID != original.Keys[0] || rows[0].Amount != 3 || rows[1].Name != "new" {
			t.Fatal(value, rows)
		}
	})
	for _, mode := range []query.BulkConflictMode{query.BulkConflictError, query.BulkConflictIgnore, query.BulkConflictUpdate} {
		t.Run("integrity_failure_"+string(mode), func(t *testing.T) {
			for _, failure := range []string{"check", "foreign_key", "required_null", "invalid_type"} {
				t.Run(failure, func(t *testing.T) {
					reset()
					rows := [][]query.Value{Row("would-create", 1), Row("bad", -1)}
					switch failure {
					case "foreign_key":
						rows[1] = Row("bad", 1)
						rows[1][2] = query.Integer(99999)
					case "required_null":
						rows[1] = Row("bad", 1)
						rows[1][0] = query.Null()
					case "invalid_type":
						rows[1][1] = query.String("1")
					}
					var result db.BulkInsertResult
					err := backend.Atomic(ctx, func(session db.Session) error {
						var err error
						result, err = session.(db.BulkInserter).BulkInsert(ctx, Plan(t, rows, Policy(t, mode)))
						return err
					})
					if err == nil || len(snapshot()) != 0 {
						t.Fatal("integrity failure left partial writes", result, err, snapshot())
					}
				})
			}
		})
	}
	t.Run("update_conflict_selected_columns", func(t *testing.T) {
		reset()
		seed := Row("existing", 3)
		seed[3] = query.String("retained")
		original := insert([][]query.Value{seed}, query.BulkConflictError)
		incoming := Row("existing", 8)
		incoming[3] = query.String("not-selected")
		value := insert([][]query.Value{incoming, Row("new", 9)}, query.BulkConflictUpdate)
		rows := snapshot()
		if value.RowsAffected != 2 || len(value.Keys) != 2 || value.Keys[0] != original.Keys[0] || len(rows) != 2 || rows[0].Amount != 8 || rows[0].Note.String != "retained" {
			t.Fatal(value, rows)
		}
	})
	t.Run("short_returning_rolls_back_all_rows", func(t *testing.T) {
		reset()
		var result db.BulkInsertResult
		err := backend.Atomic(ctx, func(session db.Session) error {
			var err error
			result, err = session.(db.BulkInserter).BulkInsert(ctx, Plan(t, [][]query.Value{Row("would-create", 1), Row("skip-native", 2)}, query.BulkConflict{}))
			return err
		})
		if !errors.Is(err, &query.Error{Code: query.CodeUnexpectedRows}) || len(result.Keys) != 0 || result.RowsAffected != 0 || len(snapshot()) != 0 {
			t.Fatal("incomplete native RETURNING became partial success", result, err, snapshot())
		}
	})
	t.Run("duplicate_conflict_keys_follow_native_statement", func(t *testing.T) {
		reset()
		value, err := backend.BulkInsert(ctx, Plan(t, [][]query.Value{Row("same", 1), Row("same", 2)}, Policy(t, query.BulkConflictUpdate)))
		rows := snapshot()
		if postgres {
			if err == nil || len(value.Keys) != 0 || len(rows) != 0 {
				t.Fatal("PostgreSQL duplicate upsert did not fail atomically", value, err, rows)
			}
		} else if err != nil || value.RowsAffected != 2 || len(value.Keys) != 2 || value.Keys[0] != value.Keys[1] || len(rows) != 1 || rows[0].Amount != 2 {
			t.Fatal("SQLite duplicate upsert result", value, err, rows)
		}
	})
	t.Run("parameter_batch_exceeds_legacy_999_limit", func(t *testing.T) {
		reset()
		rows := make([][]query.Value, 1100)
		for index := range rows {
			rows[index] = Row(fmt.Sprint("batch-", index), int64(index))
		}
		value := insert(rows, query.BulkConflictError)
		if value.RowsAffected != int64(len(rows)) || len(value.Keys) != len(rows) || len(snapshot()) != len(rows) {
			t.Fatal("native multi-row batch was incomplete")
		}
	})
	owners := map[string]func(context.Context, func(db.Session) error) error{
		"ordinary": backend.Atomic, "coordinated": backend.CoordinatedAtomic,
		"relation": func(ctx context.Context, callback func(db.Session) error) error {
			return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		},
		"coordinated_relation": func(ctx context.Context, callback func(db.Session) error) error {
			return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		},
	}
	for name, atomic := range owners {
		t.Run("scope_"+name, func(t *testing.T) {
			reset()
			rollback := errors.New("rollback authored multi-batch operation")
			var parent, child db.BulkInserter
			err := atomic(ctx, func(session db.Session) error {
				parent = session.(db.BulkInserter)
				limits, err := parent.BulkInsertLimits(ctx)
				if err != nil || limits.Parameters <= 0 || limits.Rows <= 0 {
					t.Fatal(limits, err)
				}
				if _, err := parent.BulkInsert(ctx, Plan(t, [][]query.Value{Row("first-batch", 1)}, query.BulkConflict{})); err != nil {
					return err
				}
				if err := db.WithSavepoint(ctx, session, func(inner db.Session) error {
					child = inner.(db.BulkInserter)
					if _, err := parent.BulkInsertLimits(ctx); err == nil {
						t.Fatal("parent usable while child scope active")
					}
					_, err := child.BulkInsert(ctx, Plan(t, [][]query.Value{Row("child", 2)}, query.BulkConflict{}))
					return err
				}); err != nil {
					return err
				}
				if _, err := child.BulkInsertLimits(ctx); err == nil {
					t.Fatal("expired child accepted a capability read")
				}
				return rollback
			})
			if !errors.Is(err, rollback) || len(snapshot()) != 0 {
				t.Fatal("owned rollback did not undo all batches", err, snapshot())
			}
			if _, err := parent.BulkInsert(context.Background(), Plan(t, [][]query.Value{Row("late", 1)}, query.BulkConflict{})); err == nil {
				t.Fatal("expired parent accepted a write")
			}
		})
	}
	t.Run("snapshot_never_advertises_bulk_write", func(t *testing.T) {
		if err := backend.ReadSnapshot(ctx, func(session db.Queryer) error {
			if _, ok := session.(db.BulkInserter); ok {
				t.Fatal("read-only snapshot advertises writes")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("root_cursor_supplies_its_native_write_capability", func(t *testing.T) {
		reset()
		insert([][]query.Value{Row("cursor", 1)}, query.BulkConflictError)
		plan := query.NewPlan("bulk_items", []query.FieldRef{ID})
		calls := 0
		err := backend.QueryBatches(ctx, plan, 1, func(row db.Row) error { var id int64; return row.Scan(&id) }, func(session db.Queryer) (bool, error) {
			calls++
			capability, ok := session.(db.BulkInserter)
			if !ok {
				t.Fatal("root cursor lost native bulk capability")
			}
			if _, err := capability.BulkInsertLimits(ctx); err != nil {
				return false, err
			}
			auto, err := query.NewBulkInsertPlan("bulk_auto", nil, [][]query.Value{nil, nil}, ID, query.BulkConflict{})
			if err != nil {
				return false, err
			}
			result, err := capability.BulkInsert(ctx, auto)
			if err == nil && (result.RowsAffected != 2 || len(result.Keys) != 2) {
				t.Fatal(result)
			}
			return true, err
		})
		if err != nil || calls != 1 {
			t.Fatal(calls, err)
		}
	})
}
