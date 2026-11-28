package consumer

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type derivedReadProbe struct {
	db.Session
	mu     sync.Mutex
	plans  []query.Plan
	closes atomic.Int64
	fault  string
}

func (p *derivedReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.mu.Lock()
	p.plans = append(p.plans, plan)
	p.mu.Unlock()
	rows, err := p.Session.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &derivedRows{Rows: rows, owner: p}, nil
}
func (p *derivedReadProbe) calls() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.plans) }
func (p *derivedReadProbe) ValidateSession(ctx context.Context) error {
	if session, ok := p.Session.(db.SessionValidator); ok {
		return session.ValidateSession(ctx)
	}
	return ctx.Err()
}

type derivedRows struct {
	db.Rows
	owner *derivedReadProbe
}

func (r *derivedRows) Scan(values ...any) error {
	if r.owner.fault == "scan" {
		return errors.New("derived scan failure")
	}
	return r.Rows.Scan(values...)
}
func (r *derivedRows) Err() error {
	if r.owner.fault == "iteration" {
		return errors.Join(r.Rows.Err(), errors.New("derived iteration failure"))
	}
	return r.Rows.Err()
}
func (r *derivedRows) Close() error {
	r.owner.closes.Add(1)
	err := r.Rows.Close()
	if r.owner.fault == "close" {
		return errors.Join(err, errors.New("derived close failure"))
	}
	return err
}

func derivedOptional[T any](value *T, encode func(T) query.Value) query.Value {
	if value == nil {
		return query.Null()
	}
	return encode(*value)
}

func TestDerivedRuntime(t *testing.T) {
	withComputedBackends(t, func(t *testing.T, backend computedBackend, vendor string, _ computedPhysicalWriter) {
		fixture := readDerivedReference(t, vendor)
		f := records.MeasureFields
		within := func(t *testing.T, run func(db.Session)) {
			rollback := errors.New("derived runtime owned rollback")
			err := backend.Atomic(t.Context(), func(session db.Session) error { seedComputed(t, session, fixture.Input); run(session); return rollback })
			if !errors.Is(err, rollback) {
				t.Fatal("derived runtime failed rollback", err)
			}
			count, err := records.MeasureObjects.Using(backend).Count(t.Context())
			check(t, err)
			if count != 0 {
				t.Fatal("derived runtime leaked rows")
			}
		}
		t.Run("computed_group_ratio_and_page", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &derivedReadProbe{Session: session}
				api, err := project.UsingSession(probe)
				check(t, err)
				opened := orm.Count(f.ID).Where(f.Enabled.Exact(true))
				count := orm.CountRows[records.Measure]()
				grouped, err := project.GroupRecordsMeasureBy(api.RecordsMeasure, orm.Project1(f.GroupKey, func(key *string) *string { return key }),
					orm.Aggregate2(count, opened, func(total, open int64) [2]int64 { return [2]int64{total, open} }), func(_ *string, counts [2]int64) [2]int64 { return counts })
				check(t, err)
				stage, err := orm.Derive(grouped)
				check(t, err)
				key, total, open := orm.SelectedField(stage, f.GroupKey), orm.SelectedAggregate(stage, count), orm.SelectedAggregate(stage, opened)
				closed := orm.SubtractExpressions(total, open)
				ratio := orm.DivideExpressions(orm.Multiply(open, int64(10000)), total)
				safe := orm.Case(ratio, orm.When(total.Exact(0), orm.Value[orm.ResultOf[records.Measure]](int64(0))))
				projection := orm.CombineProjections(orm.Project3(key, total, open, func(key *string, total, open int64) []query.Value {
					return []query.Value{nullableString(key), query.Integer(total), query.Integer(open)}
				}),
					orm.Project2(closed, safe, func(closed, ratio orm.Optional[int64]) []query.Value {
						return []query.Value{optionalInteger(closed), optionalInteger(ratio)}
					}),
					func(a, b []query.Value) []query.Value { return append(a, b...) })
				typed, err := orm.SelectResults(stage, projection)
				check(t, err)
				typed = typed.Filter(safe.GreaterThanOrEqual(1)).OrderBy(safe.Desc(), key.Asc().NullsLast())
				dynamicRatio := orm.DynamicCase(orm.DynamicColumn(2).Multiply(10000).Divide(orm.DynamicColumn(1)), orm.DynamicWhen(orm.DynamicColumn(1).Exact(0), 0))
				dynamicProjection, err := stage.Project(orm.DynamicColumn(0), orm.DynamicColumn(1), orm.DynamicColumn(2), orm.DynamicColumn(1).Subtract(orm.DynamicColumn(2)), dynamicRatio)
				check(t, err)
				dynamic, err := orm.SelectResults(stage, dynamicProjection)
				check(t, err)
				predicate, err := stage.Predicate(dynamicRatio.GreaterThanOrEqual(1))
				check(t, err)
				order, err := stage.Expression(dynamicRatio)
				check(t, err)
				dynamic = dynamic.Filter(predicate).OrderBy(order.Desc(), key.Asc().NullsLast())
				if !typed.Plan().Equal(dynamic.Plan()) {
					t.Fatal("typed/dynamic ratio AST differs")
				}
				page, err := typed.Page(t.Context(), 1, 1)
				check(t, err)
				if page.Total != 2 || len(page.Rows) != 1 || !reflect.DeepEqual(page.Rows[0], []query.Value{query.String("x"), query.Integer(3), query.Integer(2), query.Integer(1), query.Integer(6666)}) || probe.calls() != 1 {
					t.Fatal("single-statement ratio page", page, probe.calls())
				}
				zero, err := typed.Page(t.Context(), 0, 0)
				check(t, err)
				if zero.Total != 2 || len(zero.Rows) != 0 || zero.Rows == nil {
					t.Fatal("zero page lost count", zero)
				}
				left, err := typed.All(t.Context())
				check(t, err)
				right, err := dynamic.All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(left, right) || len(left) != 2 || probe.calls() != 4 {
					t.Fatal("ratio result differs or page warmed cache", left, right, probe.calls())
				}
			})
		})
		t.Run("stage_order_and_reaggregation", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				input, err := project.SelectRecordsMeasureQuery(api.RecordsMeasure.OrderBy(f.Code.Desc()), orm.Project2(f.Code, f.Amount, func(code string, amount int64) string { t.Fatal("input DTO builder ran"); return code }))
				check(t, err)
				input, err = input.Limit(2)
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				amount := orm.SelectedField(stage, f.Amount)
				result, err := orm.SelectResults(stage, orm.Project1(amount, func(n int64) int64 { return n }))
				check(t, err)
				result = result.Filter(amount.GreaterThan(0))
				values, err := result.All(t.Context())
				check(t, err)
				if !reflect.DeepEqual(values, []int64{5}) {
					t.Fatal("filter moved before sliced input", values)
				}
				next, err := orm.Derive(result)
				check(t, err)
				selected := orm.SelectedField(next, amount)
				aggregate, err := orm.AggregateResults(next, orm.Aggregate2(orm.Sum(orm.Numeric(selected)), orm.CountRows[orm.ResultOf[orm.ResultOf[records.Measure]]](), func(sum orm.Optional[int64], count int64) [2]int64 {
					value, _ := sum.Get()
					return [2]int64{value, count}
				}))
				check(t, err)
				value, err := aggregate.One(t.Context())
				check(t, err)
				if value != [2]int64{5, 1} {
					t.Fatal("terminal aggregate changed stage order", value)
				}
			})
		})
		t.Run("duration_mean_keeps_native_comparison", func(t *testing.T) {
			within(t, func(session db.Session) {
				api, err := project.UsingSession(session)
				check(t, err)
				mean := orm.Avg(f.Elapsed)
				input, err := project.AggregateRecordsMeasureQuery(api.RecordsMeasure.Filter(orm.Or(f.Code.Exact("a"), f.Code.Exact("b"))),
					orm.Aggregate1(mean, func(value orm.Optional[duration.Duration]) orm.Optional[duration.Duration] { return value }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				column := orm.SelectedAggregate(stage, mean)
				value := orm.Case(orm.OptionalOperand(column), orm.When(column.IsNull(true), orm.Value[orm.ResultOf[records.Measure]](duration.Duration{})))
				selected, err := orm.SelectResults(stage, orm.Project1(value, func(value orm.Optional[duration.Duration]) orm.Optional[duration.Duration] { return value }))
				check(t, err)
				rows, err := selected.All(t.Context())
				check(t, err)
				if len(rows) != 1 {
					t.Fatal(rows)
				}
				if mean, present := rows[0].Get(); !present || mean != duration.FromMicroseconds(4) {
					t.Fatal("duration AVG rounded before or after wrong boundary", rows)
				}
				filtered, err := selected.Filter(value.GreaterThanOrEqual(duration.FromMicroseconds(4))).All(t.Context())
				check(t, err)
				want := 0
				if vendor == "postgres" {
					want = 1
				}
				if len(filtered) != want {
					t.Fatal("duration mean lost native WHERE behavior", filtered, vendor)
				}
				next, err := orm.Derive(selected)
				check(t, err)
				projection, err := next.Project(orm.DynamicColumn(0))
				check(t, err)
				dynamic, err := orm.SelectResults(next, projection)
				check(t, err)
				actual, err := dynamic.All(t.Context())
				check(t, err)
				if len(actual) != 1 || !actual[0][0].Equal(query.Duration(duration.FromMicroseconds(4))) {
					t.Fatal("dynamic result lost native duration mean", actual)
				}
				bound, err := next.Expression(orm.DynamicColumn(0))
				check(t, err)
				// Computed Duration SUM now retains its input's native mean
				// provenance. A second result stage must still compare SQLite's
				// fractional value before the final model decoder rounds it.
				summed, err := orm.AggregateResults(next, orm.Aggregate1(bound.Sum(), func(v query.Value) query.Value { return v }))
				check(t, err)
				sum, err := summed.One(t.Context())
				check(t, err)
				if !sum.Equal(query.Duration(duration.FromMicroseconds(4))) {
					t.Fatal("duration reaggregation lost its final model value", sum)
				}
				afterSum, err := orm.Derive(summed)
				check(t, err)
				boundSum, err := afterSum.Expression(orm.DynamicColumn(0))
				check(t, err)
				sumRows, err := orm.SelectResults(afterSum, orm.Project1(boundSum, func(v query.Value) query.Value { return v }))
				check(t, err)
				sumFiltered, err := sumRows.Filter(boundSum.GreaterThanOrEqual(duration.FromMicroseconds(4))).All(t.Context())
				check(t, err)
				if len(sumFiltered) != want {
					t.Fatal("duration SUM rounded a native mean before its subsequent predicate", sumFiltered, vendor)
				}
			})
		})
		t.Run("nullable_scalar_codecs", func(t *testing.T) {
			within(t, func(session db.Session) {
				raw := records.MeasureObjects.Using(session).OrderBy(f.Code.Asc())
				first := orm.Project4(f.Code, f.Other, f.Score, f.Price, func(code string, other *int64, score *float64, price *decimal.Decimal) []query.Value {
					return []query.Value{query.String(code), derivedOptional(other, query.Integer), derivedOptional(score, query.Float), derivedOptional(price, query.Decimal)}
				})
				second := orm.Project2(f.Elapsed, f.Enabled, func(elapsed *duration.Duration, enabled bool) []query.Value {
					return []query.Value{derivedOptional(elapsed, query.Duration), query.Boolean(enabled)}
				})
				input, err := orm.SelectQuery(raw, orm.CombineProjections(first, second, func(a, b []query.Value) []query.Value { return append(a, b...) }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				code, other, score, price, elapsed, enabled := orm.SelectedField(stage, f.Code), orm.SelectedField(stage, f.Other), orm.SelectedField(stage, f.Score), orm.SelectedField(stage, f.Price), orm.SelectedField(stage, f.Elapsed), orm.SelectedField(stage, f.Enabled)
				typedProjection := orm.CombineProjections(orm.Project4(code, other, score, price, func(code string, other *int64, score *float64, price *decimal.Decimal) []query.Value {
					return []query.Value{query.String(code), derivedOptional(other, query.Integer), derivedOptional(score, query.Float), derivedOptional(price, query.Decimal)}
				}), orm.Project2(elapsed, enabled, func(elapsed *duration.Duration, enabled bool) []query.Value {
					return []query.Value{derivedOptional(elapsed, query.Duration), query.Boolean(enabled)}
				}), func(a, b []query.Value) []query.Value { return append(a, b...) })
				typed, err := orm.SelectResults(stage, typedProjection)
				check(t, err)
				projection, err := stage.Project(orm.DynamicColumn(0), orm.DynamicColumn(1), orm.DynamicColumn(2), orm.DynamicColumn(3), orm.DynamicColumn(4), orm.DynamicColumn(5))
				check(t, err)
				dynamic, err := orm.SelectResults(stage, projection)
				check(t, err)
				typed, dynamic = typed.OrderBy(code.Asc()), dynamic.OrderBy(code.Asc())
				if !typed.Plan().Equal(dynamic.Plan()) {
					t.Fatal("codec projections have different AST")
				}
				left, err := typed.All(t.Context())
				check(t, err)
				right, err := dynamic.All(t.Context())
				check(t, err)
				original, err := input.All(t.Context())
				check(t, err)
				if len(left) != 5 || !reflect.DeepEqual(left, right) || !reflect.DeepEqual(left, original) {
					t.Fatal("derived result changed scalar codec or NULL", left, right, original)
				}
				condition := orm.Case(orm.NullableOperand(other), orm.When(other.IsNull(true), orm.Value[orm.ResultOf[records.Measure]](int64(9))))
				conditional, err := orm.SelectResults(stage, orm.Project1(condition, func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
				check(t, err)
				values, err := conditional.Filter(code.Exact("b")).All(t.Context())
				check(t, err)
				if len(values) != 1 {
					t.Fatal(values)
				}
				if n, valid := values[0].Get(); !valid || n != 9 {
					t.Fatal("nullable result operand did not preserve CASE", values)
				}
			})
		})
		t.Run("empty_and_zero_division", func(t *testing.T) {
			within(t, func(session db.Session) {
				input, err := orm.SelectQuery(records.MeasureObjects.Using(session).Filter(f.ID.LessThan(0)), orm.Project1(f.Amount, func(n int64) int64 { return n }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				value := orm.SelectedField(stage, f.Amount)
				aggregate, err := orm.AggregateResults(stage, orm.Aggregate2(orm.CountRows[orm.ResultOf[records.Measure]](), orm.Sum(orm.Numeric(value)), func(n int64, sum orm.Optional[int64]) []query.Value {
					return []query.Value{query.Integer(n), optionalInteger(sum)}
				}))
				check(t, err)
				row, err := aggregate.One(t.Context())
				check(t, err)
				if !reflect.DeepEqual(row, []query.Value{query.Integer(0), query.Null()}) {
					t.Fatal("empty derived aggregate lost COUNT/NULL", row)
				}
			})
			within(t, func(session db.Session) {
				// A statically empty producer must not evaluate unused native
				// division, and its COUNT row remains an input in later stages.
				raw, err := records.MeasureObjects.Using(session).Limit(0)
				check(t, err)
				unused := orm.Divide(orm.F(f.Amount), int64(0))
				input, err := orm.SelectQuery(raw, orm.Project1(unused, func(n orm.Optional[int64]) orm.Optional[int64] { t.Fatal("empty producer builder ran"); return n }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				count := orm.CountRows[orm.ResultOf[records.Measure]]()
				aggregate, err := orm.AggregateResults(stage, orm.Aggregate1(count, func(n int64) int64 { return n }))
				check(t, err)
				if n, err := aggregate.One(t.Context()); err != nil || n != 0 {
					t.Fatal("empty aggregate lost its zero row", n, err)
				}
				next, err := orm.Derive(aggregate)
				check(t, err)
				result, err := orm.SelectResults(next, orm.Project1(orm.SelectedAggregate(next, count), func(n int64) int64 { return n }))
				check(t, err)
				if values, err := result.All(t.Context()); err != nil || !reflect.DeepEqual(values, []int64{0}) {
					t.Fatal("empty COUNT row lost in a later stage", values, err)
				}
				for _, limit := range []int{0, 1} {
					page, err := result.Page(t.Context(), limit, 0)
					check(t, err)
					if page.Total != 1 || len(page.Rows) != limit || page.Rows == nil {
						t.Fatal("empty aggregate page lost its one-row total", page)
					}
				}
				emptyProjection, err := orm.SelectResults(stage, orm.Project1(orm.SelectedField(stage, unused), func(n orm.Optional[int64]) orm.Optional[int64] { t.Fatal("empty derived builder ran"); return n }))
				check(t, err)
				page, err := emptyProjection.Page(t.Context(), 5, 0)
				check(t, err)
				if page.Total != 0 || len(page.Rows) != 0 || page.Rows == nil {
					t.Fatal("empty producer page has no valid empty envelope", page)
				}
			})
			within(t, func(session db.Session) {
				input, err := orm.SelectQuery(records.MeasureObjects.Using(session).Filter(f.Code.Exact("a")), orm.Project1(f.Amount, func(n int64) int64 { return n }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				value := orm.Divide(orm.SelectedField(stage, f.Amount), int64(0))
				built := 0
				selected, err := orm.SelectResults(stage, orm.Project1(value, func(n orm.Optional[int64]) orm.Optional[int64] { built++; return n }))
				check(t, err)
				rows, err := selected.All(t.Context())
				if vendor == "postgres" {
					var native *pgconn.PgError
					if !errors.As(err, &native) || native.Code != "22012" || rows != nil || built != 0 {
						t.Fatal("native zero division changed", rows, err, built)
					}
				} else if err != nil || len(rows) != 1 || rows[0].Valid() || built != 1 {
					t.Fatal("SQLite zero division lost NULL", rows, err, built)
				}
			})
		})
		t.Run("native_overflow_and_statement_failure", func(t *testing.T) {
			within(t, func(session db.Session) {
				literal := orm.Value[records.Measure](int64(math.MaxInt64))
				input, err := orm.SelectQuery(records.MeasureObjects.Using(session), orm.Project1(literal, func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				operand := orm.OptionalOperand(orm.SelectedField(stage, literal))
				built := 0
				selected, err := orm.SelectResults(stage, orm.Project1(orm.Add(operand, int64(1)), func(n orm.Optional[int64]) orm.Optional[int64] { built++; return n }))
				check(t, err)
				rows, err := selected.All(t.Context())
				if err == nil || rows != nil || built != 0 {
					t.Fatal("derived overflow published a value", rows, err, built)
				}
				if vendor == "postgres" {
					var native *pgconn.PgError
					if !errors.As(err, &native) || native.Code != "22003" {
						t.Fatal("native overflow code lost", err)
					}
				}
			})
		})
		t.Run("scope_and_context_rejected_before_io", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &derivedReadProbe{Session: session}
				input, err := orm.SelectQuery(records.MeasureObjects.Using(probe), orm.Project1(f.Amount, func(n int64) int64 { return n }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				foreign, err := orm.Derive(input)
				check(t, err)
				column := orm.SelectedField(stage, f.Amount)
				if _, err := orm.SelectResults(foreign, orm.Project1(column, func(n int64) int64 { return n })); err == nil {
					t.Fatal("foreign identical scope accepted")
				}
				for _, expression := range []orm.DynamicExpression{orm.DynamicColumn(1), orm.DynamicColumn(-1), orm.DynamicF("amount"), orm.DynamicColumn(0).Add("text")} {
					if _, err := stage.Expression(expression); err == nil {
						t.Fatal("bad result binding accepted")
					}
				}
				selected, err := orm.SelectResults(stage, orm.Project1(column, func(n int64) int64 { return n }))
				check(t, err)
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if rows, err := selected.All(ctx); !errors.Is(err, context.Canceled) || rows != nil {
					t.Fatal("canceled result read", rows, err)
				}
				if rows, err := selected.All(nil); err == nil || rows != nil {
					t.Fatal("nil result context", rows, err)
				}
				if probe.calls() != 0 {
					t.Fatal("invalid declaration performed I/O", probe.calls())
				}
			})
		})
		t.Run("cache_cells_and_concurrent_copies", func(t *testing.T) {
			within(t, func(session db.Session) {
				probe := &derivedReadProbe{Session: session}
				raw := records.MeasureObjects.Using(probe)
				_, err := raw.All(t.Context())
				check(t, err)
				input, err := orm.SelectQuery(raw, orm.Project1(f.GroupKey, func(value *string) *string { t.Fatal("source DTO builder ran"); return value }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				column := orm.SelectedField(stage, f.GroupKey)
				var builds atomic.Int64
				selected, err := orm.SelectResults(stage, orm.Project1(column, func(value *string) *string { builds.Add(1); return value }))
				check(t, err)
				selected = selected.OrderBy(column.Asc().NullsFirst())
				type answer struct {
					rows []*string
					err  error
				}
				results := make(chan answer, 16)
				for range 16 {
					copy := selected
					go func() { rows, err := copy.All(t.Context()); results <- answer{rows, err} }()
				}
				seen := map[*string]bool{}
				for range 16 {
					result := <-results
					check(t, result.err)
					if len(result.rows) != 5 || result.rows[0] != nil || result.rows[1] == nil || *result.rows[1] != "" || seen[result.rows[1]] {
						t.Fatal("cache leaked mutable nullable cells", result.rows)
					}
					seen[result.rows[1]] = true
					*result.rows[1] = "caller mutation"
				}
				if probe.calls() != 2 || builds.Load() != 80 {
					t.Fatal("copies did not share one read and build independent DTOs", probe.calls(), builds.Load())
				}
				if n, err := selected.Count(t.Context()); err != nil || n != 5 || probe.calls() != 2 {
					t.Fatal("warm result count", n, err)
				}
				values, err := selected.Filter(column.IsNull(false)).All(t.Context())
				check(t, err)
				if len(values) != 4 || probe.calls() != 3 {
					t.Fatal("refinement did not read a fresh result", values, probe.calls())
				}
			})
		})
		t.Run("cursor_failure_and_session_lifetime", func(t *testing.T) {
			for _, fault := range []string{"scan", "iteration", "close"} {
				within(t, func(session db.Session) {
					probe := &derivedReadProbe{Session: session, fault: fault}
					input, err := orm.SelectQuery(records.MeasureObjects.Using(probe), orm.Project1(f.Code, func(code string) string { return code }))
					check(t, err)
					stage, err := orm.Derive(input)
					check(t, err)
					built := 0
					selected, err := orm.SelectResults(stage, orm.Project1(orm.SelectedField(stage, f.Code), func(code string) string { built++; return code }))
					check(t, err)
					rows, err := selected.All(t.Context())
					if err == nil || rows != nil || built != 0 || probe.closes.Load() != 1 {
						t.Fatal("failed native cursor published", fault, rows, err, built, probe.closes.Load())
					}
					page, err := selected.Page(t.Context(), 2, 0)
					if err == nil || page.Rows != nil || page.Total != 0 || built != 0 || probe.closes.Load() != 2 {
						t.Fatal("failed native page published", fault, page, err, built)
					}
					probe.fault = ""
					rows, err = selected.All(t.Context())
					check(t, err)
					if len(rows) != 5 || built != 5 || probe.calls() != 3 {
						t.Fatal("failure warmed cache", rows, built, probe.calls())
					}
				})
			}
			var held orm.ResultQuery[orm.ResultOf[records.Measure], string]
			within(t, func(session db.Session) {
				input, err := orm.SelectQuery(records.MeasureObjects.Using(session), orm.Project1(f.Code, func(code string) string { return code }))
				check(t, err)
				stage, err := orm.Derive(input)
				check(t, err)
				held, err = orm.SelectResults(stage, orm.Project1(orm.SelectedField(stage, f.Code), func(code string) string { return code }))
				check(t, err)
				_, err = held.All(t.Context())
				check(t, err)
			})
			if rows, err := held.All(t.Context()); err == nil || rows != nil {
				t.Fatal("cached derived query escaped session", rows, err)
			}
			if count, err := held.Count(t.Context()); err == nil || count != 0 {
				t.Fatal("cached derived count escaped session", count, err)
			}
		})
	})
}
