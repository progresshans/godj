// Package queryupdatetest checks uniform updates against independently seeded
// SQL rows. Both native backends execute these contracts on their real sessions.
package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

var (
	ID       = query.NewFieldRef("id", "id", query.FieldInteger, false)
	Name     = query.NewFieldRef("name", "name", query.FieldString, false)
	Amount   = query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	Other    = query.NewFieldRef("other", "other", query.FieldInteger, false)
	Optional = query.NewFieldRef("optional", "optional", query.FieldInteger, true)
	Parent   = query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	Note     = query.NewFieldRef("note", "note", query.FieldString, true)
	Score    = query.NewFieldRef("score", "score", query.FieldFloat, false)
)

func Source() query.Plan {
	return query.NewPlan("godj_update_targets", []query.FieldRef{ID, Name, Amount, Other, Optional, Parent, Note, Score})
}
func Literal(t *testing.T, value query.Value) query.ScalarExpression {
	t.Helper()
	result, err := query.LiteralExpression(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func Field(t *testing.T, field query.FieldRef) query.ScalarExpression {
	t.Helper()
	result, err := query.FieldExpression(field)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func Arithmetic(t *testing.T, operator query.ArithmeticOperator, left, right query.ScalarExpression) query.ScalarExpression {
	t.Helper()
	result, err := query.Arithmetic(operator, left, right)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func Assignment(t *testing.T, field query.FieldRef, expression query.ScalarExpression) query.ScalarAssignment {
	t.Helper()
	value, err := query.NewScalarAssignment(field, expression)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func Plan(t *testing.T, source query.Plan, assignments ...query.ScalarAssignment) query.QueryUpdatePlan {
	t.Helper()
	plan, err := query.NewQueryUpdatePlan(source, ID, assignments)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func Filter(t *testing.T, source query.Plan, conditions ...query.Condition) query.Plan {
	t.Helper()
	plan, err := source.WithConditions(conditions...)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type Backend interface {
	db.QueryUpdater
	db.Atomic
	db.CoordinatedAtomic
	db.RelationAtomic
	db.CoordinatedRelationAtomic
	db.SnapshotReader
	db.BatchQueryer
}

func CheckCompiler(t *testing.T, compile func(query.QueryUpdatePlan) (string, []any, error), postgres bool) {
	t.Helper()
	attack := "one'); DROP TABLE godj_update_targets; --"
	source := Filter(t, Source(), query.NewCondition(Name, query.LookupExact, query.String(attack)))
	expression := Arithmetic(t, query.ArithmeticAdd, Field(t, Amount), Literal(t, query.Integer(2)))
	plan := Plan(t, source, Assignment(t, Amount, expression), Assignment(t, Note, Literal(t, query.String(attack))))
	statement, arguments, err := compile(plan)
	if err != nil || !reflect.DeepEqual(arguments, []any{attack, int64(2), attack}) || strings.Contains(statement, attack) || strings.Count(statement, "UPDATE ") != 1 {
		t.Fatal(statement, arguments, err)
	}
	if postgres {
		if !strings.Contains(statement, `"amount" = ("amount" + CAST($2 AS bigint))`) || !strings.Contains(statement, `"note" = $3`) || !strings.Contains(statement, `"name" = $1`) || strings.Contains(statement, "MATERIALIZED") {
			t.Fatal("lost native predicate or numeric type", statement)
		}
	} else {
		if !strings.Contains(statement, `WITH "godj_update_targets_0"`) || !strings.Contains(statement, `"amount" = godj_int64((godj_int64("amount") + ?2))`) || !strings.Contains(statement, `"note" = ?3`) {
			t.Fatal("lost frozen membership or exact integer guard", statement)
		}
	}
	if statement, arguments, err := compile(query.QueryUpdatePlan{}); err == nil || statement != "" || arguments != nil {
		t.Fatal("zero plan reached SQL")
	}
	if statement, arguments, err := compile(Plan(t, source)); err != nil || statement != "" || arguments != nil {
		t.Fatal("empty assignments produced SQL", statement, arguments, err)
	}
}

type row struct {
	ID, Amount, Other int64
	Name              string
	Optional, Parent  sql.NullInt64
	Note              sql.NullString
	Score             float64
}

func Check(t *testing.T, backend Backend, database *sql.DB, quote func(string) string, postgres bool) {
	t.Helper()
	ctx := t.Context()
	table, parent, labels, links := quote("godj_update_targets"), quote("godj_update_targets_0"), quote("godj_update_targets_1"), quote("query_update_links")
	exec := func(statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE TABLE " + parent + " (id BIGINT PRIMARY KEY,name TEXT NOT NULL)")
	exec("INSERT INTO " + parent + " VALUES (1,'allowed'),(2,'denied')")
	exec("CREATE TABLE " + labels + " (id BIGINT PRIMARY KEY,name TEXT NOT NULL)")
	exec("INSERT INTO " + labels + " VALUES (1,'red'),(2,'blue')")
	exec("CREATE TABLE " + table + " (id BIGINT PRIMARY KEY,name TEXT NOT NULL UNIQUE,amount BIGINT NOT NULL,other BIGINT NOT NULL CHECK(other>=-10),optional BIGINT,parent_id BIGINT REFERENCES " + parent + "(id) DEFERRABLE INITIALLY DEFERRED,note TEXT,score DOUBLE PRECISION NOT NULL)")
	exec("CREATE TABLE " + links + " (id BIGINT PRIMARY KEY,owner_id BIGINT REFERENCES " + table + "(id),label_id BIGINT REFERENCES " + labels + "(id))")
	reset := func() {
		exec("DELETE FROM " + links)
		exec("DELETE FROM " + table)
		exec("INSERT INTO " + table + " VALUES (1,'one',7,2,1,1,'first',1.25),(2,'two',-7,3,-1,2,'',-1.25),(3,'three',0,4,NULL,NULL,NULL,0)")
		exec("INSERT INTO " + links + " VALUES (1,1,1),(2,1,2),(3,2,1),(4,1,1)")
	}
	snapshot := func() []row {
		t.Helper()
		rows, err := database.QueryContext(ctx, "SELECT id,name,amount,other,optional,parent_id,note,score FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := make([]row, 0)
		for rows.Next() {
			var value row
			if err := rows.Scan(&value.ID, &value.Name, &value.Amount, &value.Other, &value.Optional, &value.Parent, &value.Note, &value.Score); err != nil {
				t.Fatal(err)
			}
			result = append(result, value)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return result
	}
	update := func(source query.Plan, assignments ...query.ScalarAssignment) int64 {
		t.Helper()
		plan := Plan(t, source, assignments...)
		if err := backend.CheckQueryUpdate(ctx, plan); err != nil {
			t.Fatal(err)
		}
		count, err := backend.QueryUpdate(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	amounts := func() []int64 {
		rows := snapshot()
		result := make([]int64, len(rows))
		for i, value := range rows {
			result[i] = value.Amount
		}
		return result
	}
	integer := func(value int64) query.ScalarExpression { return Literal(t, query.Integer(value)) }
	add := func(field query.FieldRef, value int64) query.ScalarExpression {
		return Arithmetic(t, query.ArithmeticAdd, Field(t, field), integer(value))
	}
	for _, test := range []struct {
		name   string
		op     query.ArithmeticOperator
		number int64
		want   []int64
	}{
		{"add", query.ArithmeticAdd, 2, []int64{9, -5, 2}}, {"subtract", query.ArithmeticSubtract, 2, []int64{5, -9, -2}},
		{"multiply", query.ArithmeticMultiply, 3, []int64{21, -21, 0}}, {"divide", query.ArithmeticDivide, 2, []int64{3, -3, 0}},
		{"remainder", query.ArithmeticModulo, 2, []int64{1, -1, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reset()
			count := update(Source(), Assignment(t, Amount, Arithmetic(t, test.op, Field(t, Amount), integer(test.number))))
			if count != 3 || !slices.Equal(amounts(), test.want) {
				t.Fatal(count, amounts())
			}
		})
	}
	t.Run("original_row_and_nested_expression", func(t *testing.T) {
		reset()
		count := update(Source(), Assignment(t, Amount, add(Amount, 1)), Assignment(t, Other, add(Amount, 5)))
		rows := snapshot()
		if count != 3 || rows[0].Amount != 8 || rows[0].Other != 12 || rows[1].Amount != -6 || rows[1].Other != -2 {
			t.Fatal(count, rows)
		}
		reset()
		if count := update(Source(), Assignment(t, Amount, Field(t, Other)), Assignment(t, Other, Field(t, Amount))); count != 3 {
			t.Fatal(count)
		}
		rows = snapshot()
		if rows[0].Amount != 2 || rows[0].Other != 7 || rows[1].Amount != 3 || rows[1].Other != -7 {
			t.Fatal(rows)
		}
		reset()
		expr := Arithmetic(t, query.ArithmeticSubtract, Arithmetic(t, query.ArithmeticMultiply, Arithmetic(t, query.ArithmeticAdd, Field(t, Amount), Field(t, Other)), integer(2)), integer(1))
		if count := update(Source(), Assignment(t, Amount, expr)); count != 3 || !slices.Equal(amounts(), []int64{17, -9, 7}) {
			t.Fatal(count, amounts())
		}
	})
	t.Run("literal_arithmetic_numeric_types", func(t *testing.T) {
		reset()
		expr := Arithmetic(t, query.ArithmeticDivide, integer(5), integer(2))
		if count := update(Source(), Assignment(t, Amount, expr)); count != 3 || !slices.Equal(amounts(), []int64{2, 2, 2}) {
			t.Fatal(count, amounts())
		}
		floating := Arithmetic(t, query.ArithmeticDivide, Literal(t, query.Float(5)), Literal(t, query.Float(2)))
		if count := update(Source(), Assignment(t, Score, floating)); count != 3 {
			t.Fatal(count)
		}
		for _, value := range snapshot() {
			if value.Score != 2.5 {
				t.Fatal(value)
			}
		}
	})
	t.Run("nullable_copy_and_native_null_arithmetic", func(t *testing.T) {
		reset()
		if count := update(Source(), Assignment(t, Optional, add(Optional, 1))); count != 3 {
			t.Fatal(count)
		}
		rows := snapshot()
		if rows[0].Optional != (sql.NullInt64{Int64: 2, Valid: true}) || rows[1].Optional != (sql.NullInt64{Int64: 0, Valid: true}) || rows[2].Optional.Valid {
			t.Fatal(rows)
		}
		if count := update(Source(), Assignment(t, Note, Field(t, Name)), Assignment(t, Parent, Literal(t, query.Null()))); count != 3 {
			t.Fatal(count)
		}
		for _, value := range snapshot() {
			if value.Parent.Valid || !value.Note.Valid || value.Note.String != value.Name {
				t.Fatal(value)
			}
		}
	})
	t.Run("empty_unchanged_and_read_shape", func(t *testing.T) {
		reset()
		before := snapshot()
		if count := update(Source()); count != 0 {
			t.Fatal(count)
		}
		if count := update(Source(), Assignment(t, Amount, Field(t, Amount))); count != 3 {
			t.Fatal(count)
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("unchanged update changed data")
		}
		condition, err := query.NewInCondition(ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if count := update(Filter(t, Source(), condition), Assignment(t, Amount, integer(11))); count != 0 {
			t.Fatal(count)
		}
		lock, err := query.NewRowLock(query.LockForUpdate, query.LockNoWait)
		if err != nil {
			t.Fatal(err)
		}
		source, err := Filter(t, Source(), query.NewCondition(ID, query.LookupExact, query.Integer(1))).WithDistinct().WithOrderings(query.NewOrdering(ID, query.Descending)).WithRowLock(lock)
		if err != nil {
			t.Fatal(err)
		}
		if count := update(source, Assignment(t, Amount, integer(11))); count != 1 || !slices.Equal(amounts(), []int64{11, -7, 0}) {
			t.Fatal(count, amounts())
		}
	})
	item, parentIdentity, link, label := ir.ModelIdentity{AppLabel: "app", ModelName: "item"}, ir.ModelIdentity{AppLabel: "app", ModelName: "parent"}, ir.ModelIdentity{AppLabel: "app", ModelName: "link"}, ir.ModelIdentity{AppLabel: "app", ModelName: "label"}
	forward, err := query.NewForwardRelationPath(item, "godj_update_targets", "parent", "parent_id", parentIdentity, "godj_update_targets_0", "id", true, Name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := query.NewReverseRelationPath(link, "query_update_links", "owner", "owner_id", item, "godj_update_targets", "id", "labels", false, ID, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.NewForwardRelationPath(link, "query_update_links", "label", "label_id", label, "godj_update_targets_1", "id", false, Name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := query.NewRelationChain(append(reverse.Hops(), target.Hops()...), []query.FieldRef{ID, ID, ID}, Name, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	red, blue := query.NewRelatedCondition(collection, query.LookupExact, query.String("red")), query.NewRelatedCondition(collection, query.LookupExact, query.String("blue"))
	for _, mode := range []string{"forward", "collection", "separate_collection", "same_collection", "negated_collection", "both_colliding_tables"} {
		t.Run(mode, func(t *testing.T) {
			reset()
			source := Source()
			want := []int64{17, -7, 0}
			count := int64(1)
			switch mode {
			case "forward":
				source = Filter(t, source, query.NewRelatedCondition(forward, query.LookupExact, query.String("allowed")))
			case "collection":
				source = Filter(t, source, red)
				want = []int64{17, 3, 0}
				count = 2
			case "separate_collection":
				source = Filter(t, Filter(t, source, red), blue)
			case "same_collection":
				source = Filter(t, source, red, blue)
				want = []int64{7, -7, 0}
				count = 0
			case "negated_collection":
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
				want = []int64{7, -7, 10}
			case "both_colliding_tables":
				source = Filter(t, source, red, query.NewRelatedCondition(forward, query.LookupExact, query.String("allowed")))
			}
			if got := update(source, Assignment(t, Amount, add(Amount, 10))); got != count || !slices.Equal(amounts(), want) {
				t.Fatal(got, amounts(), want)
			}
		})
	}
	for _, mode := range []string{"unique", "check_later_row", "null_later_row", "foreign_key", "primary_key_reference", "primary_key_collision"} {
		t.Run("atomic_"+mode, func(t *testing.T) {
			reset()
			before := snapshot()
			source := Source()
			assignments := []query.ScalarAssignment{Assignment(t, Amount, add(Amount, 1))}
			switch mode {
			case "unique":
				assignments = append(assignments, Assignment(t, Name, Literal(t, query.String("same"))))
			case "check_later_row":
				assignments = append(assignments, Assignment(t, Other, add(Amount, -4)))
			case "null_later_row":
				assignments = []query.ScalarAssignment{Assignment(t, Amount, Field(t, Optional))}
			case "foreign_key":
				assignments = append(assignments, Assignment(t, Parent, integer(999)))
			case "primary_key_reference":
				assignments = append(assignments, Assignment(t, ID, add(ID, 10)))
			case "primary_key_collision":
				exec("DELETE FROM " + links)
				assignments = append(assignments, Assignment(t, ID, integer(1)))
			}
			got, err := backend.QueryUpdate(ctx, Plan(t, source, assignments...))
			if err == nil || got != 0 || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal(got, err, snapshot())
			}
			if mode == "primary_key_collision" && !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey}) {
				t.Fatal("native primary-key collision lost its structured classification", err)
			}
		})
	}
	t.Run("primary_keys_and_exact_large_values", func(t *testing.T) {
		reset()
		exec("DELETE FROM " + links)
		if count := update(Source(), Assignment(t, ID, add(ID, 9007199254740990))); count != 3 {
			t.Fatal(count)
		}
		rows := snapshot()
		if rows[0].ID != 9007199254740991 || rows[2].ID != 9007199254740993 {
			t.Fatal(rows)
		}
		if count := update(Filter(t, Source(), query.NewCondition(ID, query.LookupExact, query.Integer(rows[0].ID))), Assignment(t, ID, integer(-9))); count != 1 {
			t.Fatal(count)
		}
		if snapshot()[0].ID != -9 {
			t.Fatal(snapshot())
		}
	})
	for _, test := range []struct {
		name     string
		seed     int64
		operator query.ArithmeticOperator
		operand  int64
	}{
		{"add", math.MaxInt64, query.ArithmeticAdd, 1}, {"subtract", math.MinInt64, query.ArithmeticSubtract, 1},
		{"multiply", math.MaxInt64, query.ArithmeticMultiply, 2}, {"divide", math.MinInt64, query.ArithmeticDivide, -1},
	} {
		t.Run("overflow_"+test.name, func(t *testing.T) {
			reset()
			exec(fmt.Sprintf("UPDATE %s SET amount=%d WHERE id=2", table, test.seed))
			before := snapshot()
			plan := Plan(t, Source(), Assignment(t, Amount, Arithmetic(t, test.operator, Field(t, Amount), integer(test.operand))))
			if count, err := backend.QueryUpdate(ctx, plan); err == nil || count != 0 || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("overflow lost exact data or changed preceding rows", count, err, snapshot())
			}
		})
	}
	t.Run("negation_boundary", func(t *testing.T) {
		reset()
		negated, err := query.NegateScalar(Field(t, Amount))
		if err != nil {
			t.Fatal(err)
		}
		if count := update(Source(), Assignment(t, Amount, negated)); count != 3 || !slices.Equal(amounts(), []int64{-7, 7, 0}) {
			t.Fatal(count, amounts())
		}
		exec(fmt.Sprintf("UPDATE %s SET amount=%d WHERE id=2", table, int64(math.MinInt64)))
		before := snapshot()
		if count, err := backend.QueryUpdate(ctx, Plan(t, Source(), Assignment(t, Amount, negated))); err == nil || count != 0 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal(count, err, snapshot())
		}
	})
	t.Run("native_zero_divisor", func(t *testing.T) {
		reset()
		before := snapshot()
		plan := Plan(t, Source(), Assignment(t, Optional, Arithmetic(t, query.ArithmeticDivide, Field(t, Optional), integer(0))))
		count, err := backend.QueryUpdate(ctx, plan)
		if postgres {
			if err == nil || count != 0 || !reflect.DeepEqual(before, snapshot()) {
				t.Fatal(count, err, snapshot())
			}
		} else {
			if err != nil || count != 3 {
				t.Fatal(count, err)
			}
			for _, value := range snapshot() {
				if value.Optional.Valid {
					t.Fatal(value)
				}
			}
		}
	})
	t.Run("context_and_borrowed_scope_lifetime", func(t *testing.T) {
		reset()
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(canceled, query.QueryUpdatePlan{}); count != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal(count, err)
		}
		if err := backend.CheckQueryUpdate(canceled, query.QueryUpdatePlan{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := backend.QueryUpdate(nil, query.QueryUpdatePlan{}); err == nil {
			t.Fatal("nil context accepted")
		}
		if err := backend.CheckQueryUpdate(nil, query.QueryUpdatePlan{}); err == nil {
			t.Fatal("nil preflight context accepted")
		}
	})
	scopes := map[string]func(context.Context, func(db.Session) error) error{"ordinary": backend.Atomic, "coordinated": backend.CoordinatedAtomic,
		"relation": func(ctx context.Context, callback func(db.Session) error) error {
			return backend.AtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		},
		"coordinated_relation": func(ctx context.Context, callback func(db.Session) error) error {
			return backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) })
		}}
	for name, run := range scopes {
		t.Run("scope_"+name, func(t *testing.T) {
			reset()
			sentinel := errors.New("rollback parent")
			var escaped db.QueryUpdater
			err := run(ctx, func(session db.Session) error {
				capability, ok := session.(db.QueryUpdater)
				if !ok {
					t.Fatal("scope omitted query update capability")
				}
				escaped = capability
				if err := capability.CheckQueryUpdate(ctx, Plan(t, Source())); err != nil {
					return err
				}
				if count, err := capability.QueryUpdate(ctx, Plan(t, Source(), Assignment(t, Amount, add(Amount, 1)))); err != nil || count != 3 {
					t.Fatal(count, err)
				}
				childErr := db.WithSavepoint(ctx, session, func(child db.Session) error {
					count, err := child.(db.QueryUpdater).QueryUpdate(ctx, Plan(t, Source(), Assignment(t, Amount, add(Amount, 10))))
					if err != nil || count != 3 {
						t.Fatal(count, err)
					}
					return sentinel
				})
				if !errors.Is(childErr, sentinel) {
					t.Fatal(childErr)
				}
				return sentinel
			})
			if !errors.Is(err, sentinel) || !slices.Equal(amounts(), []int64{7, -7, 0}) {
				t.Fatal(err, amounts())
			}
			if err := escaped.CheckQueryUpdate(ctx, Plan(t, Source())); err == nil {
				t.Fatal("escaped scope preflight succeeded")
			}
			if count, err := escaped.QueryUpdate(ctx, Plan(t, Source())); err == nil || count != 0 {
				t.Fatal("escaped empty update succeeded", count, err)
			}
		})
	}
	t.Run("snapshot_has_no_write_capability", func(t *testing.T) {
		if err := backend.ReadSnapshot(ctx, func(session db.Queryer) error {
			if _, ok := session.(db.QueryUpdater); ok {
				t.Fatal("read snapshot advertises query update")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("root_cursor_write_affinity", func(t *testing.T) {
		reset()
		calls := 0
		var escaped db.QueryUpdater
		plan := Plan(t, Filter(t, Source(), query.NewCondition(ID, query.LookupExact, query.Integer(1))), Assignment(t, Amount, add(Amount, 1)))
		err := backend.QueryBatches(ctx, Source(), 1, func(row db.Row) error {
			var id, amount, other int64
			var name string
			var optional, parent sql.NullInt64
			var note sql.NullString
			var score float64
			return row.Scan(&id, &name, &amount, &other, &optional, &parent, &note, &score)
		}, func(session db.Queryer) (bool, error) {
			calls++
			var ok bool
			escaped, ok = session.(db.QueryUpdater)
			if !ok {
				t.Fatal("root cursor omitted query update")
			}
			if err := escaped.CheckQueryUpdate(ctx, plan); err != nil {
				return false, err
			}
			count, err := escaped.QueryUpdate(ctx, plan)
			if err == nil && count != 1 {
				t.Fatal(count)
			}
			return false, err
		})
		if err != nil || calls != 1 || !slices.Equal(amounts(), []int64{8, -7, 0}) {
			t.Fatal(calls, err, amounts())
		}
		if count, err := escaped.QueryUpdate(ctx, plan); err != nil || count != 1 || !slices.Equal(amounts(), []int64{9, -7, 0}) {
			t.Fatal("root cursor no longer uses live backend", count, err, amounts())
		}
	})
}
