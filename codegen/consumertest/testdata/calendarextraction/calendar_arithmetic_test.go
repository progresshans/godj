package consumer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
)

func observeCalendarArithmetic(t *testing.T, reader db.Session, name string) (any, error) {
	t.Helper()
	f := records.EventFields
	base := records.EventObjects.Using(reader).OrderBy(f.Code.Asc())
	day, clockValue, at := orm.Date(orm.F(f.Day)), orm.Time(orm.F(f.Wall)), orm.DateTime(orm.F(f.At))
	period := duration.FromMicroseconds(3600000001)
	clockPeriod := duration.Duration{Days: 2, Microseconds: 3600000001}
	start := day.AddExpression(clockValue.Difference(clock.Time{}))
	switch name {
	case "arith_date_add":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.Add(period).ScalarExpression))
	case "arith_date_reverse":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Duration(orm.Value[records.Event](period)).AddToDateExpression(day).ScalarExpression))
	case "arith_date_subtract":
		return orm.SelectInto(t.Context(), base.Filter(orm.Not(f.Code.Exact("g"))), calendarProjection(day.Subtract(period).ScalarExpression))
	case "arith_date_difference":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.Difference(date("2000-01-01")).ScalarExpression))
	case "arith_date_instant_difference":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.DifferenceDateTimeExpression(at).ScalarExpression))
	case "arith_instant_date_difference":
		return orm.SelectInto(t.Context(), base, calendarProjection(at.DifferenceDateExpression(day).ScalarExpression))
	case "arith_time_add":
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.Add(clockPeriod).ScalarExpression))
	case "arith_time_reverse":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Duration(orm.Value[records.Event](clockPeriod)).AddToTimeExpression(clockValue).ScalarExpression))
	case "arith_time_subtract":
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.Subtract(clockPeriod).ScalarExpression))
	case "arith_time_difference":
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.Difference(wall("12:00:00")).ScalarExpression))
	case "arith_composed_start":
		return orm.SelectInto(t.Context(), base, calendarProjection(start.ScalarExpression))
	case "arith_filter_start":
		return orm.SelectInto(t.Context(), base.Filter(start.GreaterThanOrEqual(instant("2020-01-01T00:00:00Z")), start.LessThan(instant("2022-01-01T00:00:00Z"))), calendarProjection(start.ScalarExpression))
	case "arith_related_start":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		relation := relations.RecordsEvent.Schedule
		value := orm.Date(relation.Day).AddExpression(orm.Time(relation.Wall).Difference(clock.Time{}))
		return orm.SelectInto(t.Context(), base, calendarProjection(value.ScalarExpression))
	case "arith_collection_start":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		event := relations.PlanningSchedule.Events
		value := orm.Date(event.Day).AddExpression(orm.Time(event.Wall).Difference(clock.Time{}))
		grouped, err := orm.GroupBy(planning.ScheduleObjects.Using(reader), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }),
			orm.Aggregate3(orm.Min(value), orm.Max(value).Where(event.Enabled.Exact(true)), orm.Count(value), func(first, last orm.Optional[time.Time], count int64) map[string]any {
				return map[string]any{"first": optionalCalendar(first), "last": optionalCalendar(last), "count": count}
			}), func(code string, values map[string]any) map[string]any { values["code"] = code; return values })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
	case "arith_aggregate_difference":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate3(orm.Min(clockValue.Difference(clock.Time{})), orm.Max(day.Difference(date("2000-01-01"))), orm.Sum(clockValue.Difference(clock.Time{})).Where(f.Enabled.Exact(true)),
			func(first, last, total orm.Optional[duration.Duration]) map[string]any {
				return map[string]any{"first": optionalCalendar(first), "last": optionalCalendar(last), "total": optionalCalendar(total)}
			}))
	case "arith_case_time":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(clockValue.Subtract(clockPeriod), orm.When(f.Enabled.Exact(true), clockValue.Add(clockPeriod)))))
	case "arith_date_fraction", "arith_time_fraction":
		half := orm.DivideDuration(orm.Value[records.Event](duration.FromMicroseconds(1)), int64(2))
		if name == "arith_date_fraction" {
			return orm.SelectInto(t.Context(), base, calendarProjection(day.AddExpression(half).ScalarExpression))
		}
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.AddExpression(half).ScalarExpression))
	case "arith_invalid_date_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.Add(duration.Duration{}).ScalarExpression))
	case "arith_clock_24_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.Add(duration.Duration{}).ScalarExpression))
	case "arith_unselected_corrupt_date":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(orm.Value[records.Event](instant("2000-01-01T00:00:00Z")), orm.When(f.Code.Exact("never"), day.Add(period)))))
	case "arith_filtered_corrupt_date":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Min(day.Add(period)).Where(orm.Not(f.Code.Exact("a"))), func(first orm.Optional[time.Time]) map[string]any {
			return map[string]any{"first": optionalCalendar(first)}
		}))
	case "update_arithmetic_calendar":
		count, err := base.Update(t.Context(), orm.AssignExpression(f.At, start), orm.AssignExpression(f.Wall, clockValue.Add(duration.FromMicroseconds(1))))
		if err != nil {
			return nil, err
		}
		values, err := base.Fresh().All(t.Context())
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]any, 0, len(values))
		for _, value := range values {
			rows = append(rows, map[string]any{"code": value.Code, "at": pointerCalendar(value.At), "day": pointerCalendar(value.Day), "wall": pointerCalendar(value.Wall), "part": pointerCalendar(value.Part)})
		}
		return map[string]any{"changed": count, "rows": rows}, nil
	default:
		return nil, fmt.Errorf("unknown calendar arithmetic observation %q", name)
	}
}

func checkCalendarArithmetic(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical, seed calendarSeed, before string) {
	t.Helper()
	f := records.EventFields
	day, clockValue, at := orm.Date(orm.F(f.Day)), orm.Time(orm.F(f.Wall)), orm.DateTime(orm.F(f.At))
	start := day.AddExpression(clockValue.Difference(clock.Time{}))
	period := duration.FromMicroseconds(3600000001)
	clockPeriod := duration.Duration{Days: 2, Microseconds: 3600000001}
	t.Run("arithmetic_typed_dynamic_and_domains", func(t *testing.T) {
		assertCalendarParity(t, backend, day.Add(period).ScalarExpression, orm.DynamicF("day").Add(period))
		assertCalendarParity(t, backend, orm.Duration(orm.Value[records.Event](period)).AddToDateExpression(day).ScalarExpression, orm.DynamicValue(period).Add(orm.DynamicF("day")))
		assertCalendarParity(t, backend, day.Difference(date("2000-01-01")).ScalarExpression, orm.DynamicF("day").Subtract(date("2000-01-01")))
		assertCalendarParity(t, backend, day.DifferenceDateTimeExpression(at).ScalarExpression, orm.DynamicF("day").Subtract(orm.DynamicF("at")))
		assertCalendarParity(t, backend, at.DifferenceDateExpression(day).ScalarExpression, orm.DynamicF("at").Subtract(orm.DynamicF("day")))
		assertCalendarParity(t, backend, clockValue.Add(clockPeriod).ScalarExpression, orm.DynamicF("wall").Add(clockPeriod))
		assertCalendarParity(t, backend, clockValue.Subtract(clockPeriod).ScalarExpression, orm.DynamicF("wall").Subtract(clockPeriod))
		assertCalendarParity(t, backend, orm.Duration(orm.Value[records.Event](clockPeriod)).AddToTimeExpression(clockValue).ScalarExpression, orm.DynamicValue(clockPeriod).Add(orm.DynamicF("wall")))
		assertCalendarParity(t, backend, clockValue.Difference(wall("12:00:00")).ScalarExpression, orm.DynamicF("wall").Subtract(wall("12:00:00")))
		assertCalendarParity(t, backend, start.ScalarExpression, orm.DynamicF("day").Add(orm.DynamicF("wall").Subtract(clock.Time{})))
		half := orm.DivideDuration(orm.Value[records.Event](duration.FromMicroseconds(1)), int64(2))
		assertCalendarParity(t, backend, clockValue.AddExpression(half).ScalarExpression, orm.DynamicF("wall").Add(orm.DynamicValue(duration.FromMicroseconds(1)).Divide(int64(2))))
		values, err := orm.SelectInto(t.Context(), records.EventObjects.Using(backend).Filter(f.Code.In("a", "g", "h", "i", "k")).OrderBy(f.Code.Asc()), calendarProjection(start.ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, values, `[{"code":"a","value":"2021-01-01T23:59:59.999999Z"},{"code":"g","value":"0001-01-01T00:00:00.000000Z"},{"code":"h","value":"9999-12-31T23:59:59.999999Z"},{"code":"i","value":null},{"code":"k","value":"2024-03-03T00:00:00.000000Z"}]`)
		maximum := orm.ScaleDuration(orm.Value[records.Event](duration.Duration{Days: 1}), int64(999999999)).Add(duration.FromMicroseconds(86399999999))
		zero := orm.Time(orm.Value[records.Event](clock.Time{}))
		full, err := orm.SelectInto(t.Context(), records.EventObjects.Using(backend).Filter(f.Code.Exact("a")), calendarProjection(zero.SubtractExpression(maximum).ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, full, `[{"code":"a","value":"00:00:00.000001"}]`)
		probe := &calendarReadProbe{calendarBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		for _, expression := range []orm.DynamicExpression{orm.DynamicF("day").Add(orm.DynamicF("wall")), orm.DynamicF("wall").Add(orm.DynamicF("wall")), orm.DynamicValue(period).Subtract(orm.DynamicF("day")), orm.DynamicF("wall").Subtract(orm.DynamicF("at")), orm.DynamicF("day").Multiply(int64(1))} {
			if _, err := api.RecordsEvent.Expression(expression); err == nil {
				t.Fatal("invalid calendar arithmetic inferred an input domain")
			}
		}
		if probe.calls() != 0 {
			t.Fatal("invalid arithmetic reached I/O")
		}
	})
	t.Run("arithmetic_relations_groups_and_stages", func(t *testing.T) {
		value, err := observeCalendar(t, backend, "arith_filter_start")
		check(t, err)
		assertCalendarJSON(t, value, `[{"code":"a","value":"2021-01-01T23:59:59.999999Z"},{"code":"j","value":"2021-01-01T00:30:00.000001Z"}]`)
		value, err = observeCalendar(t, backend, "arith_collection_start")
		check(t, err)
		assertCalendarJSON(t, value, `[{"code":"empty","first":"1900-03-01T00:00:00.000000Z","last":null,"count":1},{"code":"future","first":"0001-01-01T00:00:00.000000Z","last":"2400-02-29T00:00:00.000001Z","count":3},{"code":"north","first":"2000-01-01T00:00:00.000000Z","last":"2021-01-01T23:59:59.999999Z","count":5},{"code":"unused","first":null,"last":null,"count":0}]`)
		probe := &calendarReadProbe{calendarBackend: backend}
		relations, err := project.BindRelations()
		check(t, err)
		related := relations.RecordsEvent.Schedule
		assertCalendarParity(t, backend, orm.Date(related.Day).AddExpression(orm.Time(related.Wall).Difference(clock.Time{})).ScalarExpression, orm.DynamicF("schedule__day").Add(orm.DynamicF("schedule__wall").Subtract(clock.Time{})))
		selected, err := orm.SelectQuery(records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), calendarProjection(start.ScalarExpression))
		check(t, err)
		page, err := selected.Page(t.Context(), 2, 0)
		check(t, err)
		if page.Total != 11 || probe.calls() != 1 {
			t.Fatal("calendar arithmetic page lost its one-statement total", page, probe.calls())
		}
		stage, err := orm.Derive(selected)
		check(t, err)
		selectedStart := orm.DateTime(orm.OptionalOperand(orm.SelectedField(stage, start)))
		result, err := orm.SelectResults(stage, orm.Project1(selectedStart.DifferenceDate(date("2000-01-01")), func(value orm.Optional[duration.Duration]) any { return optionalCalendar(value) }))
		check(t, err)
		predicate, err := stage.Predicate(orm.DynamicAnd(orm.DynamicColumn(1).GreaterThanOrEqual(instant("2020-01-01T00:00:00Z")), orm.DynamicColumn(1).LessThan(instant("2022-01-01T00:00:00Z"))))
		check(t, err)
		rows, err := result.Filter(predicate).All(t.Context())
		check(t, err)
		assertCalendarJSON(t, rows, `["7671 23:59:59.999999","7671 00:30:00.000001"]`)
		other, err := orm.Derive(selected)
		check(t, err)
		foreign := orm.DateTime(orm.OptionalOperand(orm.SelectedField(other, start)))
		calls := probe.calls()
		if rows, err := result.Filter(orm.ExactExpressions(selectedStart, foreign)).All(t.Context()); rows != nil || err == nil || probe.calls() != calls {
			t.Fatal("arithmetic lost result scope ownership", err)
		}
		events := relations.PlanningSchedule.Events
		collection := orm.Date(events.Day).AddExpression(orm.Time(events.Wall).Difference(clock.Time{}))
		if rows, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe), orm.Project1(collection, func(value orm.Optional[time.Time]) any { return optionalCalendar(value) })); rows != nil || err == nil || probe.calls() != calls {
			t.Fatal("collection arithmetic became a root row scalar", err)
		}
		if count, err := records.EventObjects.Using(probe).Update(t.Context(), orm.AssignExpression(f.At, orm.Date(related.Day).Add(period))); count != 0 || err == nil || probe.calls() != calls {
			t.Fatal("joined calendar assignment reached I/O", count, err)
		}
	})
	t.Run("arithmetic_updates_savepoints_and_cache", func(t *testing.T) {
		rollbackCalendar(t, backend, func(session db.Session) {
			q := records.EventObjects.Using(session).OrderBy(f.Code.Asc())
			held, err := q.All(t.Context())
			check(t, err)
			sibling := q.Filter()
			_, err = sibling.All(t.Context())
			check(t, err)
			count, err := q.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "at", Value: orm.DynamicF("day").Add(orm.DynamicF("wall").Subtract(clock.Time{}))}, orm.DynamicUpdateInput{Field: "wall", Value: orm.DynamicF("wall").Add(duration.FromMicroseconds(1))})
			check(t, err)
			fresh, err := q.All(t.Context())
			check(t, err)
			cached, err := sibling.All(t.Context())
			check(t, err)
			if count != 11 || !reflect.DeepEqual(held, cached) || fresh[0].At == nil || !fresh[0].At.Equal(instant("2021-01-01T23:59:59.999999Z")) || fresh[0].Wall == nil || *fresh[0].Wall != (clock.Time{}) || fresh[0].Part == nil || *fresh[0].Part != 10 {
				t.Fatal("calendar assignment changed original-row or cache ownership", count, fresh)
			}
			count, err = q.Update(t.Context(), orm.AssignExpression(f.At, day.Add(duration.Duration{Days: 1})), orm.Assign(f.Part, int64(99)))
			nativeCalendarError(t, vendor, err, "22007")
			if count != 0 {
				t.Fatal("failed arithmetic published a count", count)
			}
			after, err := q.Fresh().All(t.Context())
			check(t, err)
			if !reflect.DeepEqual(fresh, after) {
				t.Fatal("failed calendar assignment leaked a partial write")
			}
			count, err = q.Filter(f.Code.Exact("a")).Update(t.Context(), orm.AssignExpression(f.Wall, clockValue.Subtract(duration.FromMicroseconds(1))))
			check(t, err)
			if count != 1 {
				t.Fatal("savepoint failure poisoned its caller session", count)
			}
		})
		if physical.Snapshot() != before {
			t.Fatal("calendar arithmetic rollback changed stored models")
		}
	})
	t.Run("arithmetic_failures_context_and_cursor_ownership", func(t *testing.T) {
		for _, test := range []struct {
			name, observation, target string
			expression                orm.DynamicExpression
		}{
			{"invalid_date_storage", "arith_invalid_date_storage", "at", orm.DynamicF("day").Add(duration.Duration{})},
			{"clock_24_storage", "arith_clock_24_storage", "wall", orm.DynamicF("wall").Add(duration.Duration{})},
		} {
			withCalendarCorruption(t, physical, seed, vendor, test.name, func() {
				corrupt := physical.Snapshot()
				value, err := observeCalendar(t, backend, test.observation)
				nativeCalendarError(t, vendor, err, "22007")
				assertCalendarJSON(t, value, `null`)
				count, err := records.EventObjects.Using(backend).UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: test.target, Value: test.expression})
				nativeCalendarError(t, vendor, err, "22007")
				if count != 0 || physical.Snapshot() != corrupt {
					t.Fatal("calendar arithmetic overwrote a corrupt source", count)
				}
			})
		}
		withCalendarCorruption(t, physical, seed, vendor, "invalid_date_storage", func() {
			value, err := observeCalendar(t, backend, "arith_unselected_corrupt_date")
			check(t, err)
			rows, ok := value.([]map[string]any)
			if !ok || len(rows) != 11 {
				t.Fatal("unselected calendar branch changed cardinality", value)
			}
			for _, row := range rows {
				if row["value"] != "2000-01-01T00:00:00.000000Z" {
					t.Fatal("unselected corrupt Date was evaluated", row)
				}
			}
			value, err = observeCalendar(t, backend, "arith_filtered_corrupt_date")
			check(t, err)
			assertCalendarJSON(t, value, `{"first":"0001-01-01T01:00:00.000001Z"}`)
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if rows, err := orm.SelectInto(ctx, records.EventObjects.Using(backend), calendarProjection(start.ScalarExpression)); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("calendar arithmetic lost its caller context", rows, err)
		}
		for _, fault := range []string{"scan", "iteration", "close"} {
			probe := &calendarReadProbe{calendarBackend: backend, fault: fault}
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), calendarProjection(start.ScalarExpression))
			if rows != nil || err == nil || probe.closed.Load() == 0 {
				t.Fatal("calendar arithmetic exposed partial rows or leaked a cursor", fault, rows, err)
			}
		}
		if physical.Snapshot() != before {
			t.Fatal("failed calendar arithmetic changed storage")
		}
	})
}
