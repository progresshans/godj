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
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func observeDurationScaling(t *testing.T, reader db.Session, name string) (any, error) {
	f := records.EventFields
	base := records.EventObjects.Using(reader).OrderBy(f.Code.Asc())
	span := orm.Duration(orm.F(f.Span))
	half := orm.DivideDuration(span, int64(2))
	var expression orm.DurationExpression[records.Event]
	switch name {
	case "scale_integer":
		expression = orm.ScaleDuration(span, int64(2))
	case "scale_integer_reverse":
		api, err := project.UsingSession(reader)
		if err != nil {
			return nil, err
		}
		value, err := api.RecordsEvent.Expression(orm.DynamicValue(int64(2)).Multiply(orm.DynamicF("span")))
		if err != nil {
			return nil, err
		}
		return project.SelectRecordsEventInto(t.Context(), api.RecordsEvent.OrderBy(f.Code.Asc()), orm.Project2(f.Code, value, func(code string, value query.Value) map[string]any {
			return map[string]any{"code": code, "value": dynamicTemporal(value)}
		}))
	case "scale_divide_restore":
		expression = orm.ScaleDuration(orm.DivideDuration(span, int64(3)), int64(3))
	case "scale_half_final":
		expression = half
	case "scale_float_tie":
		base = base.Filter(f.Code.Exact("a"))
		expression = orm.ScaleDuration(orm.Value[records.Event](duration.FromMicroseconds(5)), 0.1)
	case "scale_decimal_tie":
		base = base.Filter(f.Code.Exact("a"))
		expression = orm.ScaleDuration(orm.Value[records.Event](duration.FromMicroseconds(5)), decimal.Decimal{Coefficient: "1", Exponent: -1})
	case "fraction_sum", "fraction_distinct_sum":
		aggregate := orm.Sum(half)
		if name == "fraction_distinct_sum" {
			aggregate = aggregate.Distinct()
		}
		return orm.AggregateInto(t.Context(), base.Filter(f.Code.In("a", "e")), orm.Aggregate1(aggregate, func(value orm.Optional[duration.Duration]) map[string]any {
			return map[string]any{"total": optionalTemporal(value)}
		}))
	case "fraction_shift":
		shift := orm.DateTime(orm.F(f.Finish)).AddExpression(half)
		return orm.SelectInto(t.Context(), base.Filter(f.Code.Exact("a")), temporalProjection(shift.ScalarExpression))
	case "scale_zero_divisor":
		base = base.Filter(f.Code.Exact("a"))
		expression = orm.DivideDuration(span, int64(0))
	case "duration_ratio":
		ratio := span.RatioExpression(orm.F(f.Other))
		return orm.SelectInto(t.Context(), base, orm.Project2(f.Code, ratio, func(code string, value orm.Optional[float64]) map[string]any {
			var actual any
			if number, valid := value.Get(); valid {
				actual = number
			}
			return map[string]any{"code": code, "value": actual}
		}))
	default:
		return nil, fmt.Errorf("unknown duration scaling observation %q", name)
	}
	return orm.SelectInto(t.Context(), base, temporalProjection(expression.ScalarExpression))
}

func checkDurationScaling(t *testing.T, backend temporalBackend, vendor string, physical temporalPhysical) {
	f := records.EventFields
	span := orm.Duration(orm.F(f.Span))
	half := orm.DivideDuration(span, int64(2))
	third := orm.DivideDuration(span, int64(3))
	restored := orm.ScaleDuration(third, int64(3))
	tick := duration.FromMicroseconds(1)
	original := `[{"code":"a","value":"00:00:00.000001"},{"code":"b","value":"-1 23:59:59.999999"},{"code":"c","value":null},{"code":"d","value":"00:00:00"},{"code":"e","value":"00:00:00.000001"},{"code":"f","value":"2 03:00:00.000003"}]`

	t.Run("typed_dynamic_duration_scaling", func(t *testing.T) {
		probe := &temporalReadProbe{temporalBackend: backend}
		api, err := project.Using(probe)
		check(t, err)
		for _, pair := range []struct {
			typed   orm.DurationExpression[records.Event]
			dynamic orm.DynamicExpression
		}{
			{restored, orm.DynamicF("span").Divide(int64(3)).Multiply(int64(3))},
			{orm.ScaleDuration(orm.DivideDuration(span, 0.5), 0.5), orm.DynamicF("span").Divide(0.5).Multiply(0.5)},
			{orm.ScaleDuration(orm.DivideDuration(span, decimal.Decimal{Coefficient: "1", Exponent: 1000}), decimal.Decimal{Coefficient: "1", Exponent: 1000}), orm.DynamicF("span").Divide(decimal.Decimal{Coefficient: "1", Exponent: 1000}).Multiply(decimal.Decimal{Coefficient: "1", Exponent: 1000})},
		} {
			start := probe.calls()
			typed, err := orm.SelectInto(t.Context(), records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), temporalProjection(pair.typed.ScalarExpression))
			check(t, err)
			bound, err := api.RecordsEvent.Expression(pair.dynamic)
			check(t, err)
			dynamic, err := project.SelectRecordsEventInto(t.Context(), api.RecordsEvent.OrderBy(f.Code.Asc()), orm.Project2(f.Code, bound, func(code string, value query.Value) map[string]any {
				return map[string]any{"code": code, "value": dynamicTemporal(value)}
			}))
			check(t, err)
			assertTemporalJSON(t, typed, original)
			if !reflect.DeepEqual(typed, dynamic) || probe.calls() != start+2 || !probe.plans[start].Equal(probe.plans[start+1]) {
				t.Fatal("generated scaling facades changed their AST or exact result", typed, dynamic)
			}
		}
		// A second generated app owns a different field set and model type.
		other := orm.ScaleDuration(orm.DivideDuration(orm.F(planning.ScheduleFields.Offset), int64(3)), int64(3))
		values, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe).OrderBy(planning.ScheduleFields.Code.Asc()), orm.Project2(planning.ScheduleFields.Code, other, func(code string, value orm.Optional[duration.Duration]) map[string]any {
			return map[string]any{"code": code, "value": optionalTemporal(value)}
		}))
		check(t, err)
		assertTemporalJSON(t, values, `[{"code":"empty","value":"00:00:00"},{"code":"missing","value":null},{"code":"negative","value":"-1 23:59:59.999999"},{"code":"normal","value":"01:30:00.000001"}]`)
		ratio := restored.Ratio(tick)
		values, err = orm.SelectInto(t.Context(), records.EventObjects.Using(probe).OrderBy(f.Code.Asc()), orm.Project2(f.Code, ratio, func(code string, value orm.Optional[float64]) map[string]any {
			var actual any
			if number, valid := value.Get(); valid {
				actual = number
			}
			return map[string]any{"code": code, "value": actual}
		}))
		check(t, err)
		assertTemporalJSON(t, values, `[{"code":"a","value":1},{"code":"b","value":-1},{"code":"c","value":null},{"code":"d","value":0},{"code":"e","value":1},{"code":"f","value":183600000003}]`)
	})

	t.Run("selected_fraction_stages_and_page", func(t *testing.T) {
		probe := &temporalReadProbe{temporalBackend: backend}
		selected, err := orm.SelectQuery(records.EventObjects.Using(probe).Filter(f.Code.In("a", "e")).OrderBy(f.Code.Asc()), temporalProjection(half.ScalarExpression))
		check(t, err)
		materialized, err := selected.All(t.Context())
		check(t, err)
		assertTemporalJSON(t, materialized, `[{"code":"a","value":"00:00:00"},{"code":"e","value":"00:00:00"}]`)
		stage, err := orm.Derive(selected)
		check(t, err)
		fraction := orm.Duration(orm.OptionalOperand(orm.SelectedField(stage, half)))
		whole := orm.ScaleDuration(fraction, int64(2))
		result, err := orm.SelectResults(stage, orm.Project1(whole, func(value orm.Optional[duration.Duration]) any { return optionalTemporal(value) }))
		check(t, err)
		values, err := result.All(t.Context())
		check(t, err)
		assertTemporalJSON(t, values, `["00:00:00.000001","00:00:00.000001"]`)
		start := probe.calls()
		page, err := result.Filter(fraction.GreaterThan(duration.Duration{})).Page(t.Context(), 1, 1)
		check(t, err)
		if page.Total != 2 || probe.calls() != start+1 {
			t.Fatal("fraction page lost one-statement membership", page.Total, probe.calls()-start)
		}
		assertTemporalJSON(t, page.Rows, `["00:00:00.000001"]`)
		summary, err := orm.AggregateResults(stage, orm.Aggregate2(orm.Sum(fraction), orm.Avg(fraction), func(sum, average orm.Optional[duration.Duration]) []any {
			return []any{optionalTemporal(sum), optionalTemporal(average)}
		}))
		check(t, err)
		grouped, err := summary.One(t.Context())
		check(t, err)
		assertTemporalJSON(t, grouped, `["00:00:00.000001","00:00:00"]`)
	})

	t.Run("fraction_groups_distinct_and_collections", func(t *testing.T) {
		base := records.EventObjects.Using(backend).Filter(f.Code.In("a", "e"))
		summary, err := orm.AggregateInto(t.Context(), base, orm.Aggregate3(orm.Sum(half), orm.Sum(half).Distinct(), orm.Avg(half), func(total, distinct, average orm.Optional[duration.Duration]) []any {
			return []any{optionalTemporal(total), optionalTemporal(distinct), optionalTemporal(average)}
		}))
		check(t, err)
		assertTemporalJSON(t, summary, `["00:00:00.000001","00:00:00","00:00:00"]`)
		total := orm.Sum(half)
		grouped, err := orm.GroupBy(base, orm.Project1(f.Bucket, func(bucket string) string { return bucket }), orm.Aggregate1(total, func(value orm.Optional[duration.Duration]) any { return optionalTemporal(value) }), func(bucket string, value any) map[string]any { return map[string]any{"bucket": bucket, "value": value} })
		check(t, err)
		values, err := grouped.Having(total.GreaterThan(orm.Some(duration.Duration{}))).All(t.Context())
		check(t, err)
		assertTemporalJSON(t, values, `[{"bucket":"first","value":"00:00:00.000001"}]`)
		relations, err := project.BindRelations()
		check(t, err)
		period := orm.DivideDuration(relations.PlanningSchedule.Events.Span, int64(2))
		collection, err := orm.GroupBy(planning.ScheduleObjects.Using(backend), orm.Project1(planning.ScheduleFields.Code, func(code string) string { return code }), orm.Aggregate2(orm.Sum(period), orm.Count(period), func(total orm.Optional[duration.Duration], count int64) map[string]any {
			return map[string]any{"total": optionalTemporal(total), "count": count}
		}), func(code string, values map[string]any) map[string]any { values["code"] = code; return values })
		check(t, err)
		values, err = collection.OrderBy(planning.ScheduleFields.Code.Asc()).All(t.Context())
		check(t, err)
		assertTemporalJSON(t, values, `[{"code":"empty","total":null,"count":0},{"code":"missing","total":"00:00:00","count":1},{"code":"negative","total":"1 01:30:00.000001","count":2},{"code":"normal","total":"00:00:00","count":2}]`)
	})

	t.Run("factor_domains_and_boundary_rounding", func(t *testing.T) {
		base := records.EventObjects.Using(backend).Filter(f.Code.Exact("a"))
		five := orm.Value[records.Event](duration.FromMicroseconds(5))
		exact := orm.ScaleDuration(five, decimal.Decimal{Coefficient: "1", Exponent: -1})
		floating := orm.ScaleDuration(five, 0.1)
		values, err := orm.SelectInto(t.Context(), base, orm.Project2(exact, floating, func(exact, floating orm.Optional[duration.Duration]) []any {
			return []any{optionalTemporal(exact), optionalTemporal(floating)}
		}))
		check(t, err)
		assertTemporalJSON(t, values, `[["00:00:00","00:00:00.000001"]]`)
		shift := orm.DateTime(orm.F(f.Finish)).AddExpression(half)
		shifted, err := orm.SelectInto(t.Context(), base, temporalProjection(shift.ScalarExpression))
		check(t, err)
		assertTemporalJSON(t, shifted, `[{"code":"a","value":"2024-02-29T00:00:00.000002Z"}]`)
		chosen := orm.Duration(orm.Case(orm.F(f.Span), orm.When(f.Code.Exact("a"), half)))
		scaled := orm.ScaleDuration(chosen, int64(2))
		periods, err := orm.SelectInto(t.Context(), base, temporalProjection(scaled.ScalarExpression))
		check(t, err)
		assertTemporalJSON(t, periods, `[{"code":"a","value":"00:00:00.000001"}]`)
	})

	t.Run("scaling_assignment_savepoint_and_cache", func(t *testing.T) {
		rollbackTemporal(t, backend, func(session db.Session) {
			base := records.EventObjects.Using(session).OrderBy(f.Code.Asc())
			held, err := base.All(t.Context())
			check(t, err)
			sibling := base.Filter()
			_, err = sibling.All(t.Context())
			check(t, err)
			count, err := base.Update(t.Context(), orm.AssignExpression(f.Span, half), orm.AssignExpression(f.Other, orm.ScaleDuration(span, int64(3))))
			check(t, err)
			fresh, err := base.All(t.Context())
			check(t, err)
			again, err := sibling.All(t.Context())
			check(t, err)
			if count != 6 || !reflect.DeepEqual(held, again) || fresh[0].Span == nil || *fresh[0].Span != (duration.Duration{}) || fresh[0].Other == nil || *fresh[0].Other != duration.FromMicroseconds(3) {
				t.Fatal("fraction assignment lost same-row inputs, rounding or cache ownership", count, fresh)
			}
			divisor := orm.Case(orm.Value[records.Event](int64(2)), orm.When(f.Code.Exact("f"), orm.Value[records.Event](int64(0))))
			count, err = base.Update(t.Context(), orm.AssignExpression(f.Span, orm.DivideDurationExpressions(span, divisor)))
			if count != 0 {
				t.Fatal("failing fractional write published a count", count)
			}
			nativeTemporalError(t, vendor, err, "22012")
			after, err := base.Fresh().All(t.Context())
			check(t, err)
			if !reflect.DeepEqual(fresh, after) {
				t.Fatal("fraction failure changed earlier rows or parent writes")
			}
			count, err = base.Filter(f.Code.Exact("a")).UpdateDynamic(t.Context(), orm.DynamicUpdateInput{Field: "span", Value: orm.DynamicF("other").Divide(int64(3))})
			check(t, err)
			if count != 1 {
				t.Fatal("fraction failure poisoned its parent transaction")
			}
		})
	})

	t.Run("fraction_authority_context_and_cursor_failures", func(t *testing.T) {
		probe := &temporalReadProbe{temporalBackend: backend}
		base := records.EventObjects.Using(probe)
		forged := orm.NewNullableDurationField[records.Event](ir.Field{Name: "span", GoName: "Span", Column: "other", Kind: ir.FieldDuration, Nullable: true})
		if rows, err := orm.SelectInto(t.Context(), base, temporalProjection(orm.DivideDuration(orm.F(forged), int64(2)).ScalarExpression)); err == nil || rows != nil || probe.calls() != 0 {
			t.Fatal("fraction expression accepted forged source metadata", err)
		}
		relations, err := project.BindRelations()
		check(t, err)
		if rows, err := orm.SelectInto(t.Context(), planning.ScheduleObjects.Using(probe), orm.Project1(orm.DivideDuration(relations.PlanningSchedule.Events.Span, int64(2)), func(value orm.Optional[duration.Duration]) any { return optionalTemporal(value) })); err == nil || rows != nil || probe.calls() != 0 {
			t.Fatal("fraction collection escaped aggregate cardinality", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if rows, err := orm.SelectInto(cancelled, base, temporalProjection(half.ScalarExpression)); !errors.Is(err, context.Canceled) || rows != nil || probe.calls() != 0 {
			t.Fatal("cancelled fractional projection reached I/O", err)
		}
		for _, fault := range []string{"scan", "iteration", "close"} {
			probe.fault = fault
			before := probe.closed.Load()
			if rows, err := orm.SelectInto(t.Context(), base, temporalProjection(half.ScalarExpression)); err == nil || rows != nil || probe.closed.Load() <= before {
				t.Fatal("fraction cursor failure published partial output or leaked", fault, err)
			}
		}
		probe.fault = ""
		// The native snapshot independently covers every physical model row.
		before := physical.Snapshot()
		shift := orm.DateTime(orm.Value[records.Event](time.Date(2500, 1, 1, 0, 0, 0, 1000, time.UTC))).AddExpression(orm.DivideDuration(orm.Value[records.Event](tick), int64(2)))
		values, err := orm.SelectInto(t.Context(), base.Filter(f.Code.Exact("a")), temporalProjection(shift.ScalarExpression))
		check(t, err)
		assertTemporalJSON(t, values, `[{"code":"a","value":"2500-01-01T00:00:00.000002Z"}]`)
		if physical.Snapshot() != before {
			t.Fatal("fraction read changed stored state")
		}
	})
}
