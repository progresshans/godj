package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationcalc"
	"github.com/progresshans/godj/query"
)

// CheckDurationScaling executes the shared AST and ownership contract against
// each real backend. Expected values below are independent rational examples.
func CheckDurationScaling(t *testing.T, backend interface {
	Backend
	db.Queryer
}, database *sql.DB, quote func(string) string, postgres bool, assertNative func(*testing.T, error)) {
	t.Helper()
	table := quote("scaling")
	periodSQL := func(value string) string {
		if postgres {
			return "INTERVAL '" + value + " microseconds'"
		}
		return value
	}
	periodOutput := func(value int64) any {
		if postgres {
			return duration.FromMicroseconds(value).String()
		}
		return value
	}
	ctx := t.Context()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	period := query.NewFieldRef("period", "period", query.FieldDuration, true)
	factor := query.NewFieldRef("factor", "factor", query.FieldInteger, false)
	marker := query.NewFieldRef("marker", "marker", query.FieldInteger, false)
	source := query.NewPlan("scaling", []query.FieldRef{id, period, factor, marker})
	ordered := source.WithOrderings(query.NewOrdering(id, query.Ascending))
	divide := Arithmetic(t, query.ArithmeticDivide, Field(t, period), Field(t, factor))
	one := Literal(t, query.Duration(duration.FromMicroseconds(1)))
	constant := func(value int64) query.ScalarExpression { return Literal(t, query.Integer(value)) }
	result := func(t *testing.T, value query.ScalarExpression) query.ResultExpression {
		t.Helper()
		result, err := query.ScalarResult(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	project := func(t *testing.T, plan query.Plan, values ...query.ResultExpression) query.Plan {
		t.Helper()
		shape, err := query.NewProjectionResult(values...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	read := func(reader db.Queryer, plan query.Plan) ([][]any, error) {
		rows, err := reader.Query(ctx, plan)
		if err != nil {
			if rows != nil {
				err = errors.Join(err, rows.Close())
			}
			return nil, err
		}
		defer rows.Close()
		var result [][]any
		for rows.Next() {
			values := make([]any, len(plan.ResultShape().Expressions()))
			targets := make([]any, len(values))
			for index := range values {
				targets[index] = &values[index]
			}
			if err := rows.Scan(targets...); err != nil {
				return nil, err
			}
			result = append(result, values)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		return result, nil
	}
	assertRows := func(t *testing.T, plan query.Plan, want [][]any) {
		t.Helper()
		got, err := read(backend, plan)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("native query changed exact arithmetic or order", got, want, err)
		}
	}
	exec := func(t *testing.T, statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	periodType := "BIGINT"
	if postgres {
		periodType = "INTERVAL"
	}
	exec(t, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, period "+periodType+", factor BIGINT NOT NULL, marker BIGINT NOT NULL CHECK(marker >= 0))")
	reset := func(t *testing.T) {
		t.Helper()
		exec(t, "DELETE FROM "+table)
		exec(t, "INSERT INTO "+table+" VALUES(1,"+periodSQL("1")+",2,0),(2,"+periodSQL("1")+",3,0),(3,"+periodSQL("1")+",6,0),(4,NULL,0,0),(5,"+periodSQL("0")+",2,0)")
	}
	aggregatePlan := func(t *testing.T, base query.Plan, values ...query.ResultExpression) query.Plan {
		t.Helper()
		shape, err := query.NewAggregateResult(values...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := base.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	aggregate := func(t *testing.T, kind query.ResultExpressionKind, operand query.ScalarExpression) query.ResultExpression {
		t.Helper()
		value, err := query.ScalarAggregateResult(kind, operand)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	ratios := func(t *testing.T, input query.Plan) query.Plan {
		t.Helper()
		stage, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		values := make([]query.ResultExpression, len(input.ResultShape().Expressions()))
		for index := range values {
			ref, err := scope.Column(index)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := query.SelectedExpression(ref)
			if err != nil {
				t.Fatal(err)
			}
			values[index] = result(t, Arithmetic(t, query.ArithmeticDivide, selected, one))
		}
		return project(t, stage, values...)
	}

	t.Run("projection_selection_comparison_and_order", func(t *testing.T) {
		reset(t)
		restored := Arithmetic(t, query.ArithmeticMultiply, divide, Field(t, factor))
		ratio := Arithmetic(t, query.ArithmeticDivide, restored, one)
		assertRows(t, project(t, ordered, result(t, ratio)), [][]any{{float64(1)}, {float64(1)}, {float64(1)}, {nil}, {float64(0)}})
		order, err := query.NewResultOrdering(result(t, divide), query.Ascending)
		if err != nil {
			t.Fatal(err)
		}
		order, err = order.WithNulls(query.NullsLast)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(t, source.WithOrderings(order), query.FieldResult(id)), [][]any{{int64(5)}, {int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}})
		less, err := query.NewScalarCondition(divide, query.LookupLessThan, query.Duration(duration.FromMicroseconds(1)))
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(t, Filter(t, ordered, less), query.FieldResult(id)), [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(5)}})
		stage, scope, err := query.NewDerivedPlan(project(t, ordered, result(t, divide), query.FieldResult(id)))
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
		idRef, err := scope.Column(1)
		if err != nil {
			t.Fatal(err)
		}
		selectedID, err := query.SelectedExpression(idRef)
		if err != nil {
			t.Fatal(err)
		}
		stageOrder, err := query.NewResultOrdering(result(t, selectedID), query.Ascending)
		if err != nil {
			t.Fatal(err)
		}
		stage = stage.WithOrderings(stageOrder)
		composed := Arithmetic(t, query.ArithmeticMultiply, selected, constant(6))
		assertRows(t, project(t, stage, result(t, Arithmetic(t, query.ArithmeticDivide, composed, one))), [][]any{{float64(3)}, {float64(2)}, {float64(1)}, {nil}, {float64(0)}})
		less, err = query.NewScalarCondition(selected, query.LookupLessThan, query.Duration(duration.FromMicroseconds(1)))
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(t, Filter(t, stage, less), result(t, Arithmetic(t, query.ArithmeticDivide, composed, one))), [][]any{{float64(3)}, {float64(2)}, {float64(1)}, {float64(0)}})
	})

	t.Run("aggregate_groups_distinct_and_final_rounding", func(t *testing.T) {
		reset(t)
		sum, err := query.ScalarAggregateResult(query.ResultSum, divide)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewAggregateResult(sum)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		stage, scope, err := query.NewDerivedPlan(plan)
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
		assertRows(t, project(t, stage, result(t, Arithmetic(t, query.ArithmeticDivide, selected, one))), [][]any{{float64(1)}})
		exec(t, "INSERT INTO "+table+" VALUES(6,"+periodSQL("2")+",4,0)") // 2/4 and 1/2 must share a canonical key.
		count, err := query.ScalarAggregateResult(query.ResultCount, divide)
		if err != nil {
			t.Fatal(err)
		}
		count, err = count.WithDistinct()
		if err != nil {
			t.Fatal(err)
		}
		shape, err = query.NewAggregateResult(count)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, plan, [][]any{{int64(4)}})
		shape, err = query.NewGroupedResult([]query.ResultExpression{query.FieldResult(marker)}, []query.ResultExpression{sum})
		if err != nil {
			t.Fatal(err)
		}
		plan, err = source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		having, err := query.NewGroupExpression(sum, query.LookupLessThan, query.Duration(duration.FromMicroseconds(2)))
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithGroupHaving(having)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, plan)
		if err != nil || len(rows) != 1 {
			t.Fatal("HAVING rounded 1.5 before comparing it", rows, err)
		}
		key, ok := rows[0][1].([]byte)
		if !ok {
			t.Fatal("fractional group lost its query carrier", rows)
		}
		value, err := durationcalc.FromKey(key)
		if err != nil {
			t.Fatal(err)
		}
		materialized, err := value.Duration()
		if err != nil || materialized != duration.FromMicroseconds(2) {
			t.Fatal("final group did not round once", materialized, err)
		}
	})

	t.Run("factor_domains_and_same_row_atomic_write", func(t *testing.T) {
		reset(t)
		five := Literal(t, query.Duration(duration.FromMicroseconds(5)))
		exact := Arithmetic(t, query.ArithmeticMultiply, five, Literal(t, query.Decimal(decimal.Decimal{Coefficient: "1", Exponent: -1})))
		floating := Arithmetic(t, query.ArithmeticMultiply, five, Literal(t, query.Float(0.1)))
		first := Filter(t, source, query.NewCondition(id, query.LookupExact, query.Integer(1)))
		rows, err := read(backend, project(t, first, result(t, exact), result(t, floating)))
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		for index, want := range []int64{0, 1} {
			key, ok := rows[0][index].([]byte)
			if !ok {
				t.Fatal("factor result lost its carrier", rows)
			}
			value, err := durationcalc.FromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			period, err := value.Duration()
			if err != nil || period != duration.FromMicroseconds(want) {
				t.Fatal("numeric operand domains were conflated", index, period, err)
			}
		}
		before, err := read(backend, project(t, ordered, query.FieldResult(id), query.FieldResult(period), query.FieldResult(factor), query.FieldResult(marker)))
		if err != nil {
			t.Fatal(err)
		}
		restored := Arithmetic(t, query.ArithmeticMultiply, divide, Field(t, factor))
		write := Plan(t, source, Assignment(t, period, restored), Assignment(t, factor, constant(17)))
		sentinel := errors.New("duration scaling rollback")
		err = backend.Atomic(ctx, func(session db.Session) error {
			count, err := session.(db.QueryUpdater).QueryUpdate(ctx, write)
			if err != nil || count != 5 {
				t.Fatal("same-row scaling write failed", count, err)
			}
			values, err := read(session, project(t, ordered, query.FieldResult(period)))
			if err != nil || !reflect.DeepEqual(values, [][]any{{periodOutput(1)}, {periodOutput(1)}, {periodOutput(1)}, {nil}, {periodOutput(0)}}) {
				t.Fatal("stored values were rounded early or read another assignment", values, err)
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatal("rollback lost original cause", err)
		}
		after, err := read(backend, project(t, ordered, query.FieldResult(id), query.FieldResult(period), query.FieldResult(factor), query.FieldResult(marker)))
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("transaction changed stored state", after, err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(cancelled, write); count != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal("write ignored cancellation", count, err)
		}
		// Row 4 must fail after earlier rows have been considered. The backend must
		// roll back the entire statement, including their factor assignments.
		exec(t, "UPDATE "+table+" SET period="+periodSQL("1")+" WHERE id=4")
		before, err = read(backend, project(t, ordered, query.FieldResult(id), query.FieldResult(period), query.FieldResult(factor), query.FieldResult(marker)))
		if err != nil {
			t.Fatal(err)
		}
		if count, err := backend.QueryUpdate(ctx, write); count != 0 || err == nil {
			t.Fatal("zero divisor produced a partial write", count, err)
		} else {
			assertNative(t, err)
		}
		after, err = read(backend, project(t, ordered, query.FieldResult(id), query.FieldResult(period), query.FieldResult(factor), query.FieldResult(marker)))
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("failed statement changed storage", after, err)
		}
	})

	t.Run("extrema_average_and_constant_aggregate_ownership", func(t *testing.T) {
		reset(t)
		values := []query.ResultExpression{
			aggregate(t, query.ResultMin, divide), aggregate(t, query.ResultMax, divide),
			aggregate(t, query.ResultAvg, divide),
		}
		assertRows(t, ratios(t, aggregatePlan(t, source, values...)), [][]any{{float64(0), float64(0.5), float64(0.25)}})
		half := Arithmetic(t, query.ArithmeticDivide, one, constant(2))
		sum, average := aggregate(t, query.ResultSum, half), aggregate(t, query.ResultAvg, half)
		assertRows(t, ratios(t, aggregatePlan(t, source, sum, average)), [][]any{{float64(2.5), float64(0.5)}})
		limited, err := ordered.WithLimit(2)
		if err != nil {
			t.Fatal(err)
		}
		limited, err = limited.WithOffset(1)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, ratios(t, aggregatePlan(t, limited, sum, average)), [][]any{{float64(1), float64(0.5)}})
		distinct, err := sum.WithDistinct()
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, ratios(t, aggregatePlan(t, source, distinct)), [][]any{{float64(0.5)}})
		// The same constants must also retain all rows of a selected source.
		selected, _, err := query.NewDerivedPlan(project(t, ordered, result(t, divide)))
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, ratios(t, aggregatePlan(t, selected, sum, average)), [][]any{{float64(2.5), float64(0.5)}})
	})

	t.Run("empty_selected_fraction_and_null_case", func(t *testing.T) {
		reset(t)
		empty, err := query.NewInCondition(id, nil)
		if err != nil {
			t.Fatal(err)
		}
		sum := aggregate(t, query.ResultSum, divide)
		assertRows(t, ratios(t, aggregatePlan(t, Filter(t, source, empty), sum)), [][]any{{nil}})
		nullSource := Filter(t, source, query.NewCondition(id, query.LookupExact, query.Integer(4)))
		assertRows(t, ratios(t, aggregatePlan(t, nullSource, sum)), [][]any{{nil}})
		condition, err := query.NewExpression(query.NewCondition(id, query.LookupExact, query.Integer(4)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, divide)
		if err != nil {
			t.Fatal(err)
		}
		nullPeriod, err := query.NullExpression(query.FieldDuration)
		if err != nil {
			t.Fatal(err)
		}
		chosen, err := query.CaseScalar(nullPeriod, branch)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(t, ordered, result(t, chosen)), [][]any{{nil}, {nil}, {nil}, {nil}, {nil}})
	})

	t.Run("full_domain_accumulation_and_invalid_intermediate", func(t *testing.T) {
		reset(t)
		large := Arithmetic(t, query.ArithmeticMultiply, Literal(t, query.Duration(duration.FromMicroseconds(math.MaxInt64))), constant(9))
		negative, err := query.NegateScalar(large)
		if err != nil {
			t.Fatal(err)
		}
		condition, err := query.NewExpression(query.NewCondition(id, query.LookupLessThan, query.Integer(3)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, large)
		if err != nil {
			t.Fatal(err)
		}
		cancelling, err := query.CaseScalar(negative, branch)
		if err != nil {
			t.Fatal(err)
		}
		four := Filter(t, source, query.NewCondition(id, query.LookupLessThan, query.Integer(5)))
		assertRows(t, ratios(t, aggregatePlan(t, four, aggregate(t, query.ResultSum, cancelling), aggregate(t, query.ResultAvg, cancelling))), [][]any{{float64(0), float64(0)}})
		// SUM may cancel or AVG may reduce a larger accumulator. Each operand
		// still fits the model; the final mean remains 9*MaxInt64 exactly.
		actual, err := read(backend, ratios(t, aggregatePlan(t, four, aggregate(t, query.ResultAvg, large))))
		if err != nil || len(actual) != 1 || actual[0][0] != float64(math.MaxInt64)*9 {
			t.Fatal("large mean was limited to the accumulator's range", actual, err)
		}
		invalid := Arithmetic(t, query.ArithmeticMultiply, large, constant(2))
		hidden := Arithmetic(t, query.ArithmeticMultiply, invalid, constant(0))
		if rows, err := read(backend, project(t, source, result(t, hidden))); err == nil || rows != nil {
			t.Fatal("zero hid an invalid Duration node", rows, err)
		} else {
			assertNative(t, err)
		}
		if rows, err := read(backend, aggregatePlan(t, four, aggregate(t, query.ResultSum, large))); err == nil || rows != nil {
			t.Fatal("out-of-domain completed sum was published", rows, err)
		} else {
			assertNative(t, err)
		}
	})

	t.Run("instant_boundary_rounds_the_exact_sum", func(t *testing.T) {
		reset(t)
		first := Filter(t, source, query.NewCondition(id, query.LookupExact, query.Integer(1)))
		origin := time.Date(2500, 1, 1, 0, 0, 0, 1000, time.UTC)
		instant := Literal(t, query.DateTime(origin))
		half := Arithmetic(t, query.ArithmeticDivide, one, constant(2))
		forward := Arithmetic(t, query.ArithmeticAdd, instant, half)
		reverse := Arithmetic(t, query.ArithmeticAdd, half, instant)
		backward := Arithmetic(t, query.ArithmeticSubtract, instant, half)
		values, err := read(backend, project(t, first, result(t, forward), result(t, reverse), result(t, backward)))
		if err != nil || len(values) != 1 {
			t.Fatal(values, err)
		}
		for index, want := range []time.Time{origin.Add(time.Microsecond), origin.Add(time.Microsecond), origin.Add(-time.Microsecond)} {
			if postgres {
				got, ok := values[0][index].(time.Time)
				if !ok || !got.Equal(want) {
					t.Fatal("large epoch rounded its period early", values[0][index], want)
				}
			} else if values[0][index] != want.Format("2006-01-02 15:04:05.000000") {
				t.Fatal("large epoch rounded its period early", values[0][index], want)
			}
		}
	})

	t.Run("null_case_filter_and_corrupt_operands", func(t *testing.T) {
		reset(t)
		exec(t, "UPDATE "+table+" SET period="+periodSQL("1")+" WHERE id=4")
		condition, err := query.NewExpression(query.NewCondition(id, query.LookupLessThan, query.Integer(4)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, divide)
		if err != nil {
			t.Fatal(err)
		}
		chosen, err := query.CaseScalar(one, branch)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, project(t, ordered, result(t, chosen)))
		if err != nil || len(rows) != 5 {
			t.Fatal("unselected zero divisor was evaluated", rows, err)
		}
		sum, err := query.ScalarAggregateResult(query.ResultSum, divide)
		if err != nil {
			t.Fatal(err)
		}
		sum, err = sum.WithFilter(condition)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewAggregateResult(sum)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = read(backend, plan)
		if err != nil || len(rows) != 1 {
			t.Fatal("FILTER evaluated an excluded zero divisor", rows, err)
		}
		invalidTable := quote("scaling_invalid")
		exec(t, "CREATE TABLE "+invalidTable+" (id BIGINT PRIMARY KEY, period "+periodType+", floating DOUBLE PRECISION)")
		badFactor := "'invalid'"
		if postgres {
			badFactor = "'Infinity'::double precision"
		}
		exec(t, "INSERT INTO "+invalidTable+" VALUES(1,NULL,"+badFactor+")")
		floating := query.NewFieldRef("floating", "floating", query.FieldFloat, true)
		invalidSource := query.NewPlan("scaling_invalid", []query.FieldRef{id, period, floating})
		scaled := Arithmetic(t, query.ArithmeticMultiply, Field(t, period), Field(t, floating))
		if values, err := read(backend, project(t, invalidSource, result(t, scaled))); err == nil || values != nil {
			t.Fatal("NULL hid a corrupt factor", values, err)
		} else {
			assertNative(t, err)
		}
		badPeriod := "X'4744520101'"
		if postgres {
			badPeriod = "INTERVAL '1 month'"
		}
		exec(t, "UPDATE "+table+" SET period="+badPeriod+",factor=1 WHERE id=4")
		if values, err := read(backend, project(t, ordered, result(t, divide))); err == nil || values != nil {
			t.Fatal("invalid stored period passed as a query fraction", values, err)
		} else {
			assertNative(t, err)
		}

	})
}
