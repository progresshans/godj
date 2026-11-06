package bulkupdatetest

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
	"github.com/progresshans/godj/schema/ir"
)

type Backend interface {
	db.BulkUpdater
	db.Atomic
	db.CoordinatedAtomic
	db.RelationAtomic
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

// Check expects update_items with an integer key, unique name, nonnegative
// amount, deferred FK parent_id and nullable note. Parents and labels are
// deliberately named like the compiler's CTE to exercise name resolution.
func Check(t *testing.T, backend Backend, database *sql.DB, table, links string, postgres bool) {
	t.Helper()
	ctx := t.Context()
	exec := func(statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
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
		exec("DELETE FROM " + links)
		exec("DELETE FROM " + table)
		exec("INSERT INTO " + table + " (id,name,amount,parent_id,note) VALUES (1,'one',1,1,'first'),(2,'two',2,2,NULL),(3,'three',3,NULL,'third')")
		exec("INSERT INTO " + links + " (id,owner_id,label_id) VALUES (1,1,1),(2,1,2),(3,2,1),(4,1,1)")
	}
	update := func(source query.Plan, fields []query.FieldRef, keys []int64, rows [][]query.Value) int64 {
		t.Helper()
		count, err := backend.BulkUpdate(ctx, Plan(t, source, fields, keys, rows))
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	amounts := func() []int64 {
		rows := snapshot()
		result := make([]int64, len(rows))
		for i, row := range rows {
			result[i] = row.Amount
		}
		return result
	}
	checkAmounts := func(want ...int64) {
		t.Helper()
		if got := amounts(); !slices.Equal(got, want) {
			t.Fatal("raw stored amounts", got, want)
		}
	}
	allKeys := []int64{1, 2, 3}
	allRows := [][]query.Value{{query.Integer(11)}, {query.Integer(22)}, {query.Integer(33)}}
	item := ir.ModelIdentity{AppLabel: "app", ModelName: "item"}
	parent := ir.ModelIdentity{AppLabel: "app", ModelName: "parent"}
	link := ir.ModelIdentity{AppLabel: "app", ModelName: "link"}
	label := ir.ModelIdentity{AppLabel: "app", ModelName: "label"}
	forward, err := query.NewForwardRelationPath(item, "update_items", "parent", "parent_id", parent, "godj_bulk_targets", "id", true, Name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := query.NewReverseRelationPath(link, "update_links", "owner", "owner_id", item, "update_items", "id", "labels", false, ID, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.NewForwardRelationPath(link, "update_links", "label", "label_id", label, "godj_bulk_targets_0", "id", false, Name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := query.NewRelationChain(append(reverse.Hops(), target.Hops()...), []query.FieldRef{ID, ID, ID}, Name, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	red := query.NewRelatedCondition(collection, query.LookupExact, query.String("red"))
	blue := query.NewRelatedCondition(collection, query.LookupExact, query.String("blue"))
	allowed := query.NewRelatedCondition(forward, query.LookupExact, query.String("allowed"))

	t.Run("context_precedes_invalid_plan", func(t *testing.T) {
		reset()
		before := snapshot()
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.BulkUpdate(canceled, query.BulkUpdatePlan{}); count != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal(count, err)
		}
		if limit, err := backend.BulkUpdateBatchSize(canceled, query.BulkUpdateSpec{}); limit != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal(limit, err)
		}
		if _, err := backend.BulkUpdateBatchSize(nil, query.BulkUpdateSpec{}); err == nil {
			t.Fatal("nil-context capability accepted")
		}
		if _, err := backend.BulkUpdate(nil, query.BulkUpdatePlan{}); err == nil {
			t.Fatal("nil-context write accepted")
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("rejected context changed rows")
		}
	})
	t.Run("selected_fields_and_all_null_case", func(t *testing.T) {
		reset()
		count := update(Source(), []query.FieldRef{Note, Parent}, []int64{3, 1}, [][]query.Value{{query.Null(), query.Null()}, {query.Null(), query.Null()}})
		rows := snapshot()
		if count != 2 || rows[0].Name != "one" || rows[2].Name != "three" || rows[0].Parent.Valid || rows[2].Parent.Valid || rows[0].Note.Valid || rows[2].Note.Valid {
			t.Fatal(count, rows)
		}
		checkAmounts(1, 2, 3)
	})
	t.Run("duplicate_first_missing_and_unchanged_counts", func(t *testing.T) {
		reset()
		count := update(Source(), []query.FieldRef{Amount}, []int64{2, 1, 2, 999}, [][]query.Value{{query.Integer(2)}, {query.Integer(11)}, {query.Integer(222)}, {query.Integer(999)}})
		if count != 2 {
			t.Fatal(count)
		}
		checkAmounts(11, 2, 3)
		if count := update(Source(), []query.FieldRef{Amount}, []int64{999}, [][]query.Value{{query.Integer(999)}}); count != 0 {
			t.Fatal("missing key created", count)
		}
		if count := update(Source(), []query.FieldRef{Amount}, []int64{1}, [][]query.Value{{query.Integer(12)}}); count != 1 {
			t.Fatal(count)
		}
		checkAmounts(12, 2, 3)
	})
	t.Run("zero_negative_and_large_keys_are_exact", func(t *testing.T) {
		reset()
		exec("INSERT INTO " + table + " (id,name,amount) VALUES (0,'zero',0),(-9,'negative',0),(9007199254740993,'large',0)")
		if count := update(Source(), []query.FieldRef{Amount}, []int64{9007199254740993, 0, -9}, allRows); count != 3 {
			t.Fatal(count)
		}
		checkAmounts(33, 22, 1, 2, 3, 11)
	})
	for _, mode := range []string{"root", "forward_cte_name_collision", "collection_duplicates", "separate_collection_filters", "same_collection_filter", "boolean_related_or_null", "collection_not", "root_and_both_colliding_tables"} {
		t.Run("filter_"+mode, func(t *testing.T) {
			reset()
			source := Source()
			want := []int64{11, 2, 3}
			count := int64(1)
			switch mode {
			case "root":
				source = Filter(t, source, query.NewCondition(Amount, query.LookupExact, query.Integer(1)))
			case "forward_cte_name_collision":
				source = Filter(t, source, allowed)
			case "collection_duplicates":
				source = Filter(t, source, red)
				want = []int64{11, 22, 3}
				count = 2
			case "separate_collection_filters":
				source = Filter(t, Filter(t, source, red), blue)
			case "same_collection_filter":
				source = Filter(t, source, red, blue)
				want = []int64{1, 2, 3}
				count = 0
			case "boolean_related_or_null":
				left, err := query.NewExpression(allowed)
				if err != nil {
					t.Fatal(err)
				}
				right, err := query.NewExpression(query.NewCondition(Parent, query.LookupIsNull, query.Boolean(true)))
				if err != nil {
					t.Fatal(err)
				}
				or, err := query.OrExpressions(left, right)
				if err != nil {
					t.Fatal(err)
				}
				source, err = source.WithWhere(or)
				if err != nil {
					t.Fatal(err)
				}
				want = []int64{11, 2, 33}
				count = 2
			case "collection_not":
				leaf, err := query.NewExpression(red)
				if err != nil {
					t.Fatal(err)
				}
				not, err := query.NotExpression(leaf)
				if err != nil {
					t.Fatal(err)
				}
				source, err = source.WithWhere(not)
				if err != nil {
					t.Fatal(err)
				}
				want = []int64{1, 2, 33}
			case "root_and_both_colliding_tables":
				source = Filter(t, source, allowed, red, query.NewCondition(Name, query.LookupExact, query.String("one")))
			}
			if got := update(source, []query.FieldRef{Amount}, allKeys, allRows); got != count {
				t.Fatal(got, count)
			}
			checkAmounts(want...)
		})
	}
	t.Run("write_ignores_read_shapes_and_retains_filter", func(t *testing.T) {
		reset()
		source := Filter(t, Source(), query.NewCondition(ID, query.LookupExact, query.Integer(1))).WithDistinct().WithOrderings(query.NewOrdering(Name, query.Descending))
		lock, err := query.NewRowLock(query.LockForUpdate, query.LockNoWait)
		if err != nil {
			t.Fatal(err)
		}
		source, err = source.WithRowLock(lock)
		if err != nil {
			t.Fatal(err)
		}
		if count := update(source, []query.FieldRef{Amount}, allKeys, allRows); count != 1 {
			t.Fatal(count)
		}
		checkAmounts(11, 2, 3)
	})
	t.Run("empty_filter_still_validates_late_values", func(t *testing.T) {
		reset()
		condition, err := query.NewInCondition(ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		source := Filter(t, Source(), condition)
		if count := update(source, []query.FieldRef{Amount}, allKeys, allRows); count != 0 {
			t.Fatal(count)
		}
		bad := Plan(t, source, []query.FieldRef{Amount}, []int64{1, 2}, [][]query.Value{{query.Integer(11)}, {query.String("22")}})
		if count, err := backend.BulkUpdate(ctx, bad); err == nil || count != 0 {
			t.Fatal(count, err)
		}
		checkAmounts(1, 2, 3)
	})
	for _, mode := range []string{"check", "unique", "foreign_key", "wrong_type", "required_null", "trigger_skip"} {
		t.Run("statement_atomic_"+mode, func(t *testing.T) {
			reset()
			before := snapshot()
			fields := []query.FieldRef{Amount}
			rows := [][]query.Value{{query.Integer(11)}, {query.Integer(-1)}}
			switch mode {
			case "unique":
				fields = []query.FieldRef{Name}
				rows = [][]query.Value{{query.String("three")}, {query.String("other")}}
			case "foreign_key":
				fields = []query.FieldRef{Parent}
				rows = [][]query.Value{{query.Integer(2)}, {query.Integer(999)}}
			case "wrong_type":
				rows[1][0] = query.String("22")
			case "required_null":
				rows[1][0] = query.Null()
			case "trigger_skip":
				fields = []query.FieldRef{Name}
				rows = [][]query.Value{{query.String("updated")}, {query.String("skip-native")}}
			}
			count, err := backend.BulkUpdate(ctx, Plan(t, Source(), fields, []int64{1, 2}, rows))
			if mode == "trigger_skip" {
				if err != nil || count != 1 {
					t.Fatal(count, err)
				}
				after := snapshot()
				if after[0].Name != "updated" || after[1] != before[1] || after[2] != before[2] {
					t.Fatal(after)
				}
				return
			}
			if err == nil || count != 0 || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("statement failure changed rows or returned a count", count, err, snapshot())
			}
		})
	}
	t.Run("large_single_statement_exceeds_999_parameters", func(t *testing.T) {
		reset()
		keys := make([]int64, 1100)
		rows := make([][]query.Value, len(keys))
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for i := range keys {
			keys[i] = int64(i + 10)
			rows[i] = []query.Value{query.Integer(int64(i))}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (id,name,amount) VALUES (%d,'batch-%d',0)", table, keys[i], i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		plan := Plan(t, Source(), []query.FieldRef{Amount}, keys, rows)
		limit, err := backend.BulkUpdateBatchSize(ctx, plan.Spec())
		if err != nil || limit < len(keys) {
			t.Fatal(limit, err)
		}
		if count, err := backend.BulkUpdate(ctx, plan); err != nil || count != int64(len(keys)) {
			t.Fatal(count, err)
		}
		stored := snapshot()
		for i, row := range stored[3:] {
			if row.ID != keys[i] || row.Amount != int64(i) {
				t.Fatal(i, row)
			}
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
			before := snapshot()
			rollback := errors.New("rollback all authored batches")
			var parent, child db.BulkUpdater
			plan := Plan(t, Source(), []query.FieldRef{Amount}, []int64{1}, [][]query.Value{{query.Integer(11)}})
			err := atomic(ctx, func(session db.Session) error {
				parent = session.(db.BulkUpdater)
				if limit, err := parent.BulkUpdateBatchSize(ctx, plan.Spec()); err != nil || limit < 1 {
					t.Fatal(limit, err)
				}
				if count, err := parent.BulkUpdate(ctx, plan); err != nil || count != 1 {
					t.Fatal(count, err)
				}
				if err := db.WithSavepoint(ctx, session, func(inner db.Session) error {
					child = inner.(db.BulkUpdater)
					if _, err := parent.BulkUpdateBatchSize(ctx, plan.Spec()); err == nil {
						t.Fatal("suspended parent advertised capability")
					}
					if _, err := parent.BulkUpdate(ctx, plan); err == nil {
						t.Fatal("suspended parent wrote")
					}
					_, err := child.BulkUpdate(ctx, Plan(t, Source(), []query.FieldRef{Amount}, []int64{2}, [][]query.Value{{query.Integer(22)}}))
					return err
				}); err != nil {
					return err
				}
				if _, err := child.BulkUpdateBatchSize(ctx, plan.Spec()); err == nil {
					t.Fatal("expired child advertised capability")
				}
				if _, err := child.BulkUpdate(ctx, plan); err == nil {
					t.Fatal("expired child wrote")
				}
				return rollback
			})
			if !errors.Is(err, rollback) || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("parent rollback retained batches", err, snapshot())
			}
			if _, err := parent.BulkUpdate(ctx, plan); err == nil {
				t.Fatal("expired parent wrote")
			}
			if _, err := parent.BulkUpdateBatchSize(ctx, plan.Spec()); err == nil {
				t.Fatal("expired parent advertised capability")
			}
		})
		t.Run("scope_"+name+"_failed_late_batch", func(t *testing.T) {
			reset()
			before := snapshot()
			err := atomic(ctx, func(session db.Session) error {
				updater := session.(db.BulkUpdater)
				if _, err := updater.BulkUpdate(ctx, Plan(t, Source(), []query.FieldRef{Amount}, []int64{1}, [][]query.Value{{query.Integer(11)}})); err != nil {
					return err
				}
				_, err := updater.BulkUpdate(ctx, Plan(t, Source(), []query.FieldRef{Amount}, []int64{2}, [][]query.Value{{query.Integer(-1)}}))
				return err
			})
			if err == nil || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("failed late batch committed earlier values", err, snapshot())
			}
		})
	}
	t.Run("snapshot_does_not_advertise_writes", func(t *testing.T) {
		if err := backend.ReadSnapshot(ctx, func(session db.Queryer) error {
			if _, ok := session.(db.BulkUpdater); ok {
				t.Fatal("snapshot advertises bulk update")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("root_cursor_supplies_live_write_scope", func(t *testing.T) {
		reset()
		calls := 0
		var escaped db.BulkUpdater
		plan := Plan(t, Source(), []query.FieldRef{Note}, []int64{1}, [][]query.Value{{query.String("cursor wrote")}})
		err := backend.QueryBatches(ctx, query.NewPlan("update_items", []query.FieldRef{ID}), 1, func(row db.Row) error { var id int64; return row.Scan(&id) }, func(session db.Queryer) (bool, error) {
			calls++
			escaped = session.(db.BulkUpdater)
			if limit, err := escaped.BulkUpdateBatchSize(ctx, plan.Spec()); err != nil || limit < 1 {
				t.Fatal(limit, err)
			}
			count, err := escaped.BulkUpdate(ctx, plan)
			if err == nil && count != 1 {
				t.Fatal(count)
			}
			return false, err
		})
		if err != nil || calls != 1 || snapshot()[0].Note.String != "cursor wrote" {
			t.Fatal(calls, err, snapshot())
		}
		// This is a root executor. Once the cursor releases its connection,
		// retained values deliberately use the live original backend.
		if count, err := escaped.BulkUpdate(ctx, plan); err != nil || count != 1 {
			t.Fatal("released root cursor lost backend access", count, err)
		}
		if limit, err := escaped.BulkUpdateBatchSize(ctx, plan.Spec()); err != nil || limit < 1 {
			t.Fatal("released root cursor lost backend capability", limit, err)
		}
	})
}
