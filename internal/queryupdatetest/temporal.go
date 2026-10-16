package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

// CheckTemporal uses independently seeded native rows and checks the common
// AST through real reads and same-row writes, including corrupt external data.
func CheckTemporal(t *testing.T, backend interface {
	Backend
	db.Queryer
}, database *sql.DB, quote func(string) string, postgres bool, assertNative func(*testing.T, error)) {
	t.Helper()
	ctx := t.Context()
	const name = "temporal_expression_values"
	table := quote(name)
	at := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	otherAt := query.NewFieldRef("other_at", "other_at", query.FieldDateTime, true)
	span := query.NewFieldRef("span", "span", query.FieldDuration, true)
	otherSpan := query.NewFieldRef("other_span", "other_span", query.FieldDuration, true)
	marker := query.NewFieldRef("marker", "marker", query.FieldInteger, false)
	source := query.NewPlan(name, []query.FieldRef{ID, at, otherAt, span, otherSpan, marker})
	exec := func(statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(text string) string {
		if postgres {
			return "TIMESTAMPTZ '" + text + "+00'"
		}
		return "'" + text + "'"
	}
	period := func(text string) string {
		if postgres {
			return "INTERVAL '" + text + " microseconds'"
		}
		return text
	}
	timeType, durationType := "DATETIME", "INTEGER"
	if postgres {
		timeType, durationType = "TIMESTAMPTZ", "INTERVAL"
	}
	exec("CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, at " + timeType + ", other_at " + timeType + ", span " + durationType + ", other_span " + durationType + ", marker BIGINT NOT NULL CHECK(marker >= 0))")
	reset := func() {
		exec("DELETE FROM " + table)
		exec("INSERT INTO " + table + " VALUES (1," + stamp("9999-01-01 00:00:00.000001") + "," + stamp("0001-01-01 00:00:00.000000") + "," + period("2") + "," + period("3") + ",0),(2,NULL,NULL,NULL,NULL,0)")
	}
	snapshot := func() [][]any {
		t.Helper()
		rows, err := database.QueryContext(ctx, "SELECT id,CAST(at AS TEXT),CAST(other_at AS TEXT),CAST(span AS TEXT),CAST(other_span AS TEXT),marker FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var values [][]any
		for rows.Next() {
			value := make([]any, 6)
			if err := rows.Scan(&value[0], &value[1], &value[2], &value[3], &value[4], &value[5]); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return values
	}
	project := func(plan query.Plan, expressions ...query.ScalarExpression) query.Plan {
		t.Helper()
		values := make([]query.ResultExpression, len(expressions))
		for index, expression := range expressions {
			var err error
			values[index], err = query.ScalarResult(expression)
			if err != nil {
				t.Fatal(err)
			}
		}
		shape, err := query.NewProjectionResult(values...)
		if err != nil {
			t.Fatal(err)
		}
		result, err := plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		return result
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
			destinations := make([]any, len(row))
			for index := range row {
				destinations[index] = &row[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				return nil, err
			}
			values = append(values, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		return values, nil
	}
	instant := func(raw any, expected string) {
		t.Helper()
		want, err := time.Parse("2006-01-02 15:04:05.000000", expected)
		if err != nil {
			t.Fatal(err)
		}
		if postgres {
			got, ok := raw.(time.Time)
			if !ok || !got.Equal(want) {
				t.Fatal("native instant changed", raw, expected)
			}
		} else if raw != expected {
			t.Fatal("canonical instant changed", raw, expected)
		}
	}
	elapsed := func(raw any, micros int64) {
		t.Helper()
		var want any = micros
		if postgres {
			want = duration.FromMicroseconds(micros)
		}
		if raw != want {
			t.Fatal("native exact Duration changed", raw, want)
		}
	}
	add := func(left, right query.ScalarExpression) query.ScalarExpression {
		return Arithmetic(t, query.ArithmeticAdd, left, right)
	}
	subtract := func(left, right query.ScalarExpression) query.ScalarExpression {
		return Arithmetic(t, query.ArithmeticSubtract, left, right)
	}
	one := Literal(t, query.Duration(duration.FromMicroseconds(1)))
	shift := add(Field(t, at), Field(t, span))
	difference := subtract(Field(t, at), Field(t, otherAt))
	ordered := source.WithOrderings(query.NewOrdering(ID, query.Ascending))

	t.Run("native_reads_and_same_row_assignment", func(t *testing.T) {
		reset()
		before := snapshot()
		negative, err := query.NegateScalar(Field(t, span))
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, project(ordered, shift, add(one, Field(t, at)), difference, add(Field(t, span), Field(t, otherSpan)), negative))
		if err != nil || len(rows) != 2 {
			t.Fatal("temporal projection failed", rows, err)
		}
		instant(rows[0][0], "9999-01-01 00:00:00.000003")
		instant(rows[0][1], "9999-01-01 00:00:00.000002")
		elapsed(rows[0][2], 315506361600000001)
		elapsed(rows[0][3], 5)
		elapsed(rows[0][4], -2)
		for _, value := range rows[1] {
			if value != nil {
				t.Fatal("NULL became a zero temporal value", value)
			}
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("read changed native storage")
		}
		count, err := backend.QueryUpdate(ctx, Plan(t, source, Assignment(t, at, shift), Assignment(t, span, difference)))
		if err != nil || count != 2 {
			t.Fatal("temporal assignment failed", count, err)
		}
		rows, err = read(backend, project(ordered, Field(t, at), Field(t, span)))
		if err != nil || len(rows) != 2 {
			t.Fatal(err)
		}
		instant(rows[0][0], "9999-01-01 00:00:00.000003")
		elapsed(rows[0][1], 315506361600000001) // Both assignments use original row values.
	})

	type failure struct {
		name, mutation string
		target         query.FieldRef
		expression     query.ScalarExpression
	}
	nullTime, err := query.NullExpression(query.FieldDateTime)
	if err != nil {
		t.Fatal(err)
	}
	nullDuration, err := query.NullExpression(query.FieldDuration)
	if err != nil {
		t.Fatal(err)
	}
	badTime, badPeriod, periodMaximum := "'not-a-time'", "'not-a-period'", "9223372036854775807"
	if postgres {
		badTime, badPeriod, periodMaximum = "TIMESTAMPTZ 'infinity'", "INTERVAL '1 month'", "INTERVAL '999999999 days 23:59:59.999999'"
	}
	failures := []failure{
		{"bad_datetime_with_null", "at=" + badTime, at, add(Field(t, at), nullDuration)},
		{"bad_duration_with_null", "span=" + badPeriod, at, add(nullTime, Field(t, span))},
		{"bad_datetime_difference", "at=" + badTime, span, subtract(Field(t, at), nullTime)},
		{"bad_duration_sum", "span=" + badPeriod, span, add(nullDuration, Field(t, span))},
		{"datetime_intermediate_range", "at=" + stamp("9999-12-31 23:59:59.999999"), at, subtract(add(Field(t, at), one), one)},
		{"duration_intermediate_range", "span=" + periodMaximum, span, subtract(add(Field(t, span), one), one)},
	}
	if postgres {
		for _, test := range []struct{ name, value string }{{"year", "1 year"}, {"too_large", "1000000000 days"}, {"too_small", "-999999999 days -1 microsecond"}, {"infinity", "infinity"}} {
			failures = append(failures, failure{"duration_" + test.name, "span=INTERVAL '" + test.value + "'", span, add(Field(t, span), nullDuration)})
		}
		failures = append(failures, failure{"datetime_year_range", "at=TIMESTAMPTZ '10000-01-01 00:00:00+00'", at, add(Field(t, at), nullDuration)})
	} else {
		failures = append(failures, failure{"duration_real_storage", "span=0.5", span, add(Field(t, span), nullDuration)})
	}
	for _, test := range failures {
		t.Run(test.name, func(t *testing.T) {
			reset()
			exec("UPDATE " + table + " SET " + test.mutation + " WHERE id=2")
			before := snapshot()
			if values, err := read(backend, project(ordered, test.expression)); err == nil || values != nil {
				t.Fatal("invalid source or intermediate was hidden", values, err)
			} else {
				assertNative(t, err)
			}
			plan := Plan(t, source, Assignment(t, marker, add(Field(t, marker), Literal(t, query.Integer(1)))), Assignment(t, test.target, test.expression))
			if count, err := backend.QueryUpdate(ctx, plan); err == nil || count != 0 {
				t.Fatal("invalid UPDATE reported success", count, err)
			} else {
				assertNative(t, err)
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("failing read or later UPDATE row changed storage")
			}
		})
	}

	t.Run("case_and_filter_exclude_unexecuted_corruption", func(t *testing.T) {
		reset()
		exec("UPDATE " + table + " SET span=" + badPeriod + " WHERE id=2")
		before := snapshot()
		condition, err := query.NewExpression(query.NewCondition(ID, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, add(Field(t, span), one))
		if err != nil {
			t.Fatal(err)
		}
		selected, err := query.CaseScalar(nullDuration, branch)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, project(ordered, selected))
		if err != nil || len(rows) != 2 || rows[1][0] != nil {
			t.Fatal("unexecuted CASE branch was evaluated", rows, err)
		}
		elapsed(rows[0][0], 3)
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, add(Field(t, span), one))
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
		if err != nil || len(rows) != 1 {
			t.Fatal("FILTER evaluated excluded data", rows, err)
		}
		elapsed(rows[0][0], 3)
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("conditional reads changed storage")
		}
	})

	t.Run("transaction_and_cancellation_rollback", func(t *testing.T) {
		reset()
		before := snapshot()
		plan := Plan(t, source, Assignment(t, at, shift), Assignment(t, span, add(Field(t, span), one)))
		sentinel := errors.New("temporal rollback")
		var escaped db.QueryUpdater
		err := backend.Atomic(ctx, func(session db.Session) error {
			escaped = session.(db.QueryUpdater)
			if count, err := escaped.QueryUpdate(ctx, plan); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("transaction rollback lost temporal values", err)
		}
		if count, err := escaped.QueryUpdate(ctx, plan); err == nil || count != 0 {
			t.Fatal("expired temporal updater escaped its scope", count, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(canceled, plan); !errors.Is(err, context.Canceled) || count != 0 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("canceled temporal update changed storage", count, err)
		}
	})

	t.Run("native_mean_survives_stages_and_reaggregation", func(t *testing.T) {
		reset()
		exec("UPDATE " + table + " SET span=" + period("0") + " WHERE id=1")
		exec("UPDATE " + table + " SET span=" + period("1") + " WHERE id=2")
		before := snapshot()
		average, err := query.AvgResult(query.FieldResult(span))
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
		negative, err := query.NegateScalar(add(mean, one))
		if err != nil {
			t.Fatal(err)
		}
		far := Literal(t, query.DateTime(time.Date(9999, 1, 1, 0, 0, 0, 1000, time.UTC)))
		rows, err := read(backend, project(stage, negative, add(far, mean), add(mean, far)))
		if err != nil || len(rows) != 1 {
			t.Fatal("native mean was rounded or rejected before arithmetic", rows, err)
		}
		var expected any = float64(-1.5)
		wantTime := "9999-01-01 00:00:00.000002"
		if postgres {
			// Native INTERVAL division rounds within PostgreSQL. Observe it
			// independently rather than assuming SQLite's fractional result.
			var micros int64
			var shifted time.Time
			if err := database.QueryRowContext(ctx, "SELECT CAST(EXTRACT(EPOCH FROM -(AVG(span) + INTERVAL '1 microsecond')) * 1000000 AS BIGINT), TIMESTAMPTZ '9999-01-01 00:00:00.000001+00' + AVG(span) FROM "+table).Scan(&micros, &shifted); err != nil {
				t.Fatal(err)
			}
			expected, wantTime = duration.FromMicroseconds(micros), shifted.UTC().Format("2006-01-02 15:04:05.000000")
		}
		if rows[0][0] != expected {
			t.Fatal("native mean arithmetic changed precision", rows[0][0], expected)
		}
		instant(rows[0][1], wantTime)
		instant(rows[0][2], wantTime)
		for _, kind := range []query.ResultExpressionKind{query.ResultSum, query.ResultAvg, query.ResultMin, query.ResultMax} {
			aggregate, err := query.ScalarAggregateResult(kind, negative)
			if err != nil {
				t.Fatal(err)
			}
			if kind == query.ResultSum || kind == query.ResultAvg {
				aggregate, err = aggregate.WithDistinct()
				if err != nil {
					t.Fatal(err)
				}
			}
			shape, err := query.NewAggregateResult(aggregate)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := stage.WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := read(backend, plan)
			if err != nil || len(rows) != 1 || rows[0][0] != expected {
				t.Fatal("reaggregation lost native Duration precision", kind, rows, err)
			}
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("staged temporal reads changed native data")
		}
	})

	t.Run("constant_aggregate_owns_input_rows", func(t *testing.T) {
		reset()
		// The two source rows both contribute even though the computed
		// operand contains only literals and PostgreSQL's scalar guard SELECTs.
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, add(one, one))
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
		rows, err := read(backend, plan)
		if err != nil || len(rows) != 1 {
			t.Fatal("constant aggregate lost its input", rows, err)
		}
		elapsed(rows[0][0], 4)
	})

	t.Run("aggregate_range_precedes_having", func(t *testing.T) {
		reset()
		exec("UPDATE " + table + " SET span=" + periodMaximum + " WHERE id=1")
		exec("UPDATE " + table + " SET span=" + period("1") + " WHERE id=2")
		before := snapshot()
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, Field(t, span))
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewGroupedResult([]query.ResultExpression{query.FieldResult(marker)}, []query.ResultExpression{aggregate})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		having, err := query.NewGroupExpression(aggregate, query.LookupExact, query.Duration(duration.Duration{}))
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithGroupHaving(having)
		if err != nil {
			t.Fatal(err)
		}
		if rows, err := read(backend, plan); err == nil || rows != nil {
			t.Fatal("HAVING hid an invalid completed Duration aggregate", rows, err)
		} else {
			assertNative(t, err)
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("failed aggregate changed native storage")
		}
	})
}
