package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
)

// CheckCalendarArithmetic uses physical rows and literal calendar expectations.
// It tests SQL execution, ownership and storage; generated consumers separately
// check the public model APIs and independent Django observations.
func CheckCalendarArithmetic(t *testing.T, backend interface {
	Backend
	db.Queryer
}, database *sql.DB, quote func(string) string, postgres bool, assertNative func(*testing.T, error)) {
	t.Helper()
	ctx := t.Context()
	const name = "calendar_arithmetic_values"
	table := quote(name)
	day := query.NewFieldRef("day", "day", query.FieldDate, true)
	wall := query.NewFieldRef("wall", "wall", query.FieldTime, true)
	at := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	period := query.NewFieldRef("period", "period", query.FieldDuration, true)
	marker := query.NewFieldRef("marker", "marker", query.FieldInteger, false)
	source := query.NewPlan(name, []query.FieldRef{ID, day, wall, at, period, marker})
	ordered := source.WithOrderings(query.NewOrdering(ID, query.Ascending))
	first := Filter(t, source, query.NewCondition(ID, query.LookupExact, query.Integer(1)))
	exec := func(t *testing.T, statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(value string) string { return "'" + value + "'" }
	periodSQL := func(value string) string { return value }
	periodOutput := func(value int64) any {
		if postgres {
			return duration.FromMicroseconds(value).String()
		}
		return value
	}
	instantType, periodType := "DATETIME", "BIGINT"
	if postgres {
		instantType, periodType = "TIMESTAMPTZ", "INTERVAL"
		stamp = func(value string) string { return "TIMESTAMPTZ '" + value + "+00'" }
		periodSQL = func(value string) string { return "INTERVAL '" + value + " microseconds'" }
	}
	exec(t, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, day DATE, wall TIME, at "+instantType+", period "+periodType+", marker BIGINT NOT NULL CHECK(marker >= 0))")
	reset := func(t *testing.T) {
		t.Helper()
		exec(t, "DELETE FROM "+table)
		exec(t, "INSERT INTO "+table+" VALUES(1,'2000-02-29','23:59:59.999999',"+stamp("2000-03-01 00:00:00.000001")+","+periodSQL("2")+",0),(2,NULL,NULL,NULL,NULL,0),(3,'2020-03-08','00:00:00',"+stamp("2020-03-09 00:00:00.000000")+","+periodSQL("-86400000001")+",0)")
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
		var values [][]any
		for rows.Next() {
			row := make([]any, len(plan.ResultShape().Expressions()))
			pointers := make([]any, len(row))
			for index := range row {
				pointers[index] = &row[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			for index, value := range row {
				if instant, ok := value.(time.Time); ok {
					row[index] = instant.UTC().Format(temporal.SQLiteLayout)
				}
			}
			values = append(values, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		return values, nil
	}
	result := func(value query.ScalarExpression) query.ResultExpression {
		r, err := query.ScalarResult(value)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	project := func(plan query.Plan, values ...query.ScalarExpression) query.Plan {
		selections := make([]query.ResultExpression, len(values))
		for index, value := range values {
			selections[index] = result(value)
		}
		shape, err := query.NewProjectionResult(selections...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	assertRows := func(t *testing.T, plan query.Plan, want [][]any) {
		t.Helper()
		got, err := read(backend, plan)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("native calendar result differs", got, want, err)
		}
	}
	snapshot := func(t *testing.T) [][]any {
		t.Helper()
		columns := "id,CAST(day AS TEXT),CAST(wall AS TEXT),CAST(at AS TEXT),CAST(period AS TEXT),marker"
		width := 6
		if !postgres {
			columns += ",typeof(day),typeof(wall),typeof(at),typeof(period)"
			width += 4
		}
		rows, err := database.QueryContext(ctx, "SELECT "+columns+" FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var values [][]any
		for rows.Next() {
			row, pointers := make([]any, width), make([]any, width)
			for index := range row {
				pointers[index] = &row[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return values
	}
	add := func(left, right query.ScalarExpression) query.ScalarExpression {
		return Arithmetic(t, query.ArithmeticAdd, left, right)
	}
	sub := func(left, right query.ScalarExpression) query.ScalarExpression {
		return Arithmetic(t, query.ArithmeticSubtract, left, right)
	}
	dayValue, wallValue, atValue, periodValue := Field(t, day), Field(t, wall), Field(t, at), Field(t, period)
	zeroClock := Literal(t, query.Time(clock.Time{}))
	one := Literal(t, query.Duration(duration.FromMicroseconds(1)))
	start := add(dayValue, sub(wallValue, zeroClock))
	shifted := add(start, periodValue)

	t.Run("utc_date_shift_clock_wrap_and_signed_difference", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		assertRows(t, project(ordered, add(dayValue, periodValue), add(periodValue, dayValue), sub(dayValue, periodValue), add(wallValue, periodValue), add(periodValue, wallValue), sub(wallValue, periodValue)), [][]any{
			{"2000-02-29 00:00:00.000002", "2000-02-29 00:00:00.000002", "2000-02-28 23:59:59.999998", "00:00:00.000001", "00:00:00.000001", "23:59:59.999997"},
			{nil, nil, nil, nil, nil, nil},
			{"2020-03-06 23:59:59.999999", "2020-03-06 23:59:59.999999", "2020-03-09 00:00:00.000001", "23:59:59.999999", "23:59:59.999999", "00:00:00.000001"},
		})
		assertRows(t, project(ordered, start, shifted, sub(atValue, dayValue), sub(dayValue, atValue), sub(wallValue, zeroClock), sub(zeroClock, wallValue)), [][]any{
			{"2000-02-29 23:59:59.999999", "2000-03-01 00:00:00.000001", periodOutput(86400000001), periodOutput(-86400000001), periodOutput(86399999999), periodOutput(-86399999999)},
			{nil, nil, nil, nil, nil, nil},
			{"2020-03-08 00:00:00.000000", "2020-03-06 23:59:59.999999", periodOutput(86400000000), periodOutput(-86400000000), periodOutput(0), periodOutput(0)},
		})
		minimum := Literal(t, query.Date(calendar.Date{Year: 1, Month: 1, Day: 1}))
		maximum := Literal(t, query.Date(calendar.Date{Year: 9999, Month: 12, Day: 31}))
		assertRows(t, project(first, sub(maximum, minimum)), [][]any{{periodOutput(315537811200000000)}})
		if !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("calendar reads changed physical storage")
		}
	})

	t.Run("fractional_rounding_and_full_duration_clock_domain", func(t *testing.T) {
		reset(t)
		half := Arithmetic(t, query.ArithmeticDivide, one, Literal(t, query.Integer(2)))
		odd := Literal(t, query.Time(clock.Time{Microsecond: 1}))
		assertRows(t, project(first, add(wallValue, half), add(zeroClock, half), add(odd, half), sub(odd, half), sub(zeroClock, half), add(dayValue, half)), [][]any{{"00:00:00", "00:00:00", "00:00:00.000002", "00:00:00", "00:00:00", "2000-02-29 00:00:00.000000"}})
		wholeDays := Arithmetic(t, query.ArithmeticMultiply, Literal(t, query.Duration(duration.Duration{Days: 1})), Literal(t, query.Integer(999999999)))
		maximum := add(wholeDays, Literal(t, query.Duration(duration.FromMicroseconds(86399999999))))
		assertRows(t, project(first, add(zeroClock, maximum), sub(zeroClock, maximum)), [][]any{{"23:59:59.999999", "00:00:00.000001"}})
		invalid := add(maximum, one)
		for _, value := range []query.ScalarExpression{add(zeroClock, invalid), sub(zeroClock, invalid)} {
			if rows, err := read(backend, project(first, value)); err == nil || rows != nil {
				t.Fatal("clock modulo hid an invalid Duration", rows, err)
			} else {
				assertNative(t, err)
			}
		}
	})

	t.Run("selected_stage_order_filter_and_group", func(t *testing.T) {
		reset(t)
		stage, scope, err := query.NewDerivedPlan(project(ordered, start, Field(t, ID)))
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
		condition, err := query.NewScalarCondition(selected, query.LookupLessThan, query.DateTime(time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)))
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(Filter(t, stage, condition), add(selected, one)), [][]any{{"2000-03-01 00:00:00.000000"}})
		sum, err := query.ScalarAggregateResult(query.ResultSum, sub(wallValue, zeroClock))
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(marker)}, []query.ResultExpression{sum})
		if err != nil {
			t.Fatal(err)
		}
		grouped, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, grouped, [][]any{{int64(0), periodOutput(86399999999)}})
		ordering, err := query.NewResultOrdering(result(start), query.Descending)
		if err != nil {
			t.Fatal(err)
		}
		ordering, err = ordering.WithNulls(query.NullsLast)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(source.WithOrderings(ordering), Field(t, ID)), [][]any{{int64(3)}, {int64(1)}, {int64(2)}})
	})

	t.Run("native_mean_and_fraction_provenance_at_clock_boundary", func(t *testing.T) {
		reset(t)
		exec(t, "UPDATE "+table+" SET period="+periodSQL("0")+" WHERE id=1")
		exec(t, "UPDATE "+table+" SET period="+periodSQL("1")+" WHERE id=3")
		average, err := query.AvgResult(query.FieldResult(period))
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewAggregateResult(average)
		if err != nil {
			t.Fatal(err)
		}
		input, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		stage, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := scope.Column(0)
		if err != nil {
			t.Fatal(err)
		}
		mean, err := query.SelectedExpression(ref)
		if err != nil {
			t.Fatal(err)
		}
		fraction := Arithmetic(t, query.ArithmeticMultiply, mean, Literal(t, query.Integer(1)))
		odd := Literal(t, query.Time(clock.Time{Microsecond: 1}))
		plan := project(stage, add(odd, mean), add(odd, fraction))
		want := "00:00:00.000002"
		if postgres {
			// Native AVG(INTERVAL) owns its existing rounding. Observe its
			// clock result outside GoDj, then require both provenance paths
			// to retain that value; do not substitute an ideal rational AVG.
			if err := database.QueryRowContext(ctx, "SELECT (TIME '00:00:00.000001' + AVG(period))::text FROM "+table).Scan(&want); err != nil {
				t.Fatal(err)
			}
		}
		assertRows(t, plan, [][]any{{want, want}})
		exec(t, "UPDATE "+table+" SET period=NULL")
		assertRows(t, plan, [][]any{{nil, nil}})
	})

	badDate, badClock := "'invalid-date'", "'24:00:00'"
	if postgres {
		badDate = "DATE '10000-01-01'"
	}
	for _, test := range []struct {
		name, column, invalid string
		expression            query.ScalarExpression
		target                query.FieldRef
	}{
		{"date", "day", badDate, add(dayValue, periodValue), at},
		{"clock", "wall", badClock, add(wallValue, periodValue), wall},
	} {
		t.Run("corrupt_"+test.name+"_before_null_and_atomic_write", func(t *testing.T) {
			reset(t)
			exec(t, "UPDATE "+table+" SET "+test.column+"="+test.invalid+",period=NULL WHERE id=3")
			before := snapshot(t)
			if rows, err := read(backend, project(ordered, test.expression)); err == nil || rows != nil {
				t.Fatal("NULL hid corrupt calendar input or exposed partial rows", rows, err)
			} else {
				assertNative(t, err)
			}
			update := Plan(t, source, Assignment(t, marker, add(Field(t, marker), Literal(t, query.Integer(1)))), Assignment(t, test.target, test.expression))
			if count, err := backend.QueryUpdate(ctx, update); err == nil || count != 0 {
				t.Fatal("late corruption left partial writes", count, err)
			} else {
				assertNative(t, err)
			}
			if !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("failed calendar arithmetic changed storage")
			}
		})
	}

	t.Run("case_and_filter_exclude_invalid_operands", func(t *testing.T) {
		reset(t)
		exec(t, "UPDATE "+table+" SET wall="+badClock+" WHERE id=3")
		before := snapshot(t)
		condition, err := query.NewExpression(query.NewCondition(ID, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, add(wallValue, periodValue))
		if err != nil {
			t.Fatal(err)
		}
		chosen, err := query.CaseScalar(zeroClock, branch)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, project(ordered, chosen), [][]any{{"00:00:00.000001"}, {"00:00:00"}, {"00:00:00"}})
		sum, err := query.ScalarAggregateResult(query.ResultSum, sub(wallValue, zeroClock))
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
		filtered, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, filtered, [][]any{{periodOutput(86399999999)}})
		if !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("lazy expressions changed storage")
		}
	})

	t.Run("date_boundaries_and_invalid_intermediates", func(t *testing.T) {
		reset(t)
		minimum := Literal(t, query.Date(calendar.Date{Year: 1, Month: 1, Day: 1}))
		maximum := Literal(t, query.Date(calendar.Date{Year: 9999, Month: 12, Day: 31}))
		pastEnd := add(maximum, Literal(t, query.Duration(duration.Duration{Days: 1})))
		missing, err := query.NullExpression(query.FieldDuration)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []query.ScalarExpression{sub(minimum, one), pastEnd, sub(pastEnd, one), add(pastEnd, missing)} {
			if rows, err := read(backend, project(first, value)); err == nil || rows != nil {
				t.Fatal("a later operation hid a calendar range failure", rows, err)
			} else {
				assertNative(t, err)
			}
		}
	})

	t.Run("same_row_update_rollback_and_cancellation", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		update := Plan(t, source, Assignment(t, at, shifted), Assignment(t, period, sub(atValue, dayValue)), Assignment(t, wall, add(wallValue, periodValue)))
		sentinel := errors.New("calendar arithmetic rollback")
		var escaped db.QueryUpdater
		err := backend.Atomic(ctx, func(session db.Session) error {
			escaped = session.(db.QueryUpdater)
			if count, err := escaped.QueryUpdate(ctx, update); err != nil || count != 3 {
				t.Fatal("calendar assignments failed", count, err)
			}
			values, err := read(session.(db.Queryer), project(Filter(t, source, query.NewCondition(ID, query.LookupExact, query.Integer(1))), Field(t, at), Field(t, period), Field(t, wall)))
			if err != nil || !reflect.DeepEqual(values, [][]any{{"2000-03-01 00:00:00.000001", periodOutput(86400000001), "00:00:00.000001"}}) {
				t.Fatal("assignments read a partially updated row", values, err)
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("rollback changed calendar values", err)
		}
		if count, err := escaped.QueryUpdate(ctx, update); err == nil || count != 0 {
			t.Fatal("expired writer remained usable", count, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(canceled, update); !errors.Is(err, context.Canceled) || count != 0 || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("canceled calendar update changed storage", count, err)
		}
		if count, err := backend.QueryUpdate(ctx, update); err != nil || count != 3 {
			t.Fatal("connection could not be reused after rollback", count, err)
		}
	})
}
