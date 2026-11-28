package consumer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

func calendarInstantTruncations[M any](value orm.DateTimeExpression[M]) map[string]orm.DateTimeExpression[M] {
	return map[string]orm.DateTimeExpression[M]{
		"year": value.TruncYear(), "quarter": value.TruncQuarter(), "month": value.TruncMonth(), "week": value.TruncWeek(),
		"day": value.TruncDay(), "hour": value.TruncHour(), "minute": value.TruncMinute(), "second": value.TruncSecond(),
	}
}
func calendarDateTruncations[M any](value orm.DateExpression[M]) map[string]orm.DateExpression[M] {
	return map[string]orm.DateExpression[M]{
		"year": value.TruncYear(), "quarter": value.TruncQuarter(), "month": value.TruncMonth(), "week": value.TruncWeek(), "day": value.TruncDay(),
	}
}
func calendarTimeTruncations[M any](value orm.TimeExpression[M]) map[string]orm.TimeExpression[M] {
	return map[string]orm.TimeExpression[M]{"hour": value.TruncHour(), "minute": value.TruncMinute(), "second": value.TruncSecond()}
}
func calendarDynamicTruncations(value orm.DynamicExpression) map[string]orm.DynamicExpression {
	return map[string]orm.DynamicExpression{
		"year": value.TruncYear(), "quarter": value.TruncQuarter(), "month": value.TruncMonth(), "week": value.TruncWeek(),
		"day": value.TruncDay(), "hour": value.TruncHour(), "minute": value.TruncMinute(), "second": value.TruncSecond(),
	}
}

// These operations mirror only the reference's inputs. Their expected values
// come from literal invariants or the independently executed pinned Django.
func observeCalendarTruncation(t *testing.T, reader db.Session, name string) (any, error) {
	t.Helper()
	f := records.EventFields
	base := records.EventObjects.Using(reader).OrderBy(f.Code.Asc())
	at, day, clockValue := orm.DateTime(orm.F(f.At)), orm.Date(orm.F(f.Day)), orm.Time(orm.F(f.Wall))
	if strings.HasPrefix(name, "trunc_datetime_") {
		if value, ok := calendarInstantTruncations(at)[strings.TrimPrefix(name, "trunc_datetime_")]; ok {
			return orm.SelectInto(t.Context(), base, calendarProjection(value.ScalarExpression))
		}
	}
	if strings.HasPrefix(name, "trunc_date_") {
		if value, ok := calendarDateTruncations(day)[strings.TrimPrefix(name, "trunc_date_")]; ok {
			return orm.SelectInto(t.Context(), base, calendarProjection(value.ScalarExpression))
		}
	}
	if strings.HasPrefix(name, "trunc_time_") {
		if value, ok := calendarTimeTruncations(clockValue)[strings.TrimPrefix(name, "trunc_time_")]; ok {
			return orm.SelectInto(t.Context(), base, calendarProjection(value.ScalarExpression))
		}
	}
	switch name {
	case "trunc_date_after_shift":
		return orm.SelectInto(t.Context(), base.Filter(orm.Not(f.Code.Exact("h"))), calendarProjection(at.Add(duration.FromMicroseconds(1)).Date().TruncMonth().ScalarExpression))
	case "trunc_clock_from_instant":
		return orm.SelectInto(t.Context(), base, calendarProjection(at.Time().TruncMinute().ScalarExpression))
	case "trunc_group_month", "trunc_group_week":
		key := at.Date().TruncMonth()
		if name == "trunc_group_week" {
			key = at.Date().TruncWeek()
		}
		grouped, err := orm.GroupBy(base.OrderBy(), orm.Project1(key, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }),
			orm.Aggregate3(orm.CountRows[records.Event](), orm.Min(at), orm.Max(at), func(total int64, first, last orm.Optional[time.Time]) map[string]any {
				return map[string]any{"total": total, "first": optionalCalendar(first), "last": optionalCalendar(last)}
			}), func(value any, result map[string]any) map[string]any { result["value"] = value; return result })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(orm.GroupKey(key).Asc().NullsLast()).All(t.Context())
	case "trunc_filter_month":
		value := at.Date().TruncMonth()
		return orm.SelectInto(t.Context(), base.Filter(value.Exact(date("2021-01-01"))), calendarProjection(value.ScalarExpression))
	case "trunc_distinct_month":
		value := at.Date().TruncMonth()
		selected, err := orm.SelectQuery(base.OrderBy(value.Asc().NullsLast()), orm.Project1(value, func(value orm.Optional[calendar.Date]) map[string]any {
			return map[string]any{"value": optionalCalendar(value)}
		}))
		if err != nil {
			return nil, err
		}
		return selected.Distinct().All(t.Context())
	case "trunc_aggregate":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate3(orm.Min(day.TruncWeek()), orm.Max(at.TruncDay()), orm.Count(clockValue.TruncHour()), func(first orm.Optional[calendar.Date], last orm.Optional[time.Time], count int64) map[string]any {
			return map[string]any{"first": optionalCalendar(first), "last": optionalCalendar(last), "count": count}
		}))
	case "trunc_filtered_aggregate":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate2(orm.Min(day.TruncMonth()).Where(f.Enabled.Exact(true)), orm.Max(clockValue.TruncMinute()).Where(f.Enabled.Exact(true)), func(first orm.Optional[calendar.Date], last orm.Optional[clock.Time]) map[string]any {
			return map[string]any{"first": optionalCalendar(first), "last": optionalCalendar(last)}
		}))
	case "trunc_related":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		schedule, detail := relations.RecordsEvent.Schedule, relations.RecordsEvent.Detail
		return orm.SelectInto(t.Context(), base, orm.Project5(f.Code, orm.DateTime(schedule.Anchor).TruncMonth(), orm.Date(schedule.Day).TruncWeek(), orm.DateTime(detail.At).TruncSecond(), orm.Time(detail.Wall).TruncHour(),
			func(code string, anchor orm.Optional[time.Time], scheduleDay orm.Optional[calendar.Date], detailAt orm.Optional[time.Time], detailTime orm.Optional[clock.Time]) map[string]any {
				return map[string]any{"code": code, "anchor": optionalCalendar(anchor), "schedule_day": optionalCalendar(scheduleDay), "detail_at": optionalCalendar(detailAt), "detail_time": optionalCalendar(detailTime)}
			}))
	case "trunc_collection":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		events := relations.PlanningSchedule.Events
		grouped, err := orm.GroupBy(planning.ScheduleObjects.Using(reader), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }),
			orm.Aggregate3(orm.Min(orm.DateTime(events.At).TruncMonth()), orm.Count(orm.Date(events.Day).TruncMonth()).Distinct(), orm.Max(orm.Time(events.Wall).TruncHour()).Where(events.Enabled.Exact(true)), func(first orm.Optional[time.Time], months int64, last orm.Optional[clock.Time]) map[string]any {
				return map[string]any{"first": optionalCalendar(first), "months": months, "last": optionalCalendar(last)}
			}), func(code string, value map[string]any) map[string]any { value["code"] = code; return value })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
	case "trunc_case_time":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(clockValue.TruncSecond(), orm.When(f.Enabled.Exact(true), clockValue.TruncHour()))))
	case "trunc_case_date":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(day.TruncWeek(), orm.When(f.Enabled.Exact(true), day.TruncMonth()))))
	case "update_truncated_calendar", "update_truncated_filtered":
		assignments := []orm.UpdateAssignment[records.Event]{orm.AssignExpression(f.At, at.TruncHour()), orm.AssignExpression(f.Day, day.TruncWeek()), orm.AssignExpression(f.Wall, clockValue.TruncMinute()), orm.AssignExpression(f.Part, day.Day())}
		source := base
		if name == "update_truncated_filtered" {
			source = base.Filter(at.Date().TruncMonth().Exact(date("2021-01-01")))
			assignments = []orm.UpdateAssignment[records.Event]{orm.AssignExpression(f.At, at.TruncDay()), orm.AssignExpression(f.Day, day.TruncYear())}
		}
		count, err := source.Update(t.Context(), assignments...)
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
	case "invalid_trunc_date_hour", "invalid_trunc_time_year", "invalid_trunc_duration_day":
		api, err := project.UsingSession(reader)
		if err != nil {
			return nil, err
		}
		expression := orm.DynamicF("day").TruncHour()
		if name == "invalid_trunc_time_year" {
			expression = orm.DynamicF("wall").TruncYear()
		}
		if name == "invalid_trunc_duration_day" {
			expression = orm.DynamicValue(duration.FromMicroseconds(1)).TruncDay()
		}
		value, err := api.RecordsEvent.Expression(expression)
		if err != nil {
			return nil, err
		}
		return project.SelectRecordsEventInto(t.Context(), api.RecordsEvent, orm.Project1(value, func(value query.Value) any { return dynamicCalendar(value) }))
	case "trunc_invalid_date_storage", "trunc_outside_date_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.TruncDay().ScalarExpression))
	case "trunc_invalid_datetime_storage", "trunc_infinite_datetime_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(at.TruncYear().ScalarExpression))
	case "trunc_clock_24_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.TruncHour().ScalarExpression))
	case "trunc_unselected_corrupt_date":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(orm.Value[records.Event](date("2000-01-01")), orm.When(f.Code.Exact("never"), day.TruncYear()))))
	case "trunc_filtered_corrupt_date":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Min(day.TruncMonth()).Where(orm.Not(f.Code.Exact("a"))), func(first orm.Optional[calendar.Date]) map[string]any {
			return map[string]any{"first": optionalCalendar(first)}
		}))
	default:
		return nil, fmt.Errorf("unknown calendar truncation observation %q", name)
	}
}

func checkCalendarTruncation(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical, seed calendarSeed, before string) {
	t.Helper()
	f := records.EventFields
	at, day, clockValue := orm.DateTime(orm.F(f.At)), orm.Date(orm.F(f.Day)), orm.Time(orm.F(f.Wall))
	t.Run("truncated_typed_dynamic_and_boundaries", func(t *testing.T) {
		instants, dates, clocks := calendarInstantTruncations(at), calendarDateTruncations(day), calendarTimeTruncations(clockValue)
		for _, unit := range slices.Sorted(maps.Keys(instants)) {
			assertCalendarParity(t, backend, instants[unit].ScalarExpression, calendarDynamicTruncations(orm.DynamicF("at"))[unit])
		}
		for _, unit := range slices.Sorted(maps.Keys(dates)) {
			assertCalendarParity(t, backend, dates[unit].ScalarExpression, calendarDynamicTruncations(orm.DynamicF("day"))[unit])
		}
		for _, unit := range slices.Sorted(maps.Keys(clocks)) {
			assertCalendarParity(t, backend, clocks[unit].ScalarExpression, calendarDynamicTruncations(orm.DynamicF("wall"))[unit])
		}
		source := records.EventObjects.Using(backend).Filter(f.Code.In("a", "g", "h")).OrderBy(f.Code.Asc())
		week, err := orm.SelectInto(t.Context(), source, calendarProjection(at.TruncWeek().ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, week, `[{"code":"a","value":"2020-12-28T00:00:00.000000Z"},{"code":"g","value":"0001-01-01T00:00:00.000000Z"},{"code":"h","value":"9999-12-27T00:00:00.000000Z"}]`)
		seconds, err := orm.SelectInto(t.Context(), source, calendarProjection(at.TruncSecond().ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, seconds, `[{"code":"a","value":"2020-12-31T23:30:59.000000Z"},{"code":"g","value":"0001-01-01T00:00:00.000000Z"},{"code":"h","value":"9999-12-31T23:59:59.000000Z"}]`)
		quarters, err := orm.SelectInto(t.Context(), source, calendarProjection(day.TruncQuarter().ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, quarters, `[{"code":"a","value":"2021-01-01"},{"code":"g","value":"0001-01-01"},{"code":"h","value":"9999-10-01"}]`)
		minutes, err := orm.SelectInto(t.Context(), source, calendarProjection(clockValue.TruncMinute().ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, minutes, `[{"code":"a","value":"23:59:00"},{"code":"g","value":"00:00:00"},{"code":"h","value":"23:59:00"}]`)
		assertCalendarParity(t, backend, orm.DateTime(orm.NullValue[records.Event, time.Time]()).TruncMonth().ScalarExpression, orm.DynamicNull(query.FieldDateTime).TruncMonth())
		assertCalendarParity(t, backend, orm.Date(orm.NullValue[records.Event, calendar.Date]()).TruncDay().ScalarExpression, orm.DynamicNull(query.FieldDate).TruncDay())
		assertCalendarParity(t, backend, orm.Time(orm.NullValue[records.Event, clock.Time]()).TruncHour().ScalarExpression, orm.DynamicNull(query.FieldTime).TruncHour())
		probe := &calendarReadProbe{calendarBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		for _, value := range []orm.DynamicExpression{orm.DynamicF("day").TruncHour(), orm.DynamicF("wall").TruncYear(), orm.DynamicF("part").TruncDay(), orm.DynamicValue("2021-01-01").TruncMonth(), orm.DynamicNull(query.FieldDate).TruncMinute(), orm.DynamicF("at").Year().TruncYear()} {
			if _, err := api.RecordsEvent.Expression(value); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				t.Fatal("invalid truncation signature was coerced or misclassified", err)
			}
		}
		nested, dynamic := at, orm.DynamicF("at")
		for range query.MaximumScalarDepth {
			nested, dynamic = nested.TruncMonth(), dynamic.TruncMonth()
		}
		if rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).Filter(f.ID.In()), calendarProjection(nested.ScalarExpression)); rows != nil || err == nil {
			t.Fatal("nested truncation bypassed its budget on an empty query", err)
		}
		if _, err := api.RecordsEvent.Expression(dynamic); err == nil || probe.calls() != 0 {
			t.Fatal("invalid truncation reached I/O", err, probe.calls())
		}
	})
	t.Run("truncated_groups_relations_and_stages", func(t *testing.T) {
		probe := &calendarReadProbe{calendarBackend: backend}
		key := at.Date().TruncWeek()
		grouped, err := orm.GroupBy(records.EventObjects.Using(probe), orm.Project1(key, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }),
			orm.Aggregate3(orm.CountRows[records.Event](), orm.Min(at), orm.Max(at), func(total int64, first, last orm.Optional[time.Time]) map[string]any {
				return map[string]any{"total": total, "first": optionalCalendar(first), "last": optionalCalendar(last)}
			}), func(value any, result map[string]any) map[string]any { result["value"] = value; return result })
		check(t, err)
		page, err := grouped.OrderBy(orm.GroupKey(key).Asc().NullsLast()).Page(t.Context(), 1, 4)
		check(t, err)
		if page.Total != 8 || probe.calls() != 1 {
			t.Fatal("truncated group page lost its total or one-statement read", page, probe.calls())
		}
		assertCalendarJSON(t, page.Rows, `[{"value":"2020-12-28","total":3,"first":"2020-12-31T23:30:59.999999Z","last":"2021-01-01T00:30:00.000001Z"}]`)
		collection, err := observeCalendar(t, backend, "trunc_collection")
		check(t, err)
		assertCalendarJSON(t, collection, `[{"code":"empty","first":"1900-03-01T00:00:00.000000Z","months":1,"last":null},{"code":"future","first":"0001-01-01T00:00:00.000000Z","months":3,"last":"00:00:00"},{"code":"north","first":"2000-02-01T00:00:00.000000Z","months":4,"last":"23:00:00"},{"code":"unused","first":null,"months":0,"last":null}]`)
		related, err := observeCalendar(t, backend, "trunc_related")
		check(t, err)
		relatedRows, ok := related.([]map[string]any)
		if !ok || len(relatedRows) != 11 {
			t.Fatal("truncation changed related-row cardinality", related)
		}
		assertCalendarJSON(t, relatedRows[0], `{"code":"a","anchor":"2021-01-01T00:00:00.000000Z","schedule_day":"2020-12-28","detail_at":"2024-03-03T00:00:00.000000Z","detail_time":"00:00:00"}`)
		assertCalendarJSON(t, relatedRows[8], `{"code":"i","anchor":null,"schedule_day":null,"detail_at":null,"detail_time":null}`)
		selected, err := orm.SelectQuery(records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), calendarProjection(at.Date().ScalarExpression))
		check(t, err)
		stage, err := orm.Derive(selected)
		check(t, err)
		month := orm.Date(orm.OptionalOperand(orm.SelectedField(stage, at.Date()))).TruncMonth()
		result, err := orm.SelectResults(stage, orm.Project1(month, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }))
		check(t, err)
		predicate, err := stage.Predicate(orm.DynamicColumn(1).TruncMonth().Exact(date("2021-01-01")))
		check(t, err)
		values, err := result.Filter(predicate).All(t.Context())
		check(t, err)
		assertCalendarJSON(t, values, `["2021-01-01","2021-01-01"]`)
		dynamicMonth, err := stage.Expression(orm.DynamicColumn(1).TruncMonth())
		check(t, err)
		dynamicResult, err := orm.SelectResults(stage, orm.Project1(dynamicMonth, func(value query.Value) any { return dynamicCalendar(value) }))
		check(t, err)
		typedRows, err := result.All(t.Context())
		check(t, err)
		dynamicRows, err := dynamicResult.All(t.Context())
		check(t, err)
		if !reflect.DeepEqual(typedRows, dynamicRows) {
			t.Fatal("truncation result stages lost typed/dynamic parity")
		}
		aggregate, err := orm.AggregateResults(stage, orm.Aggregate2(orm.Min(month), orm.Count(month).Distinct(), func(first orm.Optional[calendar.Date], count int64) map[string]any {
			return map[string]any{"first": optionalCalendar(first), "count": count}
		}))
		check(t, err)
		summary, err := aggregate.One(t.Context())
		check(t, err)
		assertCalendarJSON(t, summary, `{"first":"0001-01-01","count":8}`)
		other, err := orm.Derive(selected)
		check(t, err)
		foreign := orm.Date(orm.OptionalOperand(orm.SelectedField(other, at.Date()))).TruncMonth()
		calls := probe.calls()
		if rows, err := result.Filter(orm.ExactExpressions(month, foreign)).All(t.Context()); rows != nil || err == nil || probe.calls() != calls {
			t.Fatal("truncation discarded its selected-result scope", err)
		}
		relations, err := project.BindRelations()
		check(t, err)
		if rows, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe), orm.Project1(orm.DateTime(relations.PlanningSchedule.Events.At).TruncMonth(), func(value orm.Optional[time.Time]) any { return optionalCalendar(value) })); rows != nil || !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) || probe.calls() != calls {
			t.Fatal("truncated collection became a row scalar", err)
		}
		if count, err := records.EventObjects.Using(probe).Update(t.Context(), orm.AssignExpression(f.At, orm.DateTime(relations.RecordsEvent.Schedule.Anchor).TruncMonth())); count != 0 || err == nil || probe.calls() != calls {
			t.Fatal("truncation admitted a joined write operand", count, err)
		}
	})
	t.Run("truncated_updates_and_failures", func(t *testing.T) {
		rollbackCalendar(t, backend, func(session db.Session) {
			q := records.EventObjects.Using(session).OrderBy(f.Code.Asc())
			held, err := q.All(t.Context())
			check(t, err)
			sibling := q.Filter()
			_, err = sibling.All(t.Context())
			check(t, err)
			count, err := q.UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "at", Value: orm.DynamicF("at").TruncHour()}, orm.DynamicUpdateInput{Field: "day", Value: orm.DynamicF("day").TruncWeek()}, orm.DynamicUpdateInput{Field: "wall", Value: orm.DynamicF("wall").TruncMinute()}, orm.DynamicUpdateInput{Field: "part", Value: orm.DynamicF("day").Day()})
			check(t, err)
			fresh, err := q.All(t.Context())
			check(t, err)
			cached, err := sibling.All(t.Context())
			check(t, err)
			if count != 11 || !reflect.DeepEqual(held, cached) || fresh[0].At == nil || !fresh[0].At.Equal(instant("2020-12-31T23:00:00Z")) || fresh[0].Day == nil || fresh[0].Day.String() != "2020-12-28" || fresh[0].Wall == nil || fresh[0].Wall.String() != "23:59:00" || fresh[0].Part == nil || *fresh[0].Part != 1 {
				t.Fatal("truncated assignment changed original-row values or cache ownership", count, fresh)
			}
			count, err = q.Update(t.Context(), orm.Assign(f.Code, "duplicate"), orm.AssignExpression(f.At, at.TruncMonth()))
			if err == nil || count != 0 {
				t.Fatal("failed truncated write published a count", count, err)
			}
			if vendor == "postgres" {
				var native *pgconn.PgError
				if !errors.As(err, &native) || native.Code != "23505" {
					t.Fatal("truncated write lost native constraint cause", err)
				}
			} else {
				var native *modernsqlite.Error
				if !errors.As(err, &native) || native.Code() != 2067 {
					t.Fatal("truncated write lost native constraint cause", err)
				}
			}
			after, err := q.Fresh().All(t.Context())
			check(t, err)
			if !reflect.DeepEqual(fresh, after) {
				t.Fatal("failed truncation left partial writes")
			}
		})
		for _, test := range []struct {
			name, column string
			expression   orm.DynamicExpression
		}{
			{"trunc_invalid_date_storage", "day", orm.DynamicF("day").TruncDay()},
			{"trunc_outside_date_storage", "day", orm.DynamicF("day").TruncDay()},
			{"trunc_invalid_datetime_storage", "at", orm.DynamicF("at").TruncYear()},
			{"trunc_infinite_datetime_storage", "at", orm.DynamicF("at").TruncYear()},
			{"trunc_clock_24_storage", "wall", orm.DynamicF("wall").TruncHour()},
		} {
			withCalendarCorruption(t, physical, seed, vendor, test.name, func() {
				corrupt := physical.Snapshot()
				value, err := observeCalendar(t, backend, test.name)
				nativeCalendarError(t, vendor, err, "22007")
				assertCalendarJSON(t, value, `null`)
				count, err := records.EventObjects.Using(backend).UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: test.column, Value: test.expression})
				nativeCalendarError(t, vendor, err, "22007")
				if count != 0 || physical.Snapshot() != corrupt {
					t.Fatal("truncation repaired or partially overwrote corrupt input", count)
				}
			})
		}
		withCalendarCorruption(t, physical, seed, vendor, "invalid_date_storage", func() {
			value, err := observeCalendar(t, backend, "trunc_unselected_corrupt_date")
			check(t, err)
			rows, ok := value.([]map[string]any)
			if !ok || len(rows) != 11 {
				t.Fatal("unselected truncation changed row count", value)
			}
			for _, row := range rows {
				if row["value"] != "2000-01-01" {
					t.Fatal("unselected corrupt operand was evaluated", row)
				}
			}
			value, err = observeCalendar(t, backend, "trunc_filtered_corrupt_date")
			check(t, err)
			assertCalendarJSON(t, value, `{"first":"0001-01-01"}`)
		})
		count, err := records.EventObjects.Using(backend).Update(t.Context(), orm.AssignExpression(f.At, at.Add(duration.FromMicroseconds(1)).TruncYear()))
		nativeCalendarError(t, vendor, err, "22007")
		if count != 0 || physical.Snapshot() != before {
			t.Fatal("truncation hid an overflowing intermediate instant", count)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if count, err := records.EventObjects.Using(backend).Update(ctx, orm.AssignExpression(f.At, at.TruncMonth())); count != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled truncation lost its context", count, err)
		}
		for _, fault := range []string{"scan", "iteration", "close"} {
			probe := &calendarReadProbe{calendarBackend: backend, fault: fault}
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe), calendarProjection(at.TruncMonth().ScalarExpression))
			if rows != nil || err == nil || probe.closed.Load() == 0 {
				t.Fatal("truncation published partial rows or leaked a failing cursor", fault, rows, err)
			}
		}
		if physical.Snapshot() != before {
			t.Fatal("truncation failure changed physical storage")
		}
	})
}
