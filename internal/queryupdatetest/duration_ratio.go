package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

// CheckDurationRatios exercises the Float result of two periods through native
// reads, selected results, aggregates and writes. It never casts an input period
// to Float to derive the expected ratio.
func CheckDurationRatios(t *testing.T, backend interface {
	Backend
	db.Queryer
}, database *sql.DB, quote func(string) string, postgres bool, assertNative func(*testing.T, error, string)) {
	t.Helper()
	ctx := t.Context()
	const name = "duration_ratio_values"
	table := quote(name)
	left := query.NewFieldRef("left_period", "left_period", query.FieldDuration, true)
	right := query.NewFieldRef("right_period", "right_period", query.FieldDuration, true)
	value := query.NewFieldRef("value", "value", query.FieldFloat, true)
	marker := query.NewFieldRef("marker", "marker", query.FieldInteger, false)
	source := query.NewPlan(name, []query.FieldRef{ID, left, right, value, marker})
	ordered := source.WithOrderings(query.NewOrdering(ID, query.Ascending))
	ratio := Arithmetic(t, query.ArithmeticDivide, Field(t, left), Field(t, right))
	exec := func(t *testing.T, statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	period := func(microseconds string) string {
		if postgres {
			return "INTERVAL '" + microseconds + " microseconds'"
		}
		return microseconds
	}
	periodType := "INTEGER"
	if postgres {
		periodType = "INTERVAL"
	}
	exec(t, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, left_period "+periodType+", right_period "+periodType+", value DOUBLE PRECISION, marker BIGINT NOT NULL CHECK(marker >= 0))")
	reset := func(t *testing.T) {
		t.Helper()
		exec(t, "DELETE FROM "+table)
		exec(t, "INSERT INTO "+table+" VALUES (1,"+period("2")+","+period("3")+",NULL,0),(2,NULL,NULL,NULL,0)")
	}
	snapshot := func(t *testing.T) [][]any {
		t.Helper()
		rows, err := database.QueryContext(ctx, "SELECT id,CAST(left_period AS TEXT),CAST(right_period AS TEXT),value,marker FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result [][]any
		for rows.Next() {
			row := make([]any, 5)
			if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4]); err != nil {
				t.Fatal(err)
			}
			result = append(result, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return result
	}
	project := func(t *testing.T, plan query.Plan, scalar query.ScalarExpression) query.Plan {
		t.Helper()
		result, err := query.ScalarResult(scalar)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewProjectionResult(result)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	read := func(reader db.Queryer, plan query.Plan) ([]any, error) {
		rows, err := reader.Query(ctx, plan)
		if err != nil {
			if rows != nil {
				err = errors.Join(err, rows.Close())
			}
			return nil, err
		}
		defer rows.Close()
		var result []any
		for rows.Next() {
			var value any
			if err := rows.Scan(&value); err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		return result, nil
	}
	assertValues := func(t *testing.T, actual, expected []any) {
		t.Helper()
		if len(actual) != len(expected) {
			t.Fatal("ratio row count changed", actual, expected)
		}
		for index, want := range expected {
			if want == nil {
				if actual[index] != nil {
					t.Fatal("NULL ratio became a value", actual[index])
				}
				continue
			}
			got, valid := actual[index].(float64)
			if !valid || math.Float64bits(got) != math.Float64bits(want.(float64)) {
				t.Fatalf("ratio lost its correctly rounded Float: got %T(%v), want %v", actual[index], actual[index], want)
			}
		}
	}
	update := Plan(t, source, Assignment(t, value, ratio), Assignment(t, right, Field(t, left)),
		Assignment(t, marker, Arithmetic(t, query.ArithmeticAdd, Field(t, marker), Literal(t, query.Integer(1)))))

	t.Run("projection_assignment_and_rollback", func(t *testing.T) {
		reset(t)
		exec(t, "INSERT INTO "+table+" VALUES (3,"+period("9007199254740995")+","+period("9007199254740993")+",NULL,0),(4,"+period("0")+","+period("-2")+",NULL,0),(5,NULL,"+period("0")+",NULL,0)")
		before := snapshot(t)
		want := []any{float64(2.0 / 3.0), nil, math.Nextafter(1, 2), float64(0), nil}
		rows, err := read(backend, project(t, ordered, ratio))
		if err != nil {
			t.Fatal(err)
		}
		assertValues(t, rows, want)
		if !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("ratio projection changed storage")
		}
		sentinel := errors.New("ratio rollback")
		err = backend.Atomic(ctx, func(session db.Session) error {
			count, err := session.(db.QueryUpdater).QueryUpdate(ctx, update)
			if err != nil || count != 5 {
				t.Fatal("ratio assignment failed", count, err)
			}
			rows, err := read(session, project(t, ordered, Field(t, value)))
			if err != nil {
				t.Fatal(err)
			}
			assertValues(t, rows, want) // The other assignment must not change the original divisor.
			return sentinel
		})
		if !errors.Is(err, sentinel) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("ratio transaction did not preserve the full stored state", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(canceled, update); count != 0 || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("canceled ratio assignment escaped its context", count, err)
		}
	})

	t.Run("selected_result_and_filtered_aggregate", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		single := Filter(t, source, query.NewCondition(ID, query.LookupExact, query.Integer(1)))
		stage, scope, err := query.NewDerivedPlan(project(t, single, ratio))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := scope.Column(0)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := query.SelectedExpression(ref)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, project(t, stage, Arithmetic(t, query.ArithmeticMultiply, selected, Literal(t, query.Float(2)))))
		if err != nil {
			t.Fatal(err)
		}
		assertValues(t, rows, []any{float64(4.0 / 3.0)})
		if !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("selected ratio result changed storage")
		}
		exec(t, "UPDATE "+table+" SET left_period="+period("1")+",right_period="+period("0")+" WHERE id=2")
		before = snapshot(t)
		condition, err := query.NewExpression(query.NewCondition(ID, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, ratio)
		if err != nil {
			t.Fatal(err)
		}
		chosen, err := query.CaseScalar(Literal(t, query.Float(5)), branch)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = read(backend, project(t, ordered, chosen))
		if err != nil {
			t.Fatal("unselected zero divisor was evaluated", err)
		}
		assertValues(t, rows, []any{float64(2.0 / 3.0), float64(5)})
		for _, kind := range []query.ResultExpressionKind{query.ResultSum, query.ResultAvg, query.ResultMin, query.ResultMax} {
			aggregate, err := query.ScalarAggregateResult(kind, ratio)
			if err != nil {
				t.Fatal(err)
			}
			aggregate, err = aggregate.WithFilter(condition)
			if err != nil {
				t.Fatal(err)
			}
			shape, err := query.NewAggregateResult(aggregate)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := source.WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = read(backend, plan)
			if err != nil {
				t.Fatal("filtered ratio aggregate evaluated a zero divisor", kind, err)
			}
			assertValues(t, rows, []any{float64(2.0 / 3.0)})
		}
		if !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("ratio result stages or aggregates changed storage")
		}
	})

	bad := "'not-a-period'"
	if postgres {
		bad = "INTERVAL '1 month'"
	}
	for _, test := range []struct{ name, mutation, state string }{
		{"zero_divisor", "left_period=" + period("1") + ",right_period=" + period("0"), "22012"},
		{"corrupt_numerator_with_null", "left_period=" + bad + ",right_period=NULL", "22007"},
		{"corrupt_denominator_with_null", "left_period=NULL,right_period=" + bad, "22007"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reset(t)
			exec(t, "UPDATE "+table+" SET "+test.mutation+" WHERE id=2")
			before := snapshot(t)
			rows, err := read(backend, project(t, ordered, ratio))
			if err == nil || rows != nil {
				t.Fatal("invalid ratio published a partial result", rows, err)
			}
			assertNative(t, err, test.state)
			count, err := backend.QueryUpdate(ctx, update)
			if err == nil || count != 0 {
				t.Fatal("invalid ratio UPDATE reported success", count, err)
			}
			assertNative(t, err, test.state)
			if !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("failing ratio query or UPDATE changed storage")
			}
		})
	}
	if postgres {
		t.Run("full_model_range", func(t *testing.T) {
			reset(t)
			exec(t, "UPDATE "+table+" SET left_period=INTERVAL '999999999 days 23:59:59.999999',right_period=INTERVAL '1 microsecond' WHERE id=1")
			before := snapshot(t)
			single := Filter(t, source, query.NewCondition(ID, query.LookupExact, query.Integer(1)))
			rows, err := read(backend, project(t, single, ratio))
			if err != nil {
				t.Fatal("full model ratio was narrowed to int64", err)
			}
			assertValues(t, rows, []any{float64(86399999999999999999)})
			maximum := Literal(t, query.Duration(duration.Duration{Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}))
			rows, err = read(backend, project(t, single, Arithmetic(t, query.ArithmeticDivide, Field(t, left), maximum)))
			if err != nil {
				t.Fatal(err)
			}
			assertValues(t, rows, []any{float64(1)})
			if !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("full model ratio changed storage")
			}
		})
	}
}
