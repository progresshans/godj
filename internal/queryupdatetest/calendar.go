package queryupdatetest

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/query"
)

// CheckCalendar exercises the public AST against independent physical rows.
// Generated consumers separately own typed/dynamic API and relation behavior.
func CheckCalendar(t *testing.T, backend interface {
	Backend
	db.Queryer
}, database *sql.DB, quote func(string) string, postgres bool, assertNative func(*testing.T, error)) {
	t.Helper()
	ctx := t.Context()
	const name = "calendar_expression_values"
	table := quote(name)
	at := query.NewFieldRef("at", "at", query.FieldDateTime, true)
	day := query.NewFieldRef("day", "day", query.FieldDate, true)
	wall := query.NewFieldRef("wall", "wall", query.FieldTime, true)
	part := query.NewFieldRef("part", "part", query.FieldInteger, true)
	marker := query.NewFieldRef("marker", "marker", query.FieldInteger, false)
	source := query.NewPlan(name, []query.FieldRef{ID, at, day, wall, part, marker})
	exec := func(t *testing.T, statement string) {
		t.Helper()
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	timeType := "DATETIME"
	stamp := func(text string) string { return "'" + text + "'" }
	if postgres {
		timeType = "TIMESTAMPTZ"
		stamp = func(text string) string { return "TIMESTAMPTZ '" + text + "+00'" }
	}
	exec(t, "CREATE TABLE "+table+" (id BIGINT PRIMARY KEY, at "+timeType+", day DATE, wall TIME, part BIGINT, marker BIGINT NOT NULL CHECK(marker >= 0))")
	reset := func(t *testing.T) {
		t.Helper()
		exec(t, "DELETE FROM "+table)
		exec(t, "INSERT INTO "+table+" VALUES (1,"+stamp("2021-01-01 23:59:59.999999")+",'2000-01-01','12:34:56.999999',10,0),(2,NULL,NULL,NULL,20,0),(3,"+stamp("2000-02-29 00:00:00.000001")+",'2400-02-29','00:00:00.000001',30,0)")
	}
	snapshot := func(t *testing.T) [][]any {
		t.Helper()
		columns := "id,CAST(at AS TEXT),CAST(day AS TEXT),CAST(wall AS TEXT),part,marker"
		if !postgres {
			columns += ",typeof(at),typeof(day),typeof(wall),typeof(part),typeof(marker)"
		}
		rows, err := database.QueryContext(ctx, "SELECT "+columns+" FROM "+table+" ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		count := 6
		if !postgres {
			count = 11
		}
		var values [][]any
		for rows.Next() {
			row := make([]any, count)
			pointers := make([]any, count)
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
	function := func(t *testing.T, kind query.FunctionKind, value query.ScalarExpression) query.ScalarExpression {
		t.Helper()
		result, err := query.FunctionExpression(kind, value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	project := func(t *testing.T, plan query.Plan, values ...query.ScalarExpression) query.Plan {
		t.Helper()
		selections := make([]query.ResultExpression, len(values))
		for index, value := range values {
			var err error
			selections[index], err = query.ScalarResult(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		shape, err := query.NewProjectionResult(selections...)
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
			pointers := make([]any, len(row))
			for index := range row {
				pointers[index] = &row[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				return nil, errors.Join(err, rows.Close())
			}
			values = append(values, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		return values, nil
	}
	dateText := func(t *testing.T, raw any, nativeDate bool) any {
		t.Helper()
		if raw == nil {
			return raw
		}
		if !nativeDate {
			text, ok := raw.(string)
			if !ok {
				t.Fatal("computed SQLite Date lost its text representation", raw)
			}
			return text
		}
		date, ok := raw.(time.Time)
		_, offset := date.Zone()
		if !ok || date.Year() < 1 || date.Year() > 9999 || date.Hour() != 0 || date.Minute() != 0 || date.Second() != 0 || date.Nanosecond() != 0 || offset != 0 {
			t.Fatal("native Date acquired a clock", raw)
		}
		return date.Format("2006-01-02")
	}
	ordered := source.WithOrderings(query.NewOrdering(ID, query.Ascending))
	atValue, dayValue, wallValue := Field(t, at), Field(t, day), Field(t, wall)
	date := function(t, query.FunctionDate, atValue)
	clock := function(t, query.FunctionTime, atValue)
	year := function(t, query.FunctionYear, dayValue)
	second := function(t, query.FunctionSecond, wallValue)

	t.Run("native_parts_and_values", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		values := []query.ScalarExpression{date, clock}
		for _, kind := range []query.FunctionKind{query.FunctionYear, query.FunctionISOYear, query.FunctionQuarter, query.FunctionMonth, query.FunctionWeek, query.FunctionWeekDay, query.FunctionISOWeekDay, query.FunctionDay, query.FunctionHour, query.FunctionMinute, query.FunctionSecond} {
			values = append(values, function(t, kind, atValue))
		}
		for _, kind := range []query.FunctionKind{query.FunctionYear, query.FunctionISOYear, query.FunctionQuarter, query.FunctionMonth, query.FunctionWeek, query.FunctionWeekDay, query.FunctionISOWeekDay, query.FunctionDay} {
			values = append(values, function(t, kind, dayValue))
		}
		for _, kind := range []query.FunctionKind{query.FunctionHour, query.FunctionMinute, query.FunctionSecond} {
			values = append(values, function(t, kind, wallValue))
		}
		rows, err := read(backend, project(t, ordered, values...))
		if err != nil || len(rows) != 3 {
			t.Fatal("calendar functions failed", rows, err)
		}
		rows[0][0] = dateText(t, rows[0][0], postgres)
		want := []any{"2021-01-01", "23:59:59.999999", int64(2021), int64(2020), int64(1), int64(1), int64(53), int64(6), int64(5), int64(1), int64(23), int64(59), int64(59),
			int64(2000), int64(1999), int64(1), int64(1), int64(52), int64(7), int64(6), int64(1), int64(12), int64(34), int64(56)}
		if !reflect.DeepEqual(rows[0], want) {
			t.Fatal("calendar result changed type or value", rows[0], want)
		}
		for _, value := range rows[1] {
			if value != nil {
				t.Fatal("NULL became a zero calendar value", value)
			}
		}
		if dateText(t, rows[2][0], postgres) != "2000-02-29" || rows[2][1] != "00:00:00.000001" || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("extraction lost microseconds/leap day or changed stored rows", rows[2])
		}
	})

	t.Run("same_row_updates", func(t *testing.T) {
		reset(t)
		update := Plan(t, source, Assignment(t, day, date), Assignment(t, wall, clock), Assignment(t, part, function(t, query.FunctionISOYear, dayValue)))
		if count, err := backend.QueryUpdate(ctx, update); err != nil || count != 3 {
			t.Fatal("calendar assignment failed", count, err)
		}
		stored := snapshot(t)
		if stored[0][2] != "2021-01-01" || stored[0][3] != "23:59:59.999999" || stored[0][4] != int64(1999) ||
			stored[1][2] != nil || stored[1][3] != nil || stored[1][4] != nil ||
			stored[2][2] != "2000-02-29" || stored[2][3] != "00:00:00.000001" || stored[2][4] != int64(2400) {
			t.Fatal("assignments used previously assigned values or lost NULL", stored)
		}
	})

	t.Run("truncated_values_and_updates", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		var values []query.ScalarExpression
		for _, kind := range []query.FunctionKind{query.FunctionTruncYear, query.FunctionTruncQuarter, query.FunctionTruncMonth, query.FunctionTruncWeek, query.FunctionTruncDay, query.FunctionTruncHour, query.FunctionTruncMinute, query.FunctionTruncSecond} {
			values = append(values, function(t, kind, atValue))
		}
		for _, kind := range []query.FunctionKind{query.FunctionTruncYear, query.FunctionTruncQuarter, query.FunctionTruncMonth, query.FunctionTruncWeek, query.FunctionTruncDay} {
			values = append(values, function(t, kind, dayValue))
		}
		for _, kind := range []query.FunctionKind{query.FunctionTruncHour, query.FunctionTruncMinute, query.FunctionTruncSecond} {
			values = append(values, function(t, kind, wallValue))
		}
		rows, err := read(backend, project(t, ordered, values...))
		if err != nil || len(rows) != 3 {
			t.Fatal("calendar truncation failed", rows, err)
		}
		for index := range 8 {
			if postgres {
				instant, ok := rows[0][index].(time.Time)
				if !ok {
					t.Fatal("truncated instant lost its native type", rows[0][index])
				}
				rows[0][index] = instant.UTC().Format(temporal.SQLiteLayout)
			}
		}
		for index := 8; index < 13; index++ {
			rows[0][index] = dateText(t, rows[0][index], postgres)
		}
		want := []any{"2021-01-01 00:00:00.000000", "2021-01-01 00:00:00.000000", "2021-01-01 00:00:00.000000", "2020-12-28 00:00:00.000000", "2021-01-01 00:00:00.000000", "2021-01-01 23:00:00.000000", "2021-01-01 23:59:00.000000", "2021-01-01 23:59:59.000000",
			"2000-01-01", "2000-01-01", "2000-01-01", "1999-12-27", "2000-01-01", "12:00:00", "12:34:00", "12:34:56"}
		if !reflect.DeepEqual(rows[0], want) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("truncation changed native domains, boundaries or storage", rows[0], want)
		}
		for _, value := range rows[1] {
			if value != nil {
				t.Fatal("NULL truncation became a calendar zero", value)
			}
		}
		update := Plan(t, source, Assignment(t, at, values[3]), Assignment(t, day, values[11]), Assignment(t, wall, values[15]), Assignment(t, part, year))
		sentinel := errors.New("truncation rollback")
		err = backend.Atomic(ctx, func(session db.Session) error {
			if count, err := session.(db.QueryUpdater).QueryUpdate(ctx, update); err != nil || count != 3 {
				t.Fatal("truncated assignment failed", count, err)
			}
			stored, err := read(session, project(t, ordered, function(t, query.FunctionDate, atValue), dayValue, wallValue, Field(t, part)))
			if err != nil || len(stored) != 3 {
				t.Fatal("truncated write could not be read", stored, err)
			}
			for _, row := range stored {
				// A declared DATE column is decoded as time.Time by both
				// drivers; SQLite's computed date expression returns text.
				row[0], row[1] = dateText(t, row[0], postgres), dateText(t, row[1], true)
			}
			want := [][]any{{"2020-12-28", "1999-12-27", "12:34:56", int64(2000)}, {nil, nil, nil, nil}, {"2000-02-28", "2400-02-28", "00:00:00", int64(2400)}}
			if !reflect.DeepEqual(stored, want) {
				t.Fatal("truncated write changed original-row assignment or NULL semantics", stored)
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("truncated assignments escaped rollback", err)
		}
	})

	badInstant, badDate, badTime := "'2026-02-30 00:00:00.000000'", "'1900-02-29'", "'24:00:00'"
	if postgres {
		badInstant, badDate, badTime = "TIMESTAMPTZ 'infinity'", "DATE '10000-01-01'", "TIME '24:00:00'"
	}
	for _, test := range []struct {
		name, column, invalid string
		value                 query.ScalarExpression
		target                query.FieldRef
	}{
		{"invalid_datetime", "at", badInstant, function(t, query.FunctionYear, atValue), part},
		{"invalid_date", "day", badDate, year, part},
		{"invalid_clock", "wall", badTime, second, part},
		{"truncated_invalid_datetime", "at", badInstant, function(t, query.FunctionTruncYear, atValue), at},
		{"truncated_invalid_date", "day", badDate, function(t, query.FunctionTruncDay, dayValue), day},
		{"truncated_invalid_clock", "wall", badTime, function(t, query.FunctionTruncHour, wallValue), wall},
	} {
		t.Run(test.name, func(t *testing.T) {
			reset(t)
			exec(t, "UPDATE "+table+" SET "+test.column+"="+test.invalid+" WHERE id=3")
			before := snapshot(t)
			if rows, err := read(backend, project(t, ordered, test.value)); err == nil || rows != nil {
				t.Fatal("smaller result hid invalid physical input", rows, err)
			} else {
				assertNative(t, err)
			}
			increment := Arithmetic(t, query.ArithmeticAdd, Field(t, marker), Literal(t, query.Integer(1)))
			update := Plan(t, source, Assignment(t, marker, increment), Assignment(t, test.target, test.value))
			if count, err := backend.QueryUpdate(ctx, update); err == nil || count != 0 {
				t.Fatal("late invalid operand left partial assignments", count, err)
			} else {
				assertNative(t, err)
			}
			if !reflect.DeepEqual(before, snapshot(t)) {
				t.Fatal("failed extraction changed native storage")
			}
		})
	}

	t.Run("unselected_and_filtered_operands", func(t *testing.T) {
		reset(t)
		exec(t, "UPDATE "+table+" SET day="+badDate+" WHERE id=3")
		before := snapshot(t)
		condition, err := query.NewExpression(query.NewCondition(ID, query.LookupExact, query.Integer(1)))
		if err != nil {
			t.Fatal(err)
		}
		branch, _ := query.WhenScalar(condition, year)
		selected, err := query.CaseScalar(Literal(t, query.Integer(0)), branch)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := read(backend, project(t, ordered, selected))
		if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(2000)}, {int64(0)}, {int64(0)}}) {
			t.Fatal("CASE evaluated an unselected function", rows, err)
		}
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, year)
		if err != nil {
			t.Fatal(err)
		}
		aggregate, err = aggregate.WithFilter(condition)
		if err != nil {
			t.Fatal(err)
		}
		shape, _ := query.NewAggregateResult(aggregate)
		plan, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = read(backend, plan)
		if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(2000)}}) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("FILTER evaluated excluded input or changed storage", rows, err)
		}
	})

	t.Run("result_stage_and_literal_aggregate", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		input := project(t, ordered, date)
		stage, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		ref, _ := scope.Column(0)
		selected, _ := query.SelectedExpression(ref)
		rows, err := read(backend, project(t, stage, function(t, query.FunctionYear, selected)))
		if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(2021)}, {nil}, {int64(2000)}}) {
			t.Fatal("function lost selected result domain or row order", rows, err)
		}
		literal := function(t, query.FunctionYear, Literal(t, query.Date(calendar.Date{Year: 2004, Month: 2, Day: 29})))
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, literal)
		if err != nil {
			t.Fatal(err)
		}
		shape, _ := query.NewAggregateResult(aggregate)
		plan, err := source.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		rows, err = read(backend, plan)
		if err != nil || !reflect.DeepEqual(rows, [][]any{{int64(6012)}}) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("constant function aggregate lost its three source rows", rows, err)
		}
	})

	t.Run("invalid_input_precedes_having", func(t *testing.T) {
		reset(t)
		exec(t, "UPDATE "+table+" SET day="+badDate+" WHERE id=3")
		before := snapshot(t)
		zeroed := Arithmetic(t, query.ArithmeticMultiply, year, Literal(t, query.Integer(0)))
		aggregate, err := query.ScalarAggregateResult(query.ResultSum, zeroed)
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
		having, err := query.NewGroupExpression(aggregate, query.LookupExact, query.Integer(1))
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithGroupHaving(having)
		if err != nil {
			t.Fatal(err)
		}
		if rows, err := read(backend, plan); err == nil || rows != nil {
			t.Fatal("multiplication or HAVING hid corrupt calendar input", rows, err)
		} else {
			assertNative(t, err)
		}
		if !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("failed aggregate changed physical data")
		}
	})

	t.Run("transaction_and_cancellation", func(t *testing.T) {
		reset(t)
		before := snapshot(t)
		update := Plan(t, source, Assignment(t, day, date), Assignment(t, wall, clock), Assignment(t, part, year))
		sentinel := errors.New("calendar transaction rollback")
		var escaped db.QueryUpdater
		err := backend.Atomic(ctx, func(session db.Session) error {
			escaped = session.(db.QueryUpdater)
			if count, err := escaped.QueryUpdate(ctx, update); err != nil || count != 3 {
				t.Fatal("transaction calendar assignment failed", count, err)
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("rollback lost calendar values", err)
		}
		if count, err := escaped.QueryUpdate(ctx, update); err == nil || count != 0 {
			t.Fatal("expired calendar writer escaped its scope", count, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if count, err := backend.QueryUpdate(canceled, update); !errors.Is(err, context.Canceled) || count != 0 || !reflect.DeepEqual(before, snapshot(t)) {
			t.Fatal("canceled calendar update changed storage", count, err)
		}
	})
}
