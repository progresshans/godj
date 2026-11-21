package consumer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type computedReadProbe struct {
	db.Session
	plans  []query.Plan
	fault  string
	closes int
}

func (p *computedReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.plans = append(p.plans, plan)
	rows, err := p.Session.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	return &computedRows{Rows: rows, owner: p}, nil
}
func (p *computedReadProbe) ValidateSession(ctx context.Context) error {
	if value, ok := p.Session.(db.SessionValidator); ok {
		return value.ValidateSession(ctx)
	}
	return ctx.Err()
}

type computedRows struct {
	db.Rows
	owner *computedReadProbe
}

func (r *computedRows) Scan(destinations ...any) error {
	if r.owner.fault == "scan" {
		return errors.New("computed scan failure")
	}
	return r.Rows.Scan(destinations...)
}
func (r *computedRows) Close() error {
	r.owner.closes++
	err := r.Rows.Close()
	if r.owner.fault == "close" {
		return errors.Join(err, errors.New("computed close failure"))
	}
	return err
}

func TestComputedRuntime(t *testing.T) {
	withComputedBackends(t, func(t *testing.T, backend computedBackend, vendor string, physical computedPhysicalWriter) {
		fixture := readComputedReference(t, vendor)
		f := records.MeasureFields
		within := func(t *testing.T, run func(db.Session)) {
			rollback := errors.New("computed runtime owned rollback")
			err := backend.Atomic(t.Context(), func(session db.Session) error { seedComputed(t, session, fixture.Input); run(session); return rollback })
			if !errors.Is(err, rollback) {
				t.Fatal("runtime rollback failed", err)
			}
			count, err := records.MeasureObjects.Using(backend).Count(t.Context())
			check(t, err)
			if count != 0 {
				t.Fatal("runtime rows escaped rollback")
			}
		}
		t.Run("typed_dynamic_projection_and_predicates", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &computedReadProbe{Session: session}
				raw := records.MeasureObjects.Using(probe).OrderBy(f.ID.Asc())
				api, err := project.UsingSession(probe)
				check(t, err)
				expression := orm.AddExpressions(orm.F(f.Amount), orm.F(f.Other))
				dynamic, err := api.RecordsMeasure.Expression(orm.DynamicF("amount").Add(orm.DynamicF("other")))
				check(t, err)
				type row struct {
					Code  string
					Value query.Value
				}
				typed, err := orm.SelectInto(t.Context(), raw.Filter(orm.Or(expression.GreaterThan(5), f.Enabled.Exact(false))), orm.Project2(f.Code, expression, func(code string, value orm.Optional[int64]) row {
					v := query.Null()
					if n, ok := value.Get(); ok {
						v = query.Integer(n)
					}
					return row{code, v}
				}))
				check(t, err)
				untyped, err := project.SelectRecordsMeasureInto(t.Context(), api.RecordsMeasure.OrderBy(f.ID.Asc()).Filter(orm.Or(dynamic.GreaterThan(int64(5)), f.Enabled.Exact(false))), orm.Project2(f.Code, dynamic, func(code string, value query.Value) row { return row{code, value} }))
				check(t, err)
				if !reflect.DeepEqual(typed, untyped) || len(typed) != 4 || !probe.plans[0].Equal(probe.plans[1]) {
					t.Fatal("typed/dynamic computed AST or values differ", typed, untyped)
				}
				negative, err := orm.SelectInto(t.Context(), raw.Filter(orm.Not(expression.Exact(7))), orm.Project1(f.Code, func(v string) string { return v }))
				check(t, err)
				if !reflect.DeepEqual(negative, []string{"c", "e"}) {
					t.Fatal("computed NOT included NULL or lost SQL semantics", negative)
				}
				first := orm.Add(orm.F(f.Amount), int64(1))
				second := orm.Multiply(first, int64(2))
				first = orm.Numeric(orm.Value[records.Measure](int64(100)))
				values, err := orm.SelectInto(t.Context(), raw.Filter(f.Code.Exact("a")), orm.Project2(first, second, func(a, b orm.Optional[int64]) [2]orm.Optional[int64] { return [2]orm.Optional[int64]{a, b} }))
				check(t, err)
				a, _ := values[0][0].Get()
				b, _ := values[0][1].Get()
				if a != 100 || b != 12 {
					t.Fatal("rebound handle mutated an existing expression")
				}
			})
		})
		t.Run("group_keys_aggregates_and_having", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				key := orm.Remainder(orm.F(f.Amount), int64(2))
				value := orm.Add(orm.F(f.Amount), int64(1))
				sum := orm.Sum(value)
				type metrics struct {
					Sum      orm.Optional[int64]
					Average  orm.Optional[float64]
					Count    int64
					Min, Max orm.Optional[int64]
				}
				type row struct {
					Key     orm.Optional[int64]
					Metrics metrics
				}
				grouped, err := project.GroupRecordsMeasureBy(api.RecordsMeasure, orm.Project1(key, func(v orm.Optional[int64]) orm.Optional[int64] { return v }),
					orm.Aggregate5(sum, orm.Avg(value), orm.Count(value), orm.Min(value), orm.Max(value), func(a orm.Optional[int64], b orm.Optional[float64], c int64, d, e orm.Optional[int64]) metrics {
						return metrics{a, b, c, d, e}
					}),
					func(k orm.Optional[int64], v metrics) row { return row{k, v} })
				check(t, err)
				grouped = grouped.Having(sum.GreaterThan(orm.Some(int64(0)))).OrderBy(orm.GroupKey(key).Asc())
				rows, err := grouped.All(t.Context())
				check(t, err)
				if len(rows) != 2 {
					t.Fatal("computed group cardinality", rows)
				}
				k, _ := rows[1].Key.Get()
				total, _ := rows[1].Metrics.Sum.Get()
				average, _ := rows[1].Metrics.Average.Get()
				minimum, _ := rows[1].Metrics.Min.Get()
				maximum, _ := rows[1].Metrics.Max.Get()
				if k != 1 || total != 16 || average != 16.0/3 || rows[1].Metrics.Count != 3 || minimum != 4 || maximum != 6 {
					t.Fatal("computed aggregate domains", rows[1])
				}
				page, err := grouped.Page(t.Context(), 1, 1)
				check(t, err)
				if page.Total != 2 || len(page.Rows) != 1 {
					t.Fatal("computed page total", page)
				}
				score := orm.Add(orm.F(f.Score), float64(.5))
				floatSum := orm.Sum(score).Distinct().Where(f.Amount.GreaterThan(0))
				floatAvg := orm.Avg(score).Where(f.Amount.GreaterThan(0))
				pair, err := project.AggregateRecordsMeasureInto(t.Context(), api.RecordsMeasure, orm.Aggregate2(floatSum, floatAvg, func(a, b orm.Optional[float64]) [2]orm.Optional[float64] { return [2]orm.Optional[float64]{a, b} }))
				check(t, err)
				for _, v := range pair {
					n, ok := v.Get()
					if !ok || n != 2 {
						t.Fatal("float operand/filter parameter duplication", pair)
					}
				}
			})
		})
		t.Run("slice_distinct_empty_and_arguments", func(t *testing.T) {
			within(t, func(session db.Session) {
				source := records.MeasureObjects.Using(session).OrderBy(f.ID.Asc())
				value := orm.Add(orm.F(f.Amount), int64(1))
				limited, err := source.Filter(f.Amount.GreaterThan(-9)).Limit(3)
				check(t, err)
				limited, err = limited.Offset(1)
				check(t, err)
				sum, err := orm.AggregateInto(t.Context(), limited, orm.Aggregate1(orm.Sum(value), func(v orm.Optional[int64]) orm.Optional[int64] { return v }))
				check(t, err)
				n, present := sum.Get()
				if !present || n != 4 {
					t.Fatal("slice aggregate changed parameter order or row membership", sum)
				}
				values, err := orm.SelectInto(t.Context(), source.OrderBy(value.Asc()).Distinct(), orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { return v }))
				check(t, err)
				if len(values) != 4 {
					t.Fatal("computed DISTINCT cardinality", values)
				}
				if rows, err := orm.SelectInto(t.Context(), source.Distinct(), orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { return v })); err == nil || rows != nil {
					t.Fatal("unselected DISTINCT ordering accepted")
				}
				full, err := source.OrderBy(value.Asc()).Distinct().All(t.Context())
				check(t, err)
				if len(full) != 5 || full[0].Code != "b" {
					t.Fatal("hidden computed ordering changed model shape")
				}
				empty := source.Filter(f.ID.In())
				zero, err := orm.AggregateInto(t.Context(), empty, orm.Aggregate2(orm.Sum(value), orm.Count(value), func(s orm.Optional[int64], n int64) struct {
					Sum   orm.Optional[int64]
					Count int64
				} {
					return struct {
						Sum   orm.Optional[int64]
						Count int64
					}{s, n}
				}))
				check(t, err)
				if zero.Sum.Valid() || zero.Count != 0 {
					t.Fatal("folded empty lost aggregate result shape", zero)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if rows, err := orm.SelectInto(ctx, empty, orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { return v })); !errors.Is(err, context.Canceled) || rows != nil {
					t.Fatal("empty result ignored cancellation", err)
				}
			})
		})
		t.Run("source_cache_and_lifetime", func(t *testing.T) {
			var escaped orm.QuerySet[records.Measure]
			value := orm.Add(orm.F(f.Amount), int64(1))
			within(t, func(session db.Session) {
				raw := records.MeasureObjects.Using(session).OrderBy(f.ID.Asc())
				before, err := raw.All(t.Context())
				check(t, err)
				failing := orm.Case(orm.F(f.Amount), orm.When(f.Code.Exact("a"), orm.Add(orm.F(f.Amount), int64(10))), orm.When(f.Code.Exact("b"), orm.NullValue[records.Measure, int64]()))
				if count, err := raw.Update(t.Context(), orm.AssignExpression(f.Amount, failing)); err == nil || count != 0 {
					t.Fatal("conditional update published a partial count", count, err)
				}
				unchanged, err := raw.Fresh().All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(before, unchanged) {
					t.Fatal("conditional update failed to restore every row")
				}

				_, err = records.MeasureObjects.Using(session).Filter(f.Code.Exact("a")).Update(t.Context(), orm.Assign(f.Amount, int64(100)))
				check(t, err)
				projected, err := orm.SelectInto(t.Context(), raw, orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { return v }))
				check(t, err)
				n, _ := projected[0].Get()
				if n != 101 {
					t.Fatal("projection borrowed stale source cache")
				}
				warm, err := raw.All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(before, warm) {
					t.Fatal("projection replaced source cache")
				}
				for _, fault := range []string{"scan", "close"} {
					probe := &computedReadProbe{Session: session, fault: fault}
					built := 0
					rows, err := orm.SelectInto(t.Context(), records.MeasureObjects.Using(probe), orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { built++; return v }))
					if err == nil || rows != nil || probe.closes != 1 || fault == "scan" && built != 0 {
						t.Fatal("computed failure published rows or leaked owner", fault, err, probe.closes, built)
					}
				}
				escaped = raw
			})
			if rows, err := orm.SelectInto(t.Context(), escaped, orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { return v })); err == nil || rows != nil {
				t.Fatal("escaped computed source remained usable")
			}
		})
		t.Run("native_failures_and_precision", func(t *testing.T) {
			seedComputed(t, backend, fixture.Input)
			rows, err := records.MeasureObjects.Using(backend).OrderBy(f.ID.Asc()).All(t.Context())
			check(t, err)
			t.Cleanup(func() {
				check(t, physical("price", "NULL", rows[0].ID))
				for _, row := range rows {
					_, err := records.MeasureObjects.Delete(context.Background(), backend, &row)
					check(t, err)
				}
			})
			_, err = records.MeasureObjects.Using(backend).Filter(f.ID.Exact(rows[0].ID)).Update(t.Context(), orm.Assign(f.Amount, int64(math.MaxInt64)))
			check(t, err)
			value := orm.Add(orm.F(f.Amount), int64(1))
			built := 0
			result, err := orm.SelectInto(t.Context(), records.MeasureObjects.Using(backend).Filter(f.ID.Exact(rows[0].ID)), orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { built++; return v }))
			if err == nil || result != nil || built != 0 {
				t.Fatal("native int64 overflow became success", result, err)
			}
			zero, _ := decimal.Parse("0")
			copyPrice := orm.Case(orm.F(f.Price), orm.When(f.Price.IsNull(true), orm.Value[records.Measure](zero)))
			raw := "'not-a-decimal'"
			if vendor == "postgres" {
				raw = "'NaN'::numeric"
			}
			check(t, physical("price", raw, rows[0].ID))
			if result, err := orm.SelectInto(t.Context(), records.MeasureObjects.Using(backend).Filter(f.ID.Exact(rows[0].ID)), orm.Project1(copyPrice, func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v })); err == nil || result != nil {
				t.Fatal("computed source hid invalid physical Decimal")
			}
			selected := orm.Case(orm.Value[records.Measure](zero), orm.When(f.Code.Exact("never"), orm.F(f.Price)))
			resultDecimal, err := orm.SelectInto(t.Context(), records.MeasureObjects.Using(backend).Filter(f.ID.Exact(rows[0].ID)), orm.Project1(selected, func(v orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return v }))
			check(t, err)
			if len(resultDecimal) != 1 || !resultDecimal[0].Valid() {
				t.Fatal("unselected Decimal branch was evaluated")
			}
		})
		t.Run("dynamic_rejection_and_ownership", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &computedReadProbe{Session: session}
				source := records.MeasureObjects.Using(probe)
				for _, input := range []orm.DynamicExpression{orm.DynamicF("unknown"), orm.DynamicValue(nil), orm.DynamicValue([]byte("mutable")), orm.DynamicF("amount").Add("bad"), orm.DynamicCase(orm.DynamicF("amount"), orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), "text"))} {
					if _, err := orm.BindExpression(source, input); err == nil {
						t.Fatal("invalid dynamic scalar bound")
					}
				}
				field := orm.NewNullableIntegerField[records.Measure](ir.Field{Name: "amount", GoName: "Amount", Column: "amount", Kind: ir.FieldInteger, Nullable: true})
				value := orm.Add(orm.F(field), int64(1))
				if result, err := orm.SelectInto(t.Context(), source, orm.Project1(value, func(v orm.Optional[int64]) orm.Optional[int64] { return v })); err == nil || result != nil {
					t.Fatal("forged source nullability accepted")
				}
				if len(probe.plans) != 0 {
					t.Fatal("invalid computed declaration performed I/O")
				}
				branches := []orm.DynamicWhenExpression{orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), int64(8))}
				input := orm.DynamicCase(int64(4), branches...)
				branches[0] = orm.DynamicWhen(orm.DynamicF("enabled").Exact(true), int64(99))
				bound, err := orm.BindExpression(source, input)
				check(t, err)
				selected, err := orm.SelectInto(t.Context(), source.Filter(f.Code.Exact("a")), orm.Project1(bound, func(v query.Value) query.Value { return v }))
				check(t, err)
				n, _ := selected[0].Integer()
				if n != 8 {
					t.Fatal("dynamic branch container aliases caller")
				}
				if result, err := orm.SelectInto(t.Context(), source.Filter(bound.Exact("wrong")), orm.Project1(bound, func(v query.Value) query.Value { return v })); err == nil || result != nil {
					t.Fatal("dynamic comparison ignored result kind")
				}
			})
		})
		t.Run("literal_codecs_and_typed_nulls", func(t *testing.T) {
			within(t, func(session db.Session) {
				_, err := records.OtherObjects.Create(t.Context(), session, records.NewOtherCreate(1, true))
				check(t, err)
				checkComputedCodecs(t, records.OtherObjects.Using(session))
			})
			count, err := records.OtherObjects.Using(backend).Count(t.Context())
			check(t, err)
			if count != 0 {
				t.Fatal("literal codec fixture escaped its rollback")
			}
		})
	})
}
