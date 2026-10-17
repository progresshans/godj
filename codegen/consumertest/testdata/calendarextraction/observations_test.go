package consumer

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

// These parsers read checked-in synthetic inputs. They never compute expected
// extraction results, consult a backend, or read the independent oracle.
func instant(text string) time.Time {
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		panic(err)
	}
	return value
}
func date(text string) calendar.Date {
	value, err := calendar.Parse(text)
	if err != nil {
		panic(err)
	}
	return value
}
func wall(text string) clock.Time {
	value, err := clock.Parse(text)
	if err != nil {
		panic(err)
	}
	return value
}

type calendarValue interface {
	time.Time | calendar.Date | clock.Time | int64 | float64
}

func wireCalendar[T calendarValue](value T) any {
	switch value := any(value).(type) {
	case time.Time:
		return value.UTC().Format("2006-01-02T15:04:05.000000Z")
	case calendar.Date:
		return value.String()
	case clock.Time:
		return value.String()
	case int64:
		return value
	case float64:
		return value
	default:
		panic("invalid calendar observation value")
	}
}
func optionalCalendar[T calendarValue](value orm.Optional[T]) any {
	if value, ok := value.Get(); ok {
		return wireCalendar(value)
	}
	return nil
}
func pointerCalendar[T calendarValue](value *T) any {
	if value != nil {
		return wireCalendar(*value)
	}
	return nil
}
func dynamicCalendar(value query.Value) any {
	if value.IsNull() {
		return nil
	}
	if value, ok := value.DateTime(); ok {
		return wireCalendar(value)
	}
	if value, ok := value.Date(); ok {
		return value.String()
	}
	if value, ok := value.Time(); ok {
		return value.String()
	}
	if value, ok := value.Integer(); ok {
		return value
	}
	if value, ok := value.Float(); ok {
		return value
	}
	panic("invalid calendar dynamic result")
}

type calendarSeed struct {
	schedules map[string]planning.Schedule
	events    map[string]records.Event
}

func seedCalendar(t *testing.T, backend db.Mutator) calendarSeed {
	t.Helper()
	data, err := os.ReadFile("input.json")
	check(t, err)
	var input struct {
		Schedules []struct {
			Code              string
			Anchor, Day, Wall *string
		}
		Events []struct {
			Code, Bucket, Start     string
			Enabled                 bool
			At, Day, Wall, Schedule *string
			Part                    *int64
		}
		Details []struct {
			Event         string
			At, Day, Wall *string
		}
	}
	check(t, json.Unmarshal(data, &input))
	seed := calendarSeed{map[string]planning.Schedule{}, map[string]records.Event{}}
	for _, row := range input.Schedules {
		value := planning.NewScheduleCreate(row.Code)
		if row.Anchor != nil {
			value = value.WithAnchor(instant(*row.Anchor))
		}
		if row.Day != nil {
			value = value.WithDay(date(*row.Day))
		}
		if row.Wall != nil {
			value = value.WithWall(wall(*row.Wall))
		}
		saved, err := planning.ScheduleObjects.Create(t.Context(), backend, value)
		check(t, err)
		seed.schedules[row.Code] = saved
	}
	for _, row := range input.Events {
		value := records.NewEventCreate(row.Code, row.Bucket, row.Enabled, instant(row.Start))
		if row.At != nil {
			value = value.WithAt(instant(*row.At))
		}
		if row.Day != nil {
			value = value.WithDay(date(*row.Day))
		}
		if row.Wall != nil {
			value = value.WithWall(wall(*row.Wall))
		}
		if row.Part != nil {
			value = value.WithPart(*row.Part)
		}
		if row.Schedule != nil {
			value = value.WithScheduleID(seed.schedules[*row.Schedule].ID)
		}
		saved, err := records.EventObjects.Create(t.Context(), backend, value)
		check(t, err)
		seed.events[row.Code] = saved
	}
	for _, row := range input.Details {
		value := records.NewDetailCreate(seed.events[row.Event].ID)
		if row.At != nil {
			value = value.WithAt(instant(*row.At))
		}
		if row.Day != nil {
			value = value.WithDay(date(*row.Day))
		}
		if row.Wall != nil {
			value = value.WithWall(wall(*row.Wall))
		}
		_, err := records.DetailObjects.Create(t.Context(), backend, value)
		check(t, err)
	}
	return seed
}
func calendarProjection[T calendarValue](value orm.ScalarExpression[records.Event, T]) orm.Projection[records.Event, map[string]any] {
	return orm.Project2(records.EventFields.Code, value, func(code string, value orm.Optional[T]) map[string]any {
		return map[string]any{"code": code, "value": optionalCalendar(value)}
	})
}

func calendarParts(at orm.DateTimeExpression[records.Event], day orm.DateExpression[records.Event], wall orm.TimeExpression[records.Event]) map[string]orm.NumericExpression[records.Event, int64] {
	return map[string]orm.NumericExpression[records.Event, int64]{
		"datetime_year": at.Year(), "datetime_iso_year": at.ISOYear(), "datetime_quarter": at.Quarter(), "datetime_month": at.Month(), "datetime_week": at.Week(), "datetime_week_day": at.WeekDay(), "datetime_iso_week_day": at.ISOWeekDay(), "datetime_day": at.Day(), "datetime_hour": at.Hour(), "datetime_minute": at.Minute(), "datetime_second": at.Second(),
		"date_year": day.Year(), "date_iso_year": day.ISOYear(), "date_quarter": day.Quarter(), "date_month": day.Month(), "date_week": day.Week(), "date_week_day": day.WeekDay(), "date_iso_week_day": day.ISOWeekDay(), "date_day": day.Day(),
		"time_hour": wall.Hour(), "time_minute": wall.Minute(), "time_second": wall.Second(),
		"composed_date_year": at.Date().Year(), "composed_time_second": at.Time().Second(),
	}
}

// The input operation is mirrored, not the reference's expected result. The
// caller owns preparation and rollback, including raw corrupted storage.
func observeCalendar(t *testing.T, reader db.Session, name string) (any, error) {
	t.Helper()
	if strings.HasPrefix(name, "trunc_") || strings.HasPrefix(name, "update_truncated_") || strings.HasPrefix(name, "invalid_trunc_") {
		return observeCalendarTruncation(t, reader, name)
	}
	f := records.EventFields
	base := records.EventObjects.Using(reader).OrderBy(f.Code.Asc())
	at, day, wallValue := orm.DateTime(orm.F(f.At)), orm.Date(orm.F(f.Day)), orm.Time(orm.F(f.Wall))
	extracted, clockValue := at.Date(), at.Time()
	if part, ok := calendarParts(at, day, wallValue)[name]; ok {
		return orm.SelectInto(t.Context(), base, calendarProjection(part.ScalarExpression))
	}
	switch name {
	case "datetime_date":
		return orm.SelectInto(t.Context(), base, calendarProjection(extracted.ScalarExpression))
	case "datetime_time":
		return orm.SelectInto(t.Context(), base, calendarProjection(clockValue.ScalarExpression))
	case "literal_components":
		literal := orm.Date(orm.Value[records.Event](date("2021-01-01")))
		late := orm.DateTime(orm.Value[records.Event](instant("9999-12-31T23:59:59.000001Z"))).Time()
		return orm.SelectInto(t.Context(), base, orm.Project4(f.Code, literal.Year(), literal.ISOYear(), late, func(code string, year, isoYear orm.Optional[int64], clock orm.Optional[clock.Time]) map[string]any {
			return map[string]any{"code": code, "year": optionalCalendar(year), "iso_year": optionalCalendar(isoYear), "clock": optionalCalendar(clock)}
		}))
	case "filter_year":
		return orm.SelectInto(t.Context(), base.Filter(at.Year().GreaterThanOrEqual(2021)), calendarProjection(at.Year().ScalarExpression))
	case "filter_date_range":
		return orm.SelectInto(t.Context(), base.Filter(extracted.GreaterThanOrEqual(date("2020-12-31")), extracted.LessThanOrEqual(date("2021-01-01"))), calendarProjection(extracted.ScalarExpression))
	case "order_clock":
		return orm.SelectInto(t.Context(), base.OrderBy(clockValue.Asc().NullsLast(), f.Code.Asc()), calendarProjection(clockValue.ScalarExpression))
	case "group_by_date", "empty_group":
		source := base.OrderBy()
		if name == "empty_group" {
			source = source.Filter(f.ID.In())
		}
		grouped, err := orm.GroupBy(source, orm.Project1(extracted, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }),
			orm.Aggregate4(orm.CountRows[records.Event](), orm.Count(f.ID).Where(f.Enabled.Exact(true)), orm.Min(clockValue), orm.Max(clockValue), func(total, opened int64, first, last orm.Optional[clock.Time]) map[string]any {
				return map[string]any{"total": total, "opened": opened, "first": optionalCalendar(first), "last": optionalCalendar(last)}
			}),
			func(value any, result map[string]any) map[string]any { result["value"] = value; return result })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(orm.GroupKey(extracted).Asc().NullsLast()).All(t.Context())
	case "group_iso_week":
		grouped, err := orm.GroupBy(base.OrderBy(), orm.Project2(at.ISOYear(), at.Week(), func(year, week orm.Optional[int64]) map[string]any {
			return map[string]any{"year": optionalCalendar(year), "week": optionalCalendar(week)}
		}), orm.Aggregate1(orm.CountRows[records.Event](), func(total int64) int64 { return total }), func(value map[string]any, total int64) map[string]any { value["total"] = total; return value })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(orm.GroupKey(at.ISOYear()).Asc().NullsLast(), orm.GroupKey(at.Week()).Asc().NullsLast()).All(t.Context())
	case "aggregate_parts":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate4(orm.Sum(day.Year()), orm.Avg(wallValue.Second()), orm.Min(extracted), orm.Max(clockValue), func(years orm.Optional[int64], seconds orm.Optional[float64], first orm.Optional[calendar.Date], last orm.Optional[clock.Time]) map[string]any {
			return map[string]any{"years": optionalCalendar(years), "seconds": optionalCalendar(seconds), "first": optionalCalendar(first), "last": optionalCalendar(last)}
		}))
	case "filtered_aggregate":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate2(orm.Sum(day.Year()).Where(f.Enabled.Exact(true)), orm.Count(extracted).Distinct().Where(f.Enabled.Exact(true)), func(years orm.Optional[int64], dates int64) map[string]any {
			return map[string]any{"years": optionalCalendar(years), "dates": dates}
		}))
	case "case_date":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(extracted, orm.When(f.At.IsNull(true), orm.Value[records.Event](date("2000-01-01"))))))
	case "case_time":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(clockValue, orm.When(f.At.IsNull(true), orm.Value[records.Event](wall("00:00:00"))))))
	case "date_after_shift":
		return orm.SelectInto(t.Context(), base.Filter(orm.Not(f.Code.Exact("h"))), calendarProjection(at.Add(duration.FromMicroseconds(1)).Date().ScalarExpression))
	case "related_parts":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		schedule, detail := relations.RecordsEvent.Schedule, relations.RecordsEvent.Detail
		selection := orm.Project5(orm.DateTime(schedule.Anchor).Date(), orm.Date(schedule.Day).ISOYear(), orm.Time(schedule.Wall).Second(), orm.DateTime(detail.At).Date(), orm.Date(detail.Day).WeekDay(), func(anchor orm.Optional[calendar.Date], year, second orm.Optional[int64], detail orm.Optional[calendar.Date], day orm.Optional[int64]) map[string]any {
			return map[string]any{"anchor": optionalCalendar(anchor), "schedule_year": optionalCalendar(year), "schedule_second": optionalCalendar(second), "detail_date": optionalCalendar(detail), "detail_day": optionalCalendar(day)}
		})
		return orm.SelectInto(t.Context(), base, orm.CombineProjections(orm.Project1(f.Code, func(code string) string { return code }), selection, func(code string, value map[string]any) map[string]any { value["code"] = code; return value }))
	case "collection_years":
		relations, err := project.BindRelations()
		if err != nil {
			return nil, err
		}
		events := relations.PlanningSchedule.Events
		year, first := orm.DateTime(events.At).Year(), orm.DateTime(events.At).Date()
		grouped, err := orm.GroupBy(planning.ScheduleObjects.Using(reader), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }), orm.Aggregate3(orm.Sum(year), orm.Sum(year).Where(events.Enabled.Exact(true)), orm.Min(first), func(years, opened orm.Optional[int64], first orm.Optional[calendar.Date]) map[string]any {
			return map[string]any{"years": optionalCalendar(years), "opened": optionalCalendar(opened), "first": optionalCalendar(first)}
		}), func(code string, value map[string]any) map[string]any { value["code"] = code; return value })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
	case "distinct_dates":
		selected, err := orm.SelectQuery(base.OrderBy(extracted.Asc().NullsLast()), orm.Project1(extracted, func(value orm.Optional[calendar.Date]) map[string]any {
			return map[string]any{"value": optionalCalendar(value)}
		}))
		if err != nil {
			return nil, err
		}
		return selected.Distinct().All(t.Context())
	case "literal_aggregate":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Sum(orm.Date(orm.Value[records.Event](date("2004-02-29"))).Year()), func(years orm.Optional[int64]) map[string]any {
			return map[string]any{"years": optionalCalendar(years)}
		}))
	case "empty_aggregate":
		return orm.AggregateInto(t.Context(), base.Filter(f.ID.In()), orm.Aggregate3(orm.Sum(day.Year()), orm.Min(extracted), orm.CountRows[records.Event](), func(years orm.Optional[int64], first orm.Optional[calendar.Date], count int64) map[string]any {
			return map[string]any{"years": optionalCalendar(years), "first": optionalCalendar(first), "count": count}
		}))
	case "update_calendar", "update_filtered":
		assignments := []orm.UpdateAssignment[records.Event]{orm.AssignExpression(f.Day, extracted), orm.AssignExpression(f.Wall, clockValue), orm.AssignExpression(f.Part, day.Year())}
		source := base
		if name == "update_filtered" {
			source = base.Filter(at.Year().GreaterThanOrEqual(2021))
			assignments = []orm.UpdateAssignment[records.Event]{orm.AssignExpression(f.Part, wallValue.Second())}
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
			row := map[string]any{"code": value.Code, "part": pointerCalendar(value.Part)}
			if name == "update_calendar" {
				row["day"] = pointerCalendar(value.Day)
				row["wall"] = pointerCalendar(value.Wall)
			}
			rows = append(rows, row)
		}
		return map[string]any{"changed": count, "rows": rows}, nil
	case "invalid_date_hour", "invalid_time_year", "invalid_duration_year":
		api, err := project.UsingSession(reader)
		if err != nil {
			return nil, err
		}
		expression := orm.DynamicF("day").Hour()
		if name == "invalid_time_year" {
			expression = orm.DynamicF("wall").Year()
		}
		if name == "invalid_duration_year" {
			expression = orm.DynamicValue(duration.FromMicroseconds(1)).Year()
		}
		value, err := api.RecordsEvent.Expression(expression)
		if err != nil {
			return nil, err
		}
		return project.SelectRecordsEventInto(t.Context(), api.RecordsEvent, orm.Project1(value, func(value query.Value) any { return dynamicCalendar(value) }))
	case "invalid_date_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.Month().ScalarExpression))
	case "outside_date_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(day.Year().ScalarExpression))
	case "invalid_datetime_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(at.Month().ScalarExpression))
	case "infinite_datetime_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(at.Year().ScalarExpression))
	case "clock_24_storage":
		return orm.SelectInto(t.Context(), base, calendarProjection(wallValue.Hour().ScalarExpression))
	case "unselected_corrupt_date":
		return orm.SelectInto(t.Context(), base, calendarProjection(orm.Case(orm.Value[records.Event](int64(0)), orm.When(f.Code.Exact("never"), day.Year()))))
	case "filtered_corrupt_date":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate1(orm.Sum(day.Year()).Where(orm.Not(f.Code.Exact("a"))), func(years orm.Optional[int64]) map[string]any {
			return map[string]any{"years": optionalCalendar(years)}
		}))
	default:
		return nil, fmt.Errorf("unknown calendar observation %q", name)
	}
}
