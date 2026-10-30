package consumer

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type namedReadProbe struct {
	db.Session
	mu     sync.Mutex
	plans  []query.Plan
	closes atomic.Int64
	fault  string
}

func (p *namedReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.mu.Lock()
	p.plans = append(p.plans, plan)
	p.mu.Unlock()
	rows, err := p.Session.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &namedRows{Rows: rows, owner: p}, nil
}
func (p *namedReadProbe) calls() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.plans) }
func (p *namedReadProbe) ValidateSession(ctx context.Context) error {
	if session, ok := p.Session.(db.SessionValidator); ok {
		return session.ValidateSession(ctx)
	}
	return ctx.Err()
}

type namedRows struct {
	db.Rows
	owner *namedReadProbe
}

func (r *namedRows) Scan(values ...any) error {
	if r.owner.fault == "scan" {
		return errors.New("named scan failure")
	}
	return r.Rows.Scan(values...)
}
func (r *namedRows) Err() error {
	if r.owner.fault == "iteration" {
		return errors.Join(r.Rows.Err(), errors.New("named iteration failure"))
	}
	return r.Rows.Err()
}
func (r *namedRows) Close() error {
	r.owner.closes.Add(1)
	err := r.Rows.Close()
	if r.owner.fault == "close" {
		return errors.Join(err, errors.New("named close failure"))
	}
	return err
}

func namedOptional[V any](value orm.Optional[V], encode func(V) query.Value) query.Value {
	if n, ok := value.Get(); ok {
		return encode(n)
	}
	return query.Null()
}

func seedNamed(t *testing.T, session db.Session) {
	t.Helper()
	price, err := decimal.Parse("1.23")
	check(t, err)
	small, err := decimal.Parse("0.03")
	check(t, err)
	for _, input := range []records.MeasureCreate{
		records.NewMeasureCreate("a", true, 5).WithGroupKey("x").WithOther(2).WithPrice(price).WithElapsed(duration.FromMicroseconds(3)),
		records.NewMeasureCreate("b", false, -7).WithGroupKey("x").WithPrice(small).WithElapsed(duration.FromMicroseconds(4)),
		records.NewMeasureCreate("c", true, 3).WithGroupKey("y").WithOther(0),
		records.NewMeasureCreate("e", false, 0).WithGroupKey("y").WithOther(-2).WithElapsed(duration.FromMicroseconds(0)),
	} {
		_, err := records.MeasureObjects.Create(t.Context(), session, input)
		check(t, err)
	}
}

func TestNamedScalarRuntime(t *testing.T) {
	withComputedBackends(t, func(t *testing.T, backend computedBackend, _ string, _ computedPhysicalWriter) {
		f := records.MeasureFields
		within := func(t *testing.T, run func(db.Session)) {
			t.Helper()
			rollback := errors.New("named consumer rollback")
			err := backend.Atomic(t.Context(), func(session db.Session) error { seedNamed(t, session); run(session); return rollback })
			if !errors.Is(err, rollback) {
				t.Fatal("named rollback failed", err)
			}
			count, err := records.MeasureObjects.Using(backend).Count(t.Context())
			check(t, err)
			if count != 0 {
				t.Fatal("named consumer changed retained storage", count)
			}
		}
		t.Run("model_rows_and_typed_dynamic_projection", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &namedReadProbe{Session: session}
				api, err := project.UsingSession(probe)
				check(t, err)
				named, adjusted, err := project.AnnotateRecordsMeasure(api.RecordsMeasure.OrderBy(f.ID.Asc()), "adjusted", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				named, doubled, err := project.AnnotateRecordsMeasure(named, "doubled", orm.Multiply(orm.OptionalOperand(adjusted), int64(2)))
				check(t, err)
				rows, err := named.All(t.Context())
				check(t, err)
				if len(rows) != 4 || probe.calls() != 1 {
					t.Fatal("named model materialization", len(rows), probe.calls())
				}
				for i, want := range []int64{6, -6, 4, 1} {
					n, err := adjusted.Get(rows[i])
					check(t, err)
					if got, ok := n.Get(); !ok || got != want || rows[i].Value.Amount != want-1 {
						t.Fatal("named model arithmetic", i, n, rows[i].Value.Amount)
					}
					n, err = doubled.Get(rows[i])
					check(t, err)
					if got, ok := n.Get(); !ok || got != want*2 {
						t.Fatal("named dependency", i, n)
					}
					if _, err := rows[i].Value.Unwrap(); err != nil {
						t.Fatal("ordinary generated model wrapper is unusable", err)
					}
				}
				typed, err := project.SelectRecordsMeasureNamedQuery(named, orm.Project2(orm.F(f.Code), adjusted, func(code orm.Optional[string], n orm.Optional[int64]) []query.Value {
					if !code.Valid() {
						t.Fatal("required code expression is NULL")
					}
					return []query.Value{namedOptional(code, query.String), namedOptional(n, query.Integer)}
				}))
				check(t, err)
				projection, err := named.ProjectExpressions(orm.DynamicF("code"), orm.DynamicF("adjusted"))
				check(t, err)
				dynamic, err := project.SelectRecordsMeasureNamedQuery(named, projection)
				check(t, err)
				if !typed.Plan().Equal(dynamic.Plan()) {
					t.Fatal("typed/dynamic named AST differs")
				}
				a, err := typed.All(t.Context())
				check(t, err)
				b, err := dynamic.All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(a, b) {
					t.Fatal("named projection differs", a, b)
				}
				page, err := named.OrderBy(adjusted.Desc()).Page(t.Context(), 2, 1)
				check(t, err)
				if page.Total != 4 || len(page.Rows) != 2 || page.Rows[0].Value.Code != "c" || page.Rows[1].Value.Code != "e" {
					t.Fatal("named page", page)
				}
				limited, err := named.Limit(2)
				check(t, err)
				limited, err = limited.Offset(1)
				check(t, err)
				count, err := limited.Count(t.Context())
				check(t, err)
				if count != 2 {
					t.Fatal("named sliced count", count)
				}
				empty, err := named.Limit(0)
				check(t, err)
				exists, err := empty.Exists(t.Context())
				check(t, err)
				if exists {
					t.Fatal("LIMIT 0 exists")
				}
			})
		})
		t.Run("rebind_alias_and_projection_ownership", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				first, old, err := project.AnnotateRecordsMeasure(api.RecordsMeasure.OrderBy(f.ID.Asc()), "first", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				named, err := first.Annotate(orm.Named("dependent", orm.DynamicF("first").Multiply(2)), orm.Named("first", orm.DynamicValue(100)))
				check(t, err)
				rows, err := named.All(t.Context())
				check(t, err)
				latest, err := named.Column("first")
				check(t, err)
				dependent, err := named.Column("dependent")
				check(t, err)
				for i, want := range []int64{12, -12, 8, 2} {
					a, err := latest.Get(rows[i])
					check(t, err)
					b, err := dependent.Get(rows[i])
					check(t, err)
					if n, _ := a.Integer(); n != 100 {
						t.Fatal(a)
					}
					if n, _ := b.Integer(); n != want {
						t.Fatal(b)
					}
					if _, err := old.Get(rows[i]); err == nil {
						t.Fatal("old unselected reference read latest cell")
					}
				}
				oldRows, err := first.All(t.Context())
				check(t, err)
				if n, err := old.Get(oldRows[0]); err != nil {
					t.Fatal(err)
				} else if got, _ := n.Get(); got != 6 {
					t.Fatal("rebind changed input query", got)
				}
				hidden, err := named.Alias(orm.Named("hidden", orm.DynamicF("amount").Add(4)))
				check(t, err)
				if _, err := hidden.ProjectExpressions(orm.DynamicF("hidden")); err == nil {
					t.Fatal("alias direct selection")
				}
				promoted, err := hidden.Annotate(orm.Named("hidden", orm.DynamicF("hidden")))
				check(t, err)
				projection, err := promoted.ProjectExpressions(orm.DynamicF("hidden"))
				check(t, err)
				selected, err := project.SelectRecordsMeasureNamedQuery(promoted, projection)
				check(t, err)
				reused, err := orm.Names(selected)
				check(t, err)
				if len(reused.Plan().ResultShape().Expressions()) != 1 {
					t.Fatal("selected column duplicated")
				}
				selectedRows, err := reused.All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(selectedRows[0].Names(), []string{"hidden"}) {
					t.Fatal("projection re-published unselected names", selectedRows[0].Names())
				}
			})
		})
		t.Run("grouping_page_and_explicit_stage", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				base, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure, orm.Project1(f.GroupKey, func(key *string) *string { return key }))
				check(t, err)
				named, total, err := orm.AnnotateAggregate(base, "total", orm.Sum(f.Amount))
				check(t, err)
				named, twice, err := orm.Annotate(named, "twice", orm.Multiply(orm.OptionalOperand(total), int64(2)))
				check(t, err)
				selected, err := orm.SelectNamed(named, orm.Project1(twice, func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
				check(t, err)
				values, err := selected.OrderBy(twice.Asc()).All(t.Context())
				check(t, err)
				if len(values) != 2 {
					t.Fatal("hidden grouping key lost", values)
				}
				for i, want := range []int64{-4, 6} {
					if n, ok := values[i].Get(); !ok || n != want {
						t.Fatal(values)
					}
				}
				page, err := named.Filter(total.GreaterThan(orm.Some(int64(0)))).Page(t.Context(), 1, 0)
				check(t, err)
				if page.Total != 1 || len(page.Rows) != 1 || *page.Rows[0].Value != "y" {
					t.Fatal("named aggregate page", page)
				}
				limited, err := selected.OrderBy(twice.Desc()).Limit(1)
				check(t, err)
				stage, err := orm.Derive(limited)
				check(t, err)
				column := orm.SelectedField(stage, twice)
				reduced, err := orm.AggregateResults(stage, orm.Aggregate1(orm.Sum(orm.Numeric(orm.OptionalOperand(column))), func(value orm.Optional[int64]) orm.Optional[int64] { return value }))
				check(t, err)
				result, err := reduced.One(t.Context())
				check(t, err)
				if n, ok := result.Get(); !ok || n != 6 {
					t.Fatal("explicit named stage lost slice", result)
				}
			})
		})
		t.Run("projection_controls_first_aggregate", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				rowNames, _, err := project.AnnotateRecordsMeasure(api.RecordsMeasure, "varying", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				groupKeys, err := project.SelectRecordsMeasureNamedQuery(rowNames, orm.Project1(f.GroupKey, func(key *string) *string { return key }))
				check(t, err)
				grouped, total, err := orm.AnnotateAggregate(groupKeys, "total", orm.Sum(f.Amount))
				check(t, err)
				rows, err := grouped.OrderBy(f.GroupKey.Asc()).All(t.Context())
				check(t, err)
				if len(rows) != 2 {
					t.Fatal("unselected annotation split the first groups", len(rows))
				}
				for i, want := range []int64{-2, 3} {
					value, err := total.Get(rows[i])
					check(t, err)
					if n, ok := value.Get(); !ok || n != want || *rows[i].Value != []string{"x", "y"}[i] {
						t.Fatal("projection grouping differs", rows, value)
					}
				}

				base, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure, orm.Project1(f.GroupKey, func(key *string) *string { return key }))
				check(t, err)
				visible, varying, err := orm.Annotate(base, "varying", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				retained, sum, err := orm.AnnotateAggregate(visible, "total", orm.Sum(f.Amount))
				check(t, err)
				retainedRows, err := retained.OrderBy(f.GroupKey.Asc(), varying.Asc()).All(t.Context())
				check(t, err)
				if len(retainedRows) != 4 {
					t.Fatal("selected annotation was omitted from the first groups", len(retainedRows))
				}
				for i, want := range []int64{-7, 5, 0, 3} {
					value, err := sum.Get(retainedRows[i])
					check(t, err)
					key, err := varying.Get(retainedRows[i])
					check(t, err)
					n, valid := value.Get()
					k, present := key.Get()
					if !valid || !present || n != want || k != want+1 {
						t.Fatal("selected grouping key changed", value, key)
					}
				}
				replaced, count, err := orm.AnnotateAggregate(visible, "varying", orm.CountRows[records.Measure]())
				check(t, err)
				replacedRows, err := replaced.OrderBy(f.GroupKey.Asc()).All(t.Context())
				check(t, err)
				if len(replacedRows) != 2 {
					t.Fatal("replaced scalar still split the first groups", len(replacedRows))
				}
				for _, row := range replacedRows {
					value, err := count.Get(row)
					check(t, err)
					if value != 2 {
						t.Fatal("rebound aggregate changed membership", value)
					}
				}
				explicit, err := orm.SelectNamed(visible, orm.Project2(f.GroupKey, varying, func(key *string, n orm.Optional[int64]) *string { return key }))
				check(t, err)
				explicitGroup, explicitCount, err := orm.AnnotateAggregate(explicit, "varying", orm.CountRows[records.Measure]())
				check(t, err)
				explicitRows, err := explicitGroup.All(t.Context())
				check(t, err)
				if len(explicitRows) != 4 {
					t.Fatal("rebind removed an explicitly selected older key", len(explicitRows))
				}
				for _, row := range explicitRows {
					value, err := explicitCount.Get(row)
					check(t, err)
					if value != 1 {
						t.Fatal("explicit older key lost its group", value)
					}
				}
				batch, err := orm.AnnotateDynamic(base, orm.Named("varying", orm.DynamicF("amount").Add(1)), orm.NamedCount("counted"))
				check(t, err)
				batchRows, err := batch.All(t.Context())
				check(t, err)
				if len(batchRows) != 4 {
					t.Fatal("ordered batch omitted its selected scalar grouping key", len(batchRows))
				}
			})
		})
		t.Run("nullable_decimal_and_duration_codecs", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				named, price, err := project.AnnotateRecordsMeasure(api.RecordsMeasure.OrderBy(f.ID.Asc()), "saved_price", f.Price)
				check(t, err)
				named, span, err := project.AnnotateRecordsMeasure(named, "saved_span", f.Elapsed)
				check(t, err)
				named, other, err := project.AnnotateRecordsMeasure(named, "adjusted_other", orm.Add(orm.F(f.Other), int64(1)))
				check(t, err)
				rows, err := named.All(t.Context())
				check(t, err)
				for i, row := range rows {
					p, err := price.Get(row)
					check(t, err)
					s, err := span.Get(row)
					check(t, err)
					n, err := other.Get(row)
					check(t, err)
					if !reflect.DeepEqual(p, row.Value.Price) || !reflect.DeepEqual(s, row.Value.Elapsed) {
						t.Fatal("named nullable domain differs", i, p, s)
					}
					if row.Value.Other == nil {
						if n.Valid() {
							t.Fatal("NULL became a value")
						}
					} else if value, ok := n.Get(); !ok || value != *row.Value.Other+1 {
						t.Fatal("nullable numeric decoder", n)
					}
				}
				if rows[0].Value.Price != nil {
					p, err := price.Get(rows[0])
					check(t, err)
					*p = decimal.Decimal{}
					again, err := price.Get(rows[0])
					check(t, err)
					if again.String() != "1.23" {
						t.Fatal("nullable decimal storage escaped", again)
					}
				}
				projection, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure, orm.Project1(f.GroupKey, func(s *string) *string { return s }))
				check(t, err)
				aggregated, average, err := orm.AnnotateAggregate(projection, "average", orm.Avg(f.Elapsed))
				check(t, err)
				values, err := aggregated.OrderBy(f.GroupKey.Asc()).All(t.Context())
				check(t, err)
				avg, err := average.Get(values[0])
				check(t, err)
				if value, ok := avg.Get(); !ok {
					t.Fatal("named duration mean is NULL")
				} else if micros, err := value.TotalMicroseconds(); err != nil || micros != 4 {
					t.Fatal("native duration mean decoder", value, err)
				}
			})
		})
		t.Run("cache_failure_and_session_lifetime", func(t *testing.T) {
			var borrowed project.RecordsMeasureNamedQuery
			within(t, func(session db.Session) {
				probe := &namedReadProbe{Session: session}
				api, err := project.UsingSession(probe)
				check(t, err)
				borrowed, _, err = project.AnnotateRecordsMeasure(api.RecordsMeasure.OrderBy(f.ID.Asc()), "plus", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				rows, err := borrowed.All(t.Context())
				check(t, err)
				*rows[0].Value.GroupKey = "caller"
				cached, err := borrowed.All(t.Context())
				check(t, err)
				if *cached[0].Value.GroupKey != "x" || probe.calls() != 1 {
					t.Fatal("model cache mutation or duplicate evaluation")
				}
				for _, fault := range []string{"scan", "iteration", "close"} {
					probe.fault = fault
					fresh := borrowed.Fresh()
					before := probe.closes.Load()
					if rows, err := fresh.All(t.Context()); err == nil || rows != nil {
						t.Fatal("partial named publication", fault, rows, err)
					}
					if probe.closes.Load() != before+1 {
						t.Fatal("failed named cursor not closed once")
					}
					probe.fault = ""
					rows, err := fresh.All(t.Context())
					check(t, err)
					if len(rows) != 4 {
						t.Fatal("named retry", rows)
					}
				}
			})
			if _, err := borrowed.All(t.Context()); err == nil {
				t.Fatal("cached named facade escaped transaction")
			}
			if _, err := borrowed.Exists(t.Context()); err == nil {
				t.Fatal("named existence escaped transaction")
			}
		})
		t.Run("concurrent_copies_and_pre_io_errors", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &namedReadProbe{Session: session}
				api, err := project.UsingSession(probe)
				check(t, err)
				named, column, err := project.AnnotateRecordsMeasure(api.RecordsMeasure.OrderBy(f.ID.Asc()), "plus", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				_, foreign, err := project.AnnotateRecordsMeasure(api.RecordsMeasure, "plus", orm.Add(orm.F(f.Amount), int64(1)))
				check(t, err)
				if _, err := named.Filter(foreign.IsNull(false)).All(t.Context()); err == nil {
					t.Fatal("foreign branch reference accepted")
				}
				if _, _, err := project.AnnotateRecordsMeasure(named, "bad", orm.Case(orm.Value[records.Measure](int64(0)), orm.When(orm.GreaterThanExpressions(orm.OptionalOperand(column), orm.OptionalOperand(foreign)), orm.Value[records.Measure](int64(1))))); err == nil {
					t.Fatal("foreign CASE operand accepted")
				}
				if _, err := named.Annotate(orm.Named("future", orm.DynamicF("missing")), orm.Named("missing", orm.DynamicValue(1))); err == nil {
					t.Fatal("forward reference accepted")
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := named.All(ctx); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if probe.calls() != 0 {
					t.Fatal("invalid named input reached I/O", probe.calls())
				}
				var wait sync.WaitGroup
				failures := make(chan error, 8)
				for range 8 {
					wait.Add(1)
					go func() {
						defer wait.Done()
						rows, err := named.All(t.Context())
						if err != nil {
							failures <- err
							return
						}
						if len(rows) != 4 {
							failures <- errors.New("incomplete concurrent named rows")
							return
						}
						*rows[0].Value.GroupKey = "private"
					}()
				}
				wait.Wait()
				close(failures)
				for err := range failures {
					t.Fatal(err)
				}
				if probe.calls() != 1 {
					t.Fatal("concurrent named copies started multiple evaluations", probe.calls())
				}
			})
		})
	})
}
