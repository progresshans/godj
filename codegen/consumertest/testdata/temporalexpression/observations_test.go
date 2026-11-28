package consumer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"example.com/godj-project-bundle/planning"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func instant(text string) time.Time {
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		panic(err) // Fixed test literals and the checked-in synthetic input only.
	}
	return value
}

func period(text string) duration.Duration {
	value, err := duration.Parse(text)
	if err != nil {
		panic(err)
	}
	return value
}

func temporalValue[T time.Time | duration.Duration](value T) string {
	switch value := any(value).(type) {
	case time.Time:
		return value.UTC().Format("2006-01-02T15:04:05.000000Z")
	case duration.Duration:
		return value.String()
	default:
		panic("unsupported temporal fixture value")
	}
}
func optionalTemporal[T time.Time | duration.Duration](value orm.Optional[T]) any {
	if value, present := value.Get(); present {
		return temporalValue(value)
	}
	return nil
}
func pointerTemporal[T time.Time | duration.Duration](value *T) any {
	if value != nil {
		return temporalValue(*value)
	}
	return nil
}
func dynamicTemporal(value query.Value) any {
	if value.IsNull() {
		return nil
	}
	if instant, valid := value.DateTime(); valid {
		return temporalValue(instant)
	}
	if period, valid := value.Duration(); valid {
		return temporalValue(period)
	}
	panic("non-temporal dynamic fixture result")
}

type temporalSeed struct {
	schedules map[string]planning.Schedule
	events    map[string]records.Event
}

func seedTemporal(t *testing.T, backend db.Mutator) temporalSeed {
	t.Helper()
	data, err := os.ReadFile("input.json")
	check(t, err)
	var input struct {
		Schedules []struct {
			Code           string
			Offset, Anchor *string
		}
		Events []struct {
			Code, Bucket, Start           string
			Enabled                       bool
			Finish, Span, Other, Schedule *string
		}
		Details []struct {
			Event            string
			Offset, Occurred *string
		}
	}
	check(t, json.Unmarshal(data, &input))
	seed := temporalSeed{schedules: map[string]planning.Schedule{}, events: map[string]records.Event{}}
	for _, row := range input.Schedules {
		value := planning.NewScheduleCreate(row.Code)
		if row.Offset != nil {
			value = value.WithOffset(period(*row.Offset))
		}
		if row.Anchor != nil {
			value = value.WithAnchor(instant(*row.Anchor))
		}
		saved, err := planning.ScheduleObjects.Create(t.Context(), backend, value)
		check(t, err)
		seed.schedules[row.Code] = saved
	}
	for _, row := range input.Events {
		value := records.NewEventCreate(row.Code, row.Bucket, row.Enabled, instant(row.Start))
		if row.Finish != nil {
			value = value.WithFinish(instant(*row.Finish))
		}
		if row.Span != nil {
			value = value.WithSpan(period(*row.Span))
		}
		if row.Other != nil {
			value = value.WithOther(period(*row.Other))
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
		if row.Offset != nil {
			value = value.WithOffset(period(*row.Offset))
		}
		if row.Occurred != nil {
			value = value.WithOccurred(instant(*row.Occurred))
		}
		_, err := records.DetailObjects.Create(t.Context(), backend, value)
		check(t, err)
	}
	return seed
}

func temporalStorage(t *testing.T, reader db.Queryer) any {
	t.Helper()
	schedules, err := planning.ScheduleObjects.Using(reader).OrderBy(planning.ScheduleFields.ID.Asc()).All(t.Context())
	check(t, err)
	events, err := records.EventObjects.Using(reader).OrderBy(records.EventFields.ID.Asc()).All(t.Context())
	check(t, err)
	details, err := records.DetailObjects.Using(reader).OrderBy(records.DetailFields.ID.Asc()).All(t.Context())
	check(t, err)
	return []any{schedules, events, details}
}

func temporalProjection[T time.Time | duration.Duration](value orm.ScalarExpression[records.Event, T]) orm.Projection[records.Event, map[string]any] {
	return orm.Project2(records.EventFields.Code, value, func(code string, value orm.Optional[T]) map[string]any {
		return map[string]any{"code": code, "value": optionalTemporal(value)}
	})
}

// The observer owns operations and synthetic inputs, never Django outputs. The
// caller supplies a rollback transaction for cases that prepare or update rows.
func observeTemporal(t *testing.T, reader db.Session, name string) (any, error) {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		return nil, err
	}
	f := records.EventFields
	base := records.EventObjects.Using(reader).OrderBy(f.Code.Asc())
	finish := orm.DateTime(orm.F(f.Finish))
	start := orm.DateTime(orm.F(f.Start))
	span := orm.Duration(orm.F(f.Span))
	tick := duration.FromMicroseconds(1)
	shift := finish.AddExpression(span)
	difference := finish.DifferenceExpression(start)
	combined := span.AddExpression(orm.F(f.Other))
	first := instant("0001-01-01T00:00:00Z")
	last := instant("9999-12-31T23:59:59.999999Z")
	datetimes := func(value orm.DateTimeExpression[records.Event]) (any, error) {
		return orm.SelectInto(t.Context(), base, temporalProjection(value.ScalarExpression))
	}
	durations := func(value orm.DurationExpression[records.Event]) (any, error) {
		return orm.SelectInto(t.Context(), base, temporalProjection(value.ScalarExpression))
	}
	summary := func(source orm.QuerySet[records.Event]) (any, error) {
		return orm.AggregateInto(t.Context(), source,
			orm.Aggregate5(orm.Sum(difference), orm.Avg(difference), orm.Min(difference), orm.Max(difference), orm.Count(difference),
				func(total, average, minimum, maximum orm.Optional[duration.Duration], present int64) map[string]any {
					return map[string]any{"total": optionalTemporal(total), "average": optionalTemporal(average), "minimum": optionalTemporal(minimum), "maximum": optionalTemporal(maximum), "present": present}
				}))
	}
	update := func(assignments ...orm.UpdateAssignment[records.Event]) (any, error) {
		count, err := base.Update(t.Context(), assignments...)
		if err != nil {
			return nil, err
		}
		events, err := base.Fresh().All(t.Context())
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]any, 0, len(events))
		for _, event := range events {
			rows = append(rows, map[string]any{"code": event.Code, "start": temporalValue(event.Start), "finish": pointerTemporal(event.Finish), "span": pointerTemporal(event.Span), "other": pointerTemporal(event.Other)})
		}
		return map[string]any{"count": count, "rows": rows}, nil
	}
	switch name {
	case "datetime_add_duration":
		return datetimes(shift)
	case "duration_add_datetime":
		return datetimes(span.AddToExpression(finish))
	case "datetime_subtract_duration":
		return datetimes(finish.SubtractExpression(span))
	case "datetime_difference":
		return durations(difference)
	case "duration_add":
		return durations(combined)
	case "duration_subtract":
		return durations(span.SubtractExpression(orm.F(f.Other)))
	case "duration_negate":
		return durations(span.Negate())
	case "typed_null_duration":
		return datetimes(finish.AddExpression(orm.NullValue[records.Event, duration.Duration]()))
	case "typed_null_datetime":
		return datetimes(orm.DateTime(orm.NullValue[records.Event, time.Time]()).AddExpression(span))
	case "null_difference":
		return durations(finish.DifferenceExpression(orm.NullValue[records.Event, time.Time]()))
	case "case_fallback":
		return datetimes(orm.DateTime(orm.Case(shift, orm.When(f.Finish.IsNull(true), start.Add(tick)))))
	case "related_shift":
		return datetimes(finish.AddExpression(relations.RecordsEvent.Schedule.Offset))
	case "reverse_shift":
		return datetimes(finish.AddExpression(relations.RecordsEvent.Detail.Offset))
	case "compare_computed":
		return orm.SelectInto(t.Context(), base.Filter(orm.GreaterThanExpressions(shift, start.Add(tick))), temporalProjection(shift.ScalarExpression))
	case "order_page":
		selected, err := orm.SelectQuery(base.Filter(shift.IsNull(false)).OrderBy(shift.Desc(), f.Code.Asc()), temporalProjection(shift.ScalarExpression))
		if err != nil {
			return nil, err
		}
		page, err := selected.Page(t.Context(), 3, 1)
		return page.Rows, err
	case "computed_aggregate":
		return summary(base)
	case "empty_aggregate":
		return summary(base.Filter(f.ID.In()))
	case "all_null_aggregate":
		return summary(base.Filter(f.Code.Exact("c")))
	case "distinct_filtered_span":
		return orm.AggregateInto(t.Context(), base, orm.Aggregate2(orm.Sum(combined).Distinct().Where(f.Enabled.Exact(true)), orm.Avg(combined).Distinct().Where(f.Enabled.Exact(true)),
			func(total, average orm.Optional[duration.Duration]) map[string]any {
				return map[string]any{"total": optionalTemporal(total), "average": optionalTemporal(average)}
			}))
	case "groups_having":
		total := orm.Sum(difference)
		grouped, err := orm.GroupBy(base.OrderBy(), orm.Project1(f.Bucket, func(bucket string) string { return bucket }),
			orm.Aggregate2(total, orm.Avg(difference), func(total, average orm.Optional[duration.Duration]) map[string]any {
				return map[string]any{"total": optionalTemporal(total), "average": optionalTemporal(average)}
			}),
			func(bucket string, values map[string]any) map[string]any { values["bucket"] = bucket; return values })
		if err != nil {
			return nil, err
		}
		return grouped.Having(total.GreaterThanOrEqual(orm.Some(duration.Duration{}))).OrderBy(f.Bucket.Asc()).All(t.Context())
	case "collection_duration":
		collection := relations.PlanningSchedule.Events
		value := orm.DateTime(collection.Finish).DifferenceExpression(collection.Start)
		counted := orm.Duration(collection.Span).Add(tick)
		grouped, err := orm.GroupBy(planning.ScheduleObjects.Using(reader), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }),
			orm.Aggregate2(orm.Sum(value), orm.Count(counted), func(total orm.Optional[duration.Duration], present int64) map[string]any {
				return map[string]any{"total": optionalTemporal(total), "present": present}
			}),
			func(code string, values map[string]any) map[string]any { values["code"] = code; return values })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
	case "collection_shift_min":
		collection := relations.PlanningSchedule.Events
		value := orm.DateTime(collection.Finish).AddExpression(collection.Span)
		grouped, err := orm.GroupBy(planning.ScheduleObjects.Using(reader), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }),
			orm.Aggregate1(orm.Min(value), func(value orm.Optional[time.Time]) any { return optionalTemporal(value) }),
			func(code string, value any) map[string]any { return map[string]any{"code": code, "value": value} })
		if err != nil {
			return nil, err
		}
		return grouped.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
	case "derived_duration":
		selected, err := orm.SelectQuery(base, temporalProjection(combined.ScalarExpression))
		if err != nil {
			return nil, err
		}
		stage, err := orm.Derive(selected)
		if err != nil {
			return nil, err
		}
		value := orm.Duration(orm.OptionalOperand(orm.SelectedField(stage, combined)))
		summary, err := orm.AggregateResults(stage, orm.Aggregate2(orm.Sum(value), orm.Max(value),
			func(total, maximum orm.Optional[duration.Duration]) map[string]any {
				return map[string]any{"total": optionalTemporal(total), "maximum": optionalTemporal(maximum)}
			}))
		if err != nil {
			return nil, err
		}
		return summary.One(t.Context())
	case "update_shift":
		return update(orm.AssignExpression(f.Finish, shift))
	case "update_old_row":
		return update(orm.AssignExpression(f.Finish, shift), orm.AssignExpression(f.Span, difference))
	case "update_null":
		return update(orm.AssignExpression(f.Finish, finish.AddExpression(orm.NullValue[records.Event, duration.Duration]())))
	case "large_datetime_difference", "negative_datetime_difference":
		base = base.Filter(f.Code.Exact("a"))
		if name == "negative_datetime_difference" {
			first, last = last, first
		}
		return durations(orm.DateTime(orm.Value[records.Event](last)).Difference(first))
	case "datetime_upper_overflow":
		base = base.Filter(f.Code.Exact("a"))
		return datetimes(orm.DateTime(orm.Value[records.Event](last)).Add(tick))
	case "datetime_lower_overflow":
		base = base.Filter(f.Code.Exact("a"))
		return datetimes(orm.DateTime(orm.Value[records.Event](first)).Subtract(tick))
	case "duration_int64_overflow":
		base = base.Filter(f.Code.Exact("a"))
		return durations(orm.Duration(orm.Value[records.Event](duration.FromMicroseconds(math.MaxInt64))).Add(tick))
	case "duration_full_model_literal", "duration_model_overflow":
		base = base.Filter(f.Code.Exact("a"))
		value := orm.Duration(orm.Value[records.Event](duration.Duration{Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}))
		if name == "duration_model_overflow" {
			value = value.Add(tick)
		}
		return durations(value)
	case "case_unselected_overflow":
		return datetimes(orm.DateTime(orm.Case(finish, orm.When(f.Code.Exact("never"), orm.DateTime(orm.Value[records.Event](last)).AddExpression(span)))))
	case "mean_fractional_shift", "mean_integral_shift", "mean_zero_shift", "mean_null_shift", "mean_reverse_shift", "mean_add_duration", "mean_negate_duration", "mean_reaggregate":
		return observeTemporalMean(t, reader, name)
	case "update_datetime_overflow":
		if _, err := base.Filter(f.Code.Exact("f")).Update(t.Context(), orm.Assign(f.Finish, last)); err != nil {
			return nil, err
		}
		return update(orm.AssignExpression(f.Finish, finish.Add(tick)))
	case "update_duration_overflow":
		if _, err := base.Filter(f.Code.Exact("f")).Update(t.Context(), orm.Assign(f.Span, duration.FromMicroseconds(math.MaxInt64))); err != nil {
			return nil, err
		}
		return update(orm.AssignExpression(f.Span, span.Add(tick)))
	case "scale_integer", "scale_integer_reverse", "scale_divide_restore", "scale_half_final", "scale_float_tie", "scale_decimal_tie", "fraction_sum", "fraction_distinct_sum", "fraction_shift", "scale_zero_divisor", "duration_ratio":
		return observeDurationScaling(t, reader, name)
	default:
		return nil, fmt.Errorf("unknown temporal observation %q", name)
	}
}

func observeTemporalMean(t *testing.T, reader db.Session, name string) (any, error) {
	f := records.EventFields
	base := records.EventObjects.Using(reader).Filter(f.Code.In("a", "b"))
	values := []int64{1, 2}
	if name == "mean_integral_shift" {
		values = []int64{1, 3}
	} else if name == "mean_zero_shift" {
		values = []int64{-1, 1}
	}
	for index, code := range []string{"a", "b"} {
		assignment := orm.Assign(f.Span, duration.FromMicroseconds(values[index]))
		if name == "mean_null_shift" {
			assignment = orm.AssignNull(f.Span)
		}
		if _, err := base.Filter(f.Code.Exact(code)).Update(t.Context(), assignment); err != nil {
			return nil, err
		}
	}
	average := orm.Avg(orm.Duration(orm.F(f.Span)))
	grouped, err := orm.GroupBy(base.OrderBy(), orm.Project1(f.Bucket, func(value string) string { return value }),
		orm.Aggregate1(average, func(value orm.Optional[duration.Duration]) orm.Optional[duration.Duration] { return value }),
		func(bucket string, value orm.Optional[duration.Duration]) map[string]any {
			return map[string]any{"bucket": bucket, "period": optionalTemporal(value)}
		})
	if err != nil {
		return nil, err
	}
	stage, err := orm.Derive(grouped)
	if err != nil {
		return nil, err
	}
	bucket, period := orm.SelectedField(stage, f.Bucket), orm.SelectedAggregate(stage, average)
	value := orm.Duration(orm.OptionalOperand(period))
	if name == "mean_reaggregate" {
		result, err := orm.AggregateResults(stage, orm.Aggregate2(orm.Sum(value), orm.Avg(value), func(total, average orm.Optional[duration.Duration]) map[string]any {
			return map[string]any{"total": optionalTemporal(total), "average": optionalTemporal(average)}
		}))
		if err != nil {
			return nil, err
		}
		return result.One(t.Context())
	}
	if name == "mean_add_duration" || name == "mean_negate_duration" {
		if name == "mean_add_duration" {
			value = value.Add(duration.FromMicroseconds(1))
		} else {
			value = value.Negate()
		}
		result, err := orm.SelectResults(stage, orm.Project2(bucket, value, func(bucket string, value orm.Optional[duration.Duration]) map[string]any {
			return map[string]any{"bucket": bucket, "value": optionalTemporal(value)}
		}))
		if err != nil {
			return nil, err
		}
		return result.All(t.Context())
	}
	far := instant("9999-01-01T00:00:00.000001Z")
	shift := orm.DateTime(orm.Value[orm.ResultOf[records.Event]](far)).AddExpression(value)
	if name == "mean_reverse_shift" {
		shift = value.AddTo(far)
	}
	result, err := orm.SelectResults(stage, orm.Project3(bucket, period, shift, func(bucket string, period orm.Optional[duration.Duration], value orm.Optional[time.Time]) map[string]any {
		return map[string]any{"bucket": bucket, "period": optionalTemporal(period), "value": optionalTemporal(value)}
	}))
	if err != nil {
		return nil, err
	}
	return result.All(t.Context())
}
