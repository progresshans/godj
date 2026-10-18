package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

// CheckZonedCalendar exercises physical rows, expression composition and native
// failure/rollback on each backend. Expected values are fixed civil examples;
// neither backend supplies the other backend's expectations.
func CheckZonedCalendar(t *testing.T, backend interface {
	Backend
	db.Queryer
}, database *sql.DB, quote func(string) string, postgres bool, assertNative func(*testing.T, error)) {
	t.Helper()
	ctx := t.Context()
	const name = "zoned_calendar_values"
	table := quote(name)
	at := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	day := query.NewFieldRef("day", "day", query.FieldDate, true)
	wall := query.NewFieldRef("wall", "wall", query.FieldTime, true)
	part := query.NewFieldRef("part", "part", query.FieldInteger, true)
	marker := query.NewFieldRef("marker", "marker", query.FieldInteger, false)
	source := query.NewPlan(name, []query.FieldRef{ID, at, day, wall, part, marker})
	ordered := source.WithOrderings(query.NewOrdering(ID, query.Ascending))
	atValue := Field(t, at)
	exec := func(statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	stamp := func(value string) string { return "'" + value + "'" }
	timeType := "DATETIME"
	if postgres {
		timeType = "TIMESTAMPTZ"
		stamp = func(value string) string { return "TIMESTAMPTZ '" + value + "+00'" }
	}
	exec("CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, at " + timeType + ", day DATE, wall TIME, part BIGINT, marker BIGINT NOT NULL CHECK(marker>=0))")
	reset := func(first, third string) {
		t.Helper()
		exec("DELETE FROM " + table)
		exec("INSERT INTO " + table + " VALUES (1," + stamp(first) + ",'2000-01-01','12:34:56',10,0),(2,NULL,NULL,NULL,20,0),(3," + stamp(third) + ",'2000-01-01','12:34:56',30,0)")
	}
	snapshot := func() [][]any {
		t.Helper()
		columns, count := "id,CAST(at AS TEXT),CAST(day AS TEXT),CAST(wall AS TEXT),part,marker", 6
		if !postgres {
			columns += ",typeof(at),typeof(day),typeof(wall),typeof(part),typeof(marker)"
			count += 5
		}
		rows, err := database.QueryContext(ctx, "SELECT "+columns+" FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var values [][]any
		for rows.Next() {
			row, pointers := make([]any, count), make([]any, count)
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
	local := func(function query.FunctionKind, name string, operand query.ScalarExpression) query.ScalarExpression {
		t.Helper()
		zone, err := timezone.Parse(name)
		if err != nil {
			t.Fatal(err)
		}
		value, err := query.FunctionExpressionInZone(function, zone, operand)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	project := func(plan query.Plan, values ...query.ScalarExpression) query.Plan {
		t.Helper()
		expressions := make([]query.ResultExpression, len(values))
		for index, value := range values {
			var err error
			expressions[index], err = query.ScalarResult(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		shape, err := query.NewProjectionResult(expressions...)
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
		expressions := plan.ResultShape().Expressions()
		var values [][]any
		for rows.Next() {
			row, pointers := make([]any, len(expressions)), make([]any, len(expressions))
			for index := range row {
				pointers[index] = &row[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			for index, value := range row {
				if instant, ok := value.(time.Time); ok {
					switch expressions[index].ResultValueKind() {
					case query.FieldDate:
						row[index] = instant.Format("2006-01-02")
					case query.FieldDateTime:
						row[index] = instant.UTC().Format(temporal.SQLiteLayout)
					}
				}
			}
			values = append(values, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		return values, nil
	}

	t.Run("all_functions_and_nulls", func(t *testing.T) {
		reset("2024-12-31 16:34:59.999999", "2024-12-31 16:34:59.999999")
		before := snapshot()
		var expressions []query.ScalarExpression
		for function := query.FunctionDate; function <= query.FunctionTruncSecond; function++ {
			expressions = append(expressions, local(function, "Asia/Seoul", atValue))
		}
		rows, err := read(backend, project(ordered, expressions...))
		want := []any{"2025-01-01", "01:34:59.999999", int64(2025), int64(2025), int64(1), int64(1), int64(1), int64(4), int64(3), int64(1), int64(1), int64(34), int64(59),
			"2024-12-31 15:00:00.000000", "2024-12-31 15:00:00.000000", "2024-12-31 15:00:00.000000", "2024-12-29 15:00:00.000000", "2024-12-31 15:00:00.000000", "2024-12-31 16:00:00.000000", "2024-12-31 16:34:00.000000", "2024-12-31 16:34:59.000000"}
		if err != nil || !reflect.DeepEqual(rows, [][]any{want, make([]any, len(want)), want}) || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("zoned functions changed components, NULL, native domain or storage", rows, err)
		}
	})

	t.Run("dst_non_hour_and_historical_boundaries", func(t *testing.T) {
		for _, test := range []struct {
			zone, input, want string
			function          query.FunctionKind
		}{
			{"America/New_York", "2024-11-03 06:30:59.999999", "2024-11-03 05:00:00.000000", query.FunctionTruncHour},
			{"America/New_York", "2024-11-03 06:30:59.999999", "2024-11-03 05:30:59.000000", query.FunctionTruncSecond},
			{"Australia/Lord_Howe", "2024-10-05 15:45:00.000000", "2024-10-05 15:30:00.000000", query.FunctionTruncHour},
			{"Australia/Lord_Howe", "2024-04-06 15:15:00.000000", "2024-04-06 14:45:00.000000", query.FunctionTruncMinute},
			{"America/Sao_Paulo", "2018-11-04 04:30:00.000000", "2018-11-04 03:00:00.000000", query.FunctionTruncDay},
			{"Europe/Paris", "1900-01-01 00:00:00.000001", "1899-12-31 23:59:39.000000", query.FunctionTruncMinute},
			{"Pacific/Apia", "2011-12-30 10:30:00.000000", "2011-12-31", query.FunctionDate},
			{"Europe/Dublin", "2024-10-27 01:30:00.000000", "2024-10-27 00:00:00.000000", query.FunctionTruncHour},
			{"UTC", "0001-01-01 00:00:00.000000", "0001-01-01 00:00:00.000000", query.FunctionTruncYear},
			{"UTC", "9999-12-31 23:59:59.999999", "9999-12-31 23:59:59.000000", query.FunctionTruncSecond},
			{"America/New_York", "9999-07-01 12:34:56.999999", "9999-07-01 04:00:00.000000", query.FunctionTruncDay},
		} {
			reset(test.input, test.input)
			value := local(test.function, test.zone, atValue)
			rows, err := read(backend, project(ordered, value))
			if err != nil || !reflect.DeepEqual(rows, [][]any{{test.want}, {nil}, {test.want}}) {
				t.Fatalf("%+v: %v, %v", test, rows, err)
			}
		}
	})

	t.Run("group_having_and_followup_keep_calendar_frame", func(t *testing.T) {
		reset("2024-11-03 05:30:59.999999", "2024-11-03 06:30:59.999999")
		before := snapshot()
		key, err := query.ScalarResult(local(query.FunctionTruncHour, "America/New_York", atValue))
		if err != nil {
			t.Fatal(err)
		}
		count := query.CountAllResult()
		shape, err := query.NewGroupedResult([]query.ResultExpression{key}, []query.ResultExpression{count})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		ordering, err := query.NewGroupOrdering(key, query.Ascending, query.NullsLast)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithGroupOrderings(ordering)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, plan)
		if err != nil || !reflect.DeepEqual(rows, [][]any{{"2024-11-03 05:00:00.000000", int64(2)}, {nil, int64(1)}}) {
			t.Fatal("local-hour grouping split a fold or lost NULL", rows, err)
		}
		having, err := query.NewGroupExpression(count, query.LookupExact, query.Integer(2))
		if err != nil {
			t.Fatal(err)
		}
		filtered, err := plan.WithGroupHaving(having)
		if err != nil {
			t.Fatal(err)
		}
		stage, scope, err := query.NewDerivedPlan(filtered)
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
		rows, err = read(backend, project(stage, local(query.FunctionDate, "Asia/Seoul", selected)))
		if err != nil || !reflect.DeepEqual(rows, [][]any{{"2024-11-03"}}) {
			t.Fatal("zoned grouped result lost its instant domain in a followup", rows, err)
		}
		utcKey, err := query.ScalarResult(local(query.FunctionTruncHour, "UTC", atValue))
		if err != nil {
			t.Fatal(err)
		}
		shape, err = query.NewGroupedResult([]query.ResultExpression{key, utcKey}, []query.ResultExpression{count})
		if err != nil {
			t.Fatal(err)
		}
		plan, err = source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		utcOrder, err := query.NewGroupOrdering(utcKey, query.Ascending, query.NullsLast)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithGroupOrderings(ordering, utcOrder)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = read(backend, plan)
		want := [][]any{{"2024-11-03 05:00:00.000000", "2024-11-03 05:00:00.000000", int64(1)}, {"2024-11-03 05:00:00.000000", "2024-11-03 06:00:00.000000", int64(1)}, {nil, nil, int64(1)}}
		if err != nil || !reflect.DeepEqual(rows, want) || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("distinct calendar frames collapsed in GROUP BY or changed storage", rows, err)
		}
	})

	t.Run("same_row_update_and_transaction_rollback", func(t *testing.T) {
		reset("2024-12-31 16:34:59.999999", "2024-12-31 16:34:59.999999")
		before := snapshot()
		update := Plan(t, source,
			Assignment(t, at, local(query.FunctionTruncDay, "Asia/Seoul", atValue)),
			Assignment(t, day, local(query.FunctionDate, "Asia/Seoul", atValue)),
			Assignment(t, wall, local(query.FunctionTime, "Asia/Seoul", atValue)),
			Assignment(t, part, local(query.FunctionHour, "Asia/Seoul", atValue)))
		sentinel := errors.New("zoned calendar rollback")
		err := backend.Atomic(ctx, func(session db.Session) error {
			if count, err := session.(db.QueryUpdater).QueryUpdate(ctx, update); err != nil || count != 3 {
				t.Fatal("zoned update failed", count, err)
			}
			rows, err := read(session, project(ordered, atValue, Field(t, day), Field(t, wall), Field(t, part)))
			want := []any{"2024-12-31 15:00:00.000000", "2025-01-01", "01:34:59.999999", int64(1)}
			if err != nil || !reflect.DeepEqual(rows, [][]any{want, {nil, nil, nil, nil}, want}) {
				t.Fatal("zoned assignments observed another assignment or lost NULL", rows, err)
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("zoned writes escaped rollback", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(canceled, update); err == nil || count != 0 || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("canceled zoned update changed storage", count, err)
		}
	})

	for _, test := range []struct {
		name, zone, value string
		function          query.FunctionKind
	}{
		{"local_underflow", "Etc/GMT+1", "0001-01-01 00:00:00.000000", query.FunctionSecond},
		{"local_overflow", "Asia/Seoul", "9999-12-31 23:59:59.999999", query.FunctionSecond},
		{"truncated_utc_underflow", "Asia/Seoul", "0001-01-01 12:00:00.000000", query.FunctionTruncDay},
		{"corrupt_source", "America/New_York", "", query.FunctionYear},
	} {
		t.Run(test.name, func(t *testing.T) {
			reset("2024-12-31 16:34:59.999999", "2024-12-31 16:34:59.999999")
			invalid := stamp(test.value)
			if test.value == "" {
				invalid = "'2024-02-30 00:00:00.000000'"
				if postgres {
					invalid = "TIMESTAMPTZ 'infinity'"
				}
			}
			exec("UPDATE " + table + " SET at=" + invalid + " WHERE id=3")
			before := snapshot()
			value := local(test.function, test.zone, atValue)
			if rows, err := read(backend, project(ordered, value)); err == nil || rows != nil {
				t.Fatal("small result hid invalid source/civil/UTC range", rows, err)
			} else {
				assertNative(t, err)
			}
			target := part
			if test.function >= query.FunctionTruncYear {
				target = at
			}
			update := Plan(t, source, Assignment(t, marker, Arithmetic(t, query.ArithmeticAdd, Field(t, marker), Literal(t, query.Integer(1)))), Assignment(t, target, value))
			if count, err := backend.QueryUpdate(ctx, update); err == nil || count != 0 {
				t.Fatal("late zoned error accepted partial write", count, err)
			} else {
				assertNative(t, err)
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("failed zoned write changed physical values")
			}
		})
	}

	t.Run("unselected_case_filtered_aggregate_and_selected_scope", func(t *testing.T) {
		reset("2024-12-31 16:34:59.999999", "2024-12-31 16:34:59.999999")
		bad := "'2024-02-30 00:00:00.000000'"
		if postgres {
			bad = "TIMESTAMPTZ 'infinity'"
		}
		exec("UPDATE " + table + " SET at=" + bad + " WHERE id=3")
		before := snapshot()
		year := local(query.FunctionYear, "Asia/Seoul", atValue)
		condition, err := query.NewExpression(query.NewCondition(ID, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(condition, year)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := query.CaseScalar(Literal(t, query.Integer(0)), branch)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, project(ordered, selected))
		if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(2025)}, {int64(0)}, {int64(0)}}) {
			t.Fatal("zoned CASE evaluated excluded input", rows, err)
		}
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, year)
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
		if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(2025)}}) || !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("zoned FILTER evaluated excluded input", rows, err)
		}
		input := project(Filter(t, ordered, query.NewCondition(ID, query.LookupExact, query.Integer(1))), atValue)
		stage, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := scope.Column(0)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := query.SelectedExpression(ref)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = read(backend, project(stage, local(query.FunctionDate, "Asia/Seoul", leaf)))
		if err != nil || !reflect.DeepEqual(rows, [][]any{{"2025-01-01"}}) {
			t.Fatal("zoned operation lost selected-result source", rows, err)
		}
	})
}
