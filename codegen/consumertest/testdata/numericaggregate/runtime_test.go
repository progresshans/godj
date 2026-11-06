package consumer

import (
	"context"
	"math"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func number(t *testing.T, text string) decimal.Decimal {
	t.Helper()
	value, err := decimal.Parse(text)
	check(t, err)
	return value
}
func optional[V comparable](t *testing.T, value orm.Optional[V], want V) {
	t.Helper()
	got, valid := value.Get()
	if !valid || got != want {
		t.Fatalf("optional value %#v/%v want %#v", got, valid, want)
	}
}

func TestNumericRuntime(t *testing.T) {
	withNumericBackends(t, func(t *testing.T, backend numericBackend, vendor string, writePhysical numericPhysicalWriter) {
		f := records.MeasureFields
		t.Run("typed_dynamic_group_and_having", func(t *testing.T) {
			var first records.Measure
			for index, price := range []string{"0.10", "0.20", "-0.30"} {
				amount := int64(2)
				if index == 0 {
					amount = 1
				}
				value, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(index != 1).WithGroupKey("a").WithAmount(amount).WithScore(float64(index+1)).WithPrice(number(t, price)).WithElapsed(duration.FromMicroseconds(int64(index))))
				check(t, err)
				if index == 0 {
					first = value
				}
			}
			_, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true).WithGroupKey("b"))
			check(t, err)
			probe := &numericProbe{numericBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			type metrics struct {
				Sum     orm.Optional[int64]
				Average orm.Optional[float64]
				Cost    orm.Optional[decimal.Decimal]
				Score   orm.Optional[float64]
				Elapsed orm.Optional[duration.Duration]
			}
			type result struct {
				Key     *string
				Metrics metrics
			}
			average := orm.Avg(f.Amount)
			cost := orm.Sum(f.Price).Distinct()
			score := orm.Avg(f.Score).Where(f.Enabled.Exact(true))
			aggregates := orm.Aggregate5(orm.Sum(f.Amount), average, cost, score, orm.Sum(f.Elapsed), func(a orm.Optional[int64], b orm.Optional[float64], c orm.Optional[decimal.Decimal], d orm.Optional[float64], e orm.Optional[duration.Duration]) metrics {
				return metrics{a, b, c, d, e}
			})
			typed, err := project.GroupRecordsMeasureBy(api.RecordsMeasure, orm.Project1(f.GroupKey, func(v *string) *string { return v }), aggregates, func(k *string, v metrics) result { return result{k, v} })
			check(t, err)
			typed = typed.Having(average.GreaterThan(orm.Some(1.5)), cost.GreaterThanOrEqual(orm.Some(decimal.Decimal{}))).OrderBy(average.Desc(), orm.GroupKey(f.GroupKey).Asc())
			dynamic, err := api.RecordsMeasure.GroupValues([]string{"group_key"}, []orm.DynamicAggregateInput{
				{Kind: query.ResultSum, Field: "amount"}, {Kind: query.ResultAvg, Field: "amount"}, {Kind: query.ResultSum, Field: "price", Distinct: true},
				{Kind: query.ResultAvg, Field: "score", Filter: []orm.LookupInput{{Key: "enabled", Value: true}}}, {Kind: query.ResultSum, Field: "elapsed"},
			})
			check(t, err)
			dynamic = dynamic.HavingDynamic(orm.GroupLookupInput{Column: 2, Lookup: query.LookupGreaterThan, Value: float64(1.5)}, orm.GroupLookupInput{Column: 3, Lookup: query.LookupGreaterThanOrEqual, Value: decimal.Decimal{}}).OrderByDynamic(orm.GroupOrderInput{Column: 2, Direction: query.Descending}, orm.GroupOrderInput{Column: 0, Direction: query.Ascending})
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("typed/dynamic numeric source and result plans differ")
			}
			rows, err := typed.All(t.Context())
			check(t, err)
			if len(rows) != 1 || rows[0].Key == nil || *rows[0].Key != "a" {
				t.Fatal("numeric HAVING or nullable group", rows)
			}
			optional(t, rows[0].Metrics.Sum, int64(5))
			optional(t, rows[0].Metrics.Average, float64(5)/3)
			optional(t, rows[0].Metrics.Cost, decimal.Decimal{})
			optional(t, rows[0].Metrics.Score, float64(2))
			optional(t, rows[0].Metrics.Elapsed, duration.FromMicroseconds(3))
			values, err := dynamic.All(t.Context())
			check(t, err)
			if len(values) != 1 || !values[0].Aggregates[0].Equal(query.Integer(5)) || !values[0].Aggregates[1].Equal(query.Float(float64(5)/3)) || !values[0].Aggregates[2].Equal(query.Decimal(decimal.Decimal{})) || !values[0].Aggregates[3].Equal(query.Float(2)) || !values[0].Aggregates[4].Equal(query.Duration(duration.FromMicroseconds(3))) {
				t.Fatal("dynamic numeric decoding", values)
			}
			for _, position := range [][2]int{{0, 0}, {1, 9}} {
				page, err := typed.Page(t.Context(), position[0], position[1])
				check(t, err)
				if page.Total != 1 || len(page.Rows) != 0 {
					t.Fatal("numeric page lost total", page)
				}
			}
			*rows[0].Key = "caller"
			_, err = records.MeasureObjects.Using(backend).Filter(f.ID.Exact(first.ID)).Update(t.Context(), orm.Assign(f.Amount, int64(9)))
			check(t, err)
			warm, err := typed.All(t.Context())
			check(t, err)
			if *warm[0].Key != "a" {
				t.Fatal("numeric cache aliases key")
			}
			optional(t, warm[0].Metrics.Sum, int64(5))
			fresh, err := typed.Fresh().All(t.Context())
			check(t, err)
			optional(t, fresh[0].Metrics.Sum, int64(13))
			before := len(probe.plans)
			invalid := dynamic.HavingDynamic(orm.GroupLookupInput{Column: 2, Lookup: query.LookupGreaterThan, Value: int64(1)})
			if values, err := invalid.All(t.Context()); err == nil || values != nil || len(probe.plans) != before {
				t.Fatal("integer AVG comparison reached I/O")
			}
		})
		t.Run("required_and_forward_numeric_fields", func(t *testing.T) {
			r := records.RequiredFields
			one, err := records.RequiredObjects.Create(t.Context(), backend, records.NewRequiredCreate(7, 2.5, number(t, "1.25"), duration.FromMicroseconds(3)))
			check(t, err)
			two, err := records.RequiredObjects.Create(t.Context(), backend, records.NewRequiredCreate(-2, -0.5, number(t, "-0.25"), duration.FromMicroseconds(-1)))
			check(t, err)
			source := records.RequiredObjects.Using(backend).Filter(r.ID.In(one.ID, two.ID))
			sums, err := orm.AggregateInto(t.Context(), source, orm.Aggregate4(orm.Sum(r.Amount), orm.Sum(r.Score), orm.Sum(r.Price), orm.Sum(r.Elapsed), func(a orm.Optional[int64], b orm.Optional[float64], c orm.Optional[decimal.Decimal], d orm.Optional[duration.Duration]) []any {
				return []any{nullableNumeric(a), nullableNumeric(b), nullableNumeric(c), nullableNumeric(d)}
			}))
			check(t, err)
			if !reflect.DeepEqual(sums, []any{int64(5), float64(2), number(t, "1"), duration.FromMicroseconds(2)}) {
				t.Fatal("required numeric sums", sums)
			}
			means, err := orm.AggregateInto(t.Context(), source, orm.Aggregate4(orm.Avg(r.Amount), orm.Avg(r.Score), orm.Avg(r.Price), orm.Avg(r.Elapsed), func(a, b orm.Optional[float64], c orm.Optional[decimal.Decimal], d orm.Optional[duration.Duration]) []any {
				return []any{nullableNumeric(a), nullableNumeric(b), nullableNumeric(c), nullableNumeric(d)}
			}))
			check(t, err)
			if !reflect.DeepEqual(means, []any{float64(2.5), float64(1), number(t, "0.5"), duration.FromMicroseconds(1)}) {
				t.Fatal("required numeric means", means)
			}
			a, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true).WithAmount(4).WithScore(2).WithPrice(number(t, "0.1")).WithElapsed(duration.FromMicroseconds(1)))
			check(t, err)
			b, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true).WithAmount(6).WithScore(4).WithPrice(number(t, "0.3")).WithElapsed(duration.FromMicroseconds(2)))
			check(t, err)
			var ids []int64
			for _, id := range []int64{a.ID, a.ID, b.ID, 0} {
				input := records.NewLinkCreate("x")
				if id != 0 {
					input = input.WithMeasureID(id)
				}
				row, err := records.LinkObjects.Create(t.Context(), backend, input)
				check(t, err)
				ids = append(ids, row.ID)
			}
			api, err := project.Using(backend)
			check(t, err)
			links := api.RecordsLink.Filter(records.LinkFields.ID.In(ids...))
			relations, err := project.BindRelations()
			check(t, err)
			related := relations.RecordsLink.Measure
			grouped, err := project.GroupRecordsLinkBy(links, orm.Project1(records.LinkFields.GroupKey, func(v string) string { return v }),
				orm.Aggregate5(orm.CountRows[records.Link](), orm.Sum(related.Amount).Distinct(), orm.Avg(related.Score), orm.Sum(related.Price).Where(related.Enabled.Exact(true)), orm.Avg(related.Elapsed), func(n int64, a orm.Optional[int64], b orm.Optional[float64], c orm.Optional[decimal.Decimal], d orm.Optional[duration.Duration]) []any {
					return []any{n, nullableNumeric(a), nullableNumeric(b), nullableNumeric(c), nullableNumeric(d)}
				}), func(_ string, values []any) []any { return values })
			check(t, err)
			rows, err := grouped.All(t.Context())
			check(t, err)
			if len(rows) != 1 || !reflect.DeepEqual(rows[0], []any{int64(4), int64(10), float64(8) / 3, number(t, "0.5"), duration.FromMicroseconds(1)}) {
				t.Fatal("forward aggregate lost optional root or result kind", rows)
			}
			dynamic, err := links.GroupValues([]string{"group_key"}, []orm.DynamicAggregateInput{{Kind: query.ResultCountAll}, {Kind: query.ResultSum, Field: "measure__amount", Distinct: true}, {Kind: query.ResultAvg, Field: "measure__score"}, {Kind: query.ResultSum, Field: "measure__price", Filter: []orm.LookupInput{{Key: "measure__enabled", Value: true}}}, {Kind: query.ResultAvg, Field: "measure__elapsed"}})
			check(t, err)
			if !dynamic.Plan().Equal(grouped.Plan()) {
				t.Fatal("forward numeric plans differ")
			}
			if values, err := dynamic.All(t.Context()); err != nil || len(values) != 1 {
				t.Fatal("dynamic forward numeric execution", values, err)
			}
			selected, err := project.AggregateRecordsLinkInto(t.Context(), links.Filter(related.Amount.GreaterThan(4)), orm.Aggregate1(orm.Sum(records.LinkFields.ID), func(v orm.Optional[int64]) orm.Optional[int64] { return v }))
			check(t, err)
			optional(t, selected, ids[2])
		})
		t.Run("scalar_slice_distinct_empty_and_cancellation", func(t *testing.T) {
			f := records.RequiredFields
			var ids []int64
			for _, amount := range []int64{3, 3, 6} {
				value, err := records.RequiredObjects.Create(t.Context(), backend, records.NewRequiredCreate(amount, 1, number(t, "0.01"), duration.FromMicroseconds(1)))
				check(t, err)
				ids = append(ids, value.ID)
			}
			probe := &numericProbe{numericBackend: backend}
			source := records.RequiredObjects.Using(probe).Filter(f.ID.In(ids...)).OrderBy(f.ID.Asc())
			aggregate := orm.Aggregate2(orm.Sum(f.Amount), orm.Avg(f.Amount), func(sum orm.Optional[int64], mean orm.Optional[float64]) [2]any {
				return [2]any{nullableNumeric(sum), nullableNumeric(mean)}
			})
			limited, err := source.Limit(2)
			check(t, err)
			value, err := orm.AggregateInto(t.Context(), limited, aggregate)
			check(t, err)
			if value != ([2]any{int64(6), float64(3)}) {
				t.Fatal("numeric scalar slice", value)
			}
			unique, err := orm.AggregateInto(t.Context(), source, orm.Aggregate2(orm.Sum(f.Amount).Distinct(), orm.Avg(f.Amount).Distinct(), func(sum orm.Optional[int64], mean orm.Optional[float64]) [2]any {
				return [2]any{nullableNumeric(sum), nullableNumeric(mean)}
			}))
			check(t, err)
			if unique != ([2]any{int64(9), float64(4.5)}) {
				t.Fatal("numeric scalar DISTINCT", unique)
			}
			var beforeQueries uint64
			counter, counts := backend.(interface{ QueryCount() uint64 })
			if counts {
				beforeQueries = counter.QueryCount()
			}
			empty, err := orm.AggregateInto(t.Context(), source.Filter(f.ID.In()), aggregate)
			check(t, err)
			if empty != ([2]any{}) {
				t.Fatal("empty aggregates are present", empty)
			}
			if counts && counter.QueryCount() != beforeQueries {
				t.Fatal("folded numeric empty executed SQL")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			before := len(probe.plans)
			if _, err := orm.AggregateInto(ctx, source, aggregate); err == nil || len(probe.plans) != before {
				t.Fatal("canceled numeric read reached backend")
			}
			var retained orm.QuerySet[records.Required]
			check(t, backend.Atomic(t.Context(), func(session db.Session) error {
				retained = records.RequiredObjects.Using(session).Filter(f.ID.In(ids...))
				value, err := orm.AggregateInto(t.Context(), retained, aggregate)
				if err != nil {
					return err
				}
				if value != ([2]any{int64(12), float64(4)}) {
					t.Fatal("borrowed numeric result", value)
				}
				return nil
			}))
			if _, err := orm.AggregateInto(t.Context(), retained, aggregate); err == nil {
				t.Fatal("retained numeric query outlived transaction")
			}
		})
		t.Run("native_failure_and_special_values", func(t *testing.T) {
			var ids []int64
			for _, amount := range []int64{math.MaxInt64, 1} {
				value, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true).WithAmount(amount))
				check(t, err)
				ids = append(ids, value.ID)
			}
			built := false
			_, err := orm.AggregateInto(t.Context(), records.MeasureObjects.Using(backend).Filter(f.ID.In(ids...)), orm.Aggregate1(orm.Sum(f.Amount), func(value orm.Optional[int64]) orm.Optional[int64] { built = true; return value }))
			if err == nil || built {
				t.Fatal("integer overflow published result")
			}
			if count, err := records.MeasureObjects.Using(backend).Filter(f.ID.In(ids...)).Count(t.Context()); err != nil || count != 2 {
				t.Fatal("failed read damaged stored state", count, err)
			}
			ids = nil
			for _, score := range []float64{math.Inf(1), math.Inf(-1)} {
				value, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true).WithScore(score))
				check(t, err)
				ids = append(ids, value.ID)
			}
			for _, expression := range []orm.AggregateExpression[records.Measure, orm.Optional[float64]]{orm.Sum(f.Score), orm.Avg(f.Score)} {
				actual, err := orm.AggregateInto(t.Context(), records.MeasureObjects.Using(backend).Filter(f.ID.In(ids...)), orm.Aggregate1(expression, func(v orm.Optional[float64]) orm.Optional[float64] { return v }))
				if vendor == "sqlite" {
					if err == nil || actual.Valid() {
						t.Fatal("SQLite NaN aggregate became NULL")
					}
				} else {
					check(t, err)
					value, ok := actual.Get()
					if !ok || !math.IsNaN(value) {
						t.Fatal("native PostgreSQL NaN narrowed", actual)
					}
				}
			}
			if vendor == "sqlite" {
				value, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true).WithFullWholePrice(number(t, "1000000000000")))
				check(t, err)
				physical := records.MeasureObjects.Using(backend).Filter(f.ID.Exact(value.ID))
				for _, test := range []struct {
					column, assignment string
					run                func() error
				}{
					{"price", "full_whole_price", func() error { _, err := evaluateNumeric(t, physical, orm.Sum(f.Price), "plain"); return err }},
					{"price", "x'02'", func() error { _, err := evaluateNumeric(t, physical, orm.Avg(f.Price), "plain"); return err }},
					{"amount", "0.5", func() error { _, err := evaluateNumeric(t, physical, orm.Sum(f.Amount), "plain"); return err }},
					{"elapsed", "0.5", func() error { _, err := evaluateNumeric(t, physical, orm.Avg(f.Elapsed), "plain"); return err }},
					{"score", "'not a number'", func() error { _, err := evaluateNumeric(t, physical, orm.Avg(f.Score), "plain"); return err }},
				} {
					check(t, test.run())
					check(t, writePhysical(test.column, test.assignment, value.ID))
					if test.run() == nil {
						t.Fatal("physical aggregate input coerced", test.column, test.assignment)
					}
					check(t, writePhysical(test.column, "NULL", value.ID))
					check(t, test.run())
				}
			} else {
				one, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true))
				check(t, err)
				two, err := records.MeasureObjects.Create(t.Context(), backend, records.NewMeasureCreate(true))
				check(t, err)
				physical := records.MeasureObjects.Using(backend).Filter(f.ID.In(one.ID, two.ID))
				check(t, writePhysical("price", "'NaN'::numeric", one.ID))
				cost := orm.Sum(f.Price)
				grouped, err := orm.GroupBy(physical, orm.Project1(f.GroupKey, func(v *string) *string { return v }), orm.Aggregate1(cost, func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v }), func(_ *string, v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v })
				check(t, err)
				if values, err := grouped.Having(cost.LessThan(orm.Some(decimal.Decimal{}))).All(t.Context()); err == nil || values != nil {
					t.Fatal("invalid Decimal disappeared behind HAVING")
				}
				check(t, writePhysical("price", "NULL", one.ID))
				check(t, writePhysical("elapsed", "INTERVAL '1 month'", one.ID))
				check(t, writePhysical("elapsed", "INTERVAL '-1 month'", two.ID))
				for _, expression := range []orm.AggregateExpression[records.Measure, orm.Optional[duration.Duration]]{orm.Sum(f.Elapsed), orm.Avg(f.Elapsed)} {
					if _, err := evaluateNumeric(t, physical, expression, "plain"); err == nil {
						t.Fatal("invalid interval months canceled into a valid aggregate")
					}
				}
				check(t, writePhysical("elapsed", "NULL", two.ID))
				check(t, writePhysical("elapsed", "INTERVAL '1000000000 days'", one.ID))
				if _, err := evaluateNumeric(t, physical, orm.Avg(f.Elapsed), "plain"); err == nil {
					t.Fatal("out-of-domain interval entered average")
				}
				check(t, writePhysical("elapsed", "NULL", one.ID))
				if _, err := evaluateNumeric(t, physical, orm.Sum(f.Elapsed), "plain"); err != nil {
					t.Fatal("failed native aggregate poisoned subsequent reads", err)
				}
			}
		})
	})
}
