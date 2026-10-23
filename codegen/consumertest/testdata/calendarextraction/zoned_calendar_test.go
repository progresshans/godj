package consumer

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

func calendarZone(t *testing.T, name string) timezone.Zone {
	t.Helper()
	zone, err := timezone.Parse(name)
	check(t, err)
	return zone
}

func zonedCalendarParts[M any](value orm.ZonedDateTimeExpression[M]) map[string]orm.NumericExpression[M, int64] {
	return map[string]orm.NumericExpression[M, int64]{
		"Year":       value.Year(),
		"ISOYear":    value.ISOYear(),
		"Quarter":    value.Quarter(),
		"Month":      value.Month(),
		"Week":       value.Week(),
		"WeekDay":    value.WeekDay(),
		"ISOWeekDay": value.ISOWeekDay(),
		"Day":        value.Day(),
		"Hour":       value.Hour(),
		"Minute":     value.Minute(),
		"Second":     value.Second(),
	}
}
func dynamicZonedCalendarParts(value orm.DynamicZonedDateTimeExpression) map[string]orm.DynamicExpression {
	return map[string]orm.DynamicExpression{
		"Year":       value.Year(),
		"ISOYear":    value.ISOYear(),
		"Quarter":    value.Quarter(),
		"Month":      value.Month(),
		"Week":       value.Week(),
		"WeekDay":    value.WeekDay(),
		"ISOWeekDay": value.ISOWeekDay(),
		"Day":        value.Day(),
		"Hour":       value.Hour(),
		"Minute":     value.Minute(),
		"Second":     value.Second(),
	}
}
func zonedCalendarTruncations[M any](value orm.ZonedDateTimeExpression[M]) map[string]orm.DateTimeExpression[M] {
	return map[string]orm.DateTimeExpression[M]{
		"Year":    value.TruncYear(),
		"Quarter": value.TruncQuarter(),
		"Month":   value.TruncMonth(),
		"Week":    value.TruncWeek(),
		"Day":     value.TruncDay(),
		"Hour":    value.TruncHour(),
		"Minute":  value.TruncMinute(),
		"Second":  value.TruncSecond(),
	}
}
func dynamicZonedCalendarTruncations(value orm.DynamicZonedDateTimeExpression) map[string]orm.DynamicExpression {
	return map[string]orm.DynamicExpression{
		"Year":    value.TruncYear(),
		"Quarter": value.TruncQuarter(),
		"Month":   value.TruncMonth(),
		"Week":    value.TruncWeek(),
		"Day":     value.TruncDay(),
		"Hour":    value.TruncHour(),
		"Minute":  value.TruncMinute(),
		"Second":  value.TruncSecond(),
	}
}

func checkZonedCalendarConsumer(t *testing.T, backend calendarBackend, vendor string, physical calendarPhysical, seed calendarSeed, before string) {
	t.Helper()
	fields := records.EventFields
	at := orm.DateTime(orm.F(fields.At))
	seoul := calendarZone(t, "Asia/Seoul")
	newYork := calendarZone(t, "America/New_York")
	// These two stored extremes deliberately overflow in one local direction.
	// Normal-value comparisons exclude them; separate tests require errors.
	safe := orm.Not(fields.Code.In("g", "h"))
	t.Run("zoned_typed_dynamic_and_dst_values", func(t *testing.T) {
		for _, zone := range []timezone.Zone{timezone.UTC(), seoul, newYork} {
			typed, dynamic := at.InZone(zone), orm.DynamicF("at").InZone(zone)
			assertCalendarParity(t, backend, typed.Date().ScalarExpression, dynamic.Date(), safe)
			assertCalendarParity(t, backend, typed.Time().ScalarExpression, dynamic.Time(), safe)
			parts, dynamicParts := zonedCalendarParts(typed), dynamicZonedCalendarParts(dynamic)
			for _, name := range slices.Sorted(maps.Keys(parts)) {
				assertCalendarParity(t, backend, parts[name].ScalarExpression, dynamicParts[name], safe)
			}
			truncations, dynamicTruncations := zonedCalendarTruncations(typed), dynamicZonedCalendarTruncations(dynamic)
			for _, name := range slices.Sorted(maps.Keys(truncations)) {
				assertCalendarParity(t, backend, truncations[name].ScalarExpression, dynamicTruncations[name], safe)
			}
			assertCalendarParity(t, backend, orm.DateTime(orm.NullValue[records.Event, time.Time]()).InZone(zone).Date().ScalarExpression, orm.DynamicNull(query.FieldDateTime).InZone(zone).Date())
		}
		rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(backend).Filter(fields.Code.In("a", "b", "d", "i", "k")).OrderBy(fields.Code.Asc()), calendarProjection(at.InZone(seoul).Date().ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, rows, `[{"code":"a","value":"2021-01-01"},{"code":"b","value":"2021-01-01"},{"code":"d","value":"2000-03-01"},{"code":"i","value":null},{"code":"k","value":null}]`)
		for _, test := range []struct{ zone, input, want string }{
			{"America/New_York", "2024-11-03T05:30:59.999999Z", "2024-11-03T05:00:00.000000Z"},
			{"America/New_York", "2024-11-03T06:30:59.999999Z", "2024-11-03T05:00:00.000000Z"},
			{"Australia/Lord_Howe", "2024-10-05T15:45:00Z", "2024-10-05T15:30:00.000000Z"},
			{"Europe/Paris", "1900-01-01T00:00:00Z", "1899-12-31T23:50:39.000000Z"},
		} {
			value := orm.DateTime(orm.Value[records.Event](instant(test.input))).InZone(calendarZone(t, test.zone)).TruncHour()
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(backend).Filter(fields.Code.Exact("a")), calendarProjection(value.ScalarExpression))
			check(t, err)
			assertCalendarJSON(t, rows, `[{"code":"a","value":"`+test.want+`"}]`)
		}
	})

	t.Run("zoned_groups_relations_and_stages", func(t *testing.T) {
		probe := &calendarReadProbe{calendarBackend: backend}
		day := at.InZone(seoul).Date()
		grouped, err := orm.GroupBy(records.EventObjects.Using(probe).Filter(safe), orm.Project1(day, func(day orm.Optional[calendar.Date]) any { return optionalCalendar(day) }),
			orm.Aggregate1(orm.CountRows[records.Event](), func(count int64) int64 { return count }),
			func(day any, count int64) map[string]any { return map[string]any{"day": day, "count": count} })
		check(t, err)
		page, err := grouped.Having(orm.GroupKey(day).GreaterThanOrEqual(orm.Some(date("2021-01-01")))).OrderBy(orm.GroupKey(day).Asc()).Page(t.Context(), 1, 0)
		check(t, err)
		if page.Total != 2 || probe.calls() != 1 {
			t.Fatal("zoned group page lost filtered total or snapshot", page, probe.calls())
		}
		assertCalendarJSON(t, page.Rows, `[{"day":"2021-01-01","count":3}]`)
		relations, err := project.BindRelations()
		check(t, err)
		assertCalendarParity(t, backend, orm.DateTime(relations.RecordsEvent.Schedule.Anchor).InZone(newYork).Date().ScalarExpression, orm.DynamicF("schedule__anchor").InZone(newYork).Date())
		assertCalendarParity(t, backend, orm.DateTime(relations.RecordsEvent.Detail.At).InZone(seoul).Date().ScalarExpression, orm.DynamicF("detail__at").InZone(seoul).Date())
		events := relations.PlanningSchedule.Events
		relatedDay := orm.DateTime(events.At).InZone(seoul).Date()
		allowed := orm.Not(events.Code.In("g", "h"))
		collection, err := orm.GroupBy(planning.ScheduleObjects.Using(backend), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }),
			orm.Aggregate1(orm.Min(relatedDay).Where(allowed), func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }),
			func(code string, value any) map[string]any { return map[string]any{"code": code, "first": value} })
		check(t, err)
		values, err := collection.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
		check(t, err)
		assertCalendarJSON(t, values, `[{"code":"empty","first":"1900-03-01"},{"code":"future","first":"2400-02-29"},{"code":"north","first":"2000-03-01"},{"code":"unused","first":null}]`)
		count := probe.calls()
		invalid, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe), orm.Project1(relatedDay, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }))
		if err == nil || invalid != nil || probe.calls() != count {
			t.Fatal("zone erased collection cardinality", invalid, err)
		}
		if changed, err := records.EventObjects.Using(probe).Update(t.Context(), orm.AssignExpression(fields.Day, orm.DateTime(relations.RecordsEvent.Schedule.Anchor).InZone(seoul).Date())); err == nil || changed != 0 || probe.calls() != count {
			t.Fatal("zone permitted a joined write", changed, err)
		}
		selected, err := orm.SelectQuery(records.EventObjects.Using(probe).Filter(safe).OrderBy(fields.Code.Asc()), calendarProjection(at.ScalarExpression))
		check(t, err)
		stage, err := orm.Derive(selected)
		check(t, err)
		value := orm.DateTime(orm.OptionalOperand(orm.SelectedField(stage, at))).InZone(seoul).Date()
		result, err := orm.SelectResults(stage, orm.Project1(value, func(value orm.Optional[calendar.Date]) any { return optionalCalendar(value) }))
		check(t, err)
		typed, err := result.All(t.Context())
		check(t, err)
		dynamic, err := stage.Expression(orm.DynamicColumn(1).InZone(seoul).Date())
		check(t, err)
		other, err := orm.SelectResults(stage, orm.Project1(dynamic, func(value query.Value) any { return dynamicCalendar(value) }))
		check(t, err)
		untyped, err := other.All(t.Context())
		check(t, err)
		if !reflect.DeepEqual(typed, untyped) {
			t.Fatal("selected zoned columns lost typed/dynamic parity")
		}
		foreign, err := orm.Derive(selected)
		check(t, err)
		foreignValue := orm.DateTime(orm.OptionalOperand(orm.SelectedField(foreign, at))).InZone(seoul).Date()
		count = probe.calls()
		if values, err := result.Filter(orm.ExactExpressions(value, foreignValue)).All(t.Context()); err == nil || values != nil || probe.calls() != count {
			t.Fatal("zone erased selected scope", values, err)
		}
	})

	t.Run("zoned_updates_boundaries_and_failures", func(t *testing.T) {
		rollbackCalendar(t, backend, func(session db.Session) {
			source := records.EventObjects.Using(session).Filter(fields.Code.In("a", "i")).OrderBy(fields.Code.Asc())
			held, err := source.All(t.Context())
			check(t, err)
			sibling := source.Filter()
			_, err = sibling.All(t.Context())
			check(t, err)
			count, err := source.UpdateDynamic(t.Context(),
				orm.DynamicUpdateInput{Field: "at", Value: orm.DynamicF("at").InZone(seoul).TruncDay()},
				orm.DynamicUpdateInput{Field: "day", Value: orm.DynamicF("at").InZone(seoul).Date()},
				orm.DynamicUpdateInput{Field: "part", Value: orm.DynamicF("at").InZone(seoul).Hour()})
			check(t, err)
			fresh, err := source.All(t.Context())
			check(t, err)
			cached, err := sibling.All(t.Context())
			check(t, err)
			if count != 2 || !reflect.DeepEqual(held, cached) || len(fresh) != 2 || fresh[0].At == nil || !fresh[0].At.Equal(instant("2020-12-31T15:00:00Z")) || fresh[0].Day == nil || fresh[0].Day.String() != "2021-01-01" || fresh[0].Part == nil || *fresh[0].Part != 8 || fresh[1].At != nil || fresh[1].Day != nil || fresh[1].Part != nil {
				t.Fatal("zoned same-row update lost source, NULL or cache ownership", fresh, count)
			}
		})
		if physical.Snapshot() != before {
			t.Fatal("zoned dynamic update escaped rollback")
		}
		for _, test := range []struct {
			code string
			zone timezone.Zone
		}{{"g", newYork}, {"h", seoul}} {
			source := records.EventObjects.Using(backend).Filter(fields.Code.Exact(test.code))
			result, err := orm.SelectInto(t.Context(), source, calendarProjection(at.InZone(test.zone).Second().ScalarExpression))
			nativeCalendarError(t, vendor, err, "22007")
			if result != nil {
				t.Fatal("small component hid local year overflow", result)
			}
			count, err := source.Update(t.Context(), orm.AssignExpression(fields.Part, at.InZone(test.zone).Year()))
			nativeCalendarError(t, vendor, err, "22007")
			if count != 0 || physical.Snapshot() != before {
				t.Fatal("invalid local year partially wrote", count)
			}
		}
		count, err := records.EventObjects.Using(backend).Filter(fields.Code.Exact("g")).Update(t.Context(), orm.AssignExpression(fields.At, at.InZone(seoul).TruncDay()))
		nativeCalendarError(t, vendor, err, "22007")
		if count != 0 || physical.Snapshot() != before {
			t.Fatal("inverse truncation overflow partially wrote", count)
		}
		withCalendarCorruption(t, physical, seed, vendor, "invalid_datetime_storage", func() {
			corrupt := physical.Snapshot()
			result, err := orm.SelectInto(t.Context(), records.EventObjects.Using(backend).Filter(safe), calendarProjection(at.InZone(seoul).Year().ScalarExpression))
			nativeCalendarError(t, vendor, err, "22007")
			if result != nil || physical.Snapshot() != corrupt {
				t.Fatal("zone hid corrupt input or changed physical storage")
			}
			count, err := records.EventObjects.Using(backend).Filter(safe).UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "part", Value: orm.DynamicF("at").InZone(seoul).Year()})
			nativeCalendarError(t, vendor, err, "22007")
			if count != 0 || physical.Snapshot() != corrupt {
				t.Fatal("late invalid zoned update changed storage")
			}
		})
	})

	t.Run("zoned_ownership_context_and_cursors", func(t *testing.T) {
		local, err := seoul.Location()
		check(t, err)
		*local = *time.UTC
		probe := &calendarReadProbe{calendarBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		for _, expression := range []orm.DynamicExpression{orm.DynamicF("at").InZone(timezone.Zone{}).Date(), orm.DynamicF("day").InZone(seoul).Year(), orm.DynamicF("wall").InZone(seoul).TruncHour()} {
			if _, err := api.RecordsEvent.Expression(expression); err == nil {
				t.Fatal("invalid zoned expression accepted")
			}
		}
		if result, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).Filter(fields.ID.In()), calendarProjection(at.InZone(timezone.Zone{}).Date().ScalarExpression)); err == nil || result != nil || probe.calls() != 0 {
			t.Fatal("invalid zone reached I/O or empty-query shortcut", err, probe.calls())
		}
		day := at.InZone(seoul).Date()
		result, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).Filter(fields.Code.Exact("a")), calendarProjection(day.ScalarExpression))
		check(t, err)
		assertCalendarJSON(t, result, `[{"code":"a","value":"2021-01-01"}]`)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if result, err := orm.SelectInto(ctx, records.EventObjects.Using(backend).Filter(safe), calendarProjection(day.ScalarExpression)); !errors.Is(err, context.Canceled) || result != nil {
			t.Fatal("zoned read lost cancellation", result, err)
		}
		for _, fault := range []string{"scan", "iteration", "close"} {
			failing := &calendarReadProbe{calendarBackend: backend, fault: fault}
			rows, err := orm.SelectInto(t.Context(), records.EventObjects.Using(failing).Filter(safe), calendarProjection(day.ScalarExpression))
			if err == nil || rows != nil || failing.closed.Load() == 0 {
				t.Fatal("zoned read published partial rows or lost cursor ownership", fault, rows, err)
			}
		}
		if physical.Snapshot() != before {
			t.Fatal("zoned failure changed storage")
		}
	})
}
