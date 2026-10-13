package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type relatedReadProbe struct {
	db.Session
	mu     sync.Mutex
	plans  []query.Plan
	closed atomic.Int64
	fault  string
}

func (p *relatedReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.mu.Lock()
	p.plans = append(p.plans, plan)
	p.mu.Unlock()
	rows, err := p.Session.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &relatedRows{Rows: rows, owner: p}, nil
}
func (p *relatedReadProbe) calls() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.plans) }
func (p *relatedReadProbe) ValidateSession(ctx context.Context) error {
	if validator, ok := p.Session.(db.SessionValidator); ok {
		return validator.ValidateSession(ctx)
	}
	return ctx.Err()
}

type relatedRows struct {
	db.Rows
	owner *relatedReadProbe
}

func (rows *relatedRows) Scan(values ...any) error {
	if rows.owner.fault == "scan" {
		return errors.New("related scan failure")
	}
	return rows.Rows.Scan(values...)
}
func (rows *relatedRows) Err() error {
	if rows.owner.fault == "iteration" {
		return errors.Join(rows.Rows.Err(), errors.New("related iteration failure"))
	}
	return rows.Rows.Err()
}
func (rows *relatedRows) Close() error {
	rows.owner.closed.Add(1)
	err := rows.Rows.Close()
	if rows.owner.fault == "close" {
		return errors.Join(err, errors.New("related close failure"))
	}
	return err
}

func assertRelatedJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	data, err := json.Marshal(actual)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(data, &left))
	check(t, json.Unmarshal([]byte(expected), &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("related observation = %s, want %s", data, expected)
	}
}

func relatedStorage(t *testing.T, backend db.Queryer) any {
	t.Helper()
	groups, err := records.GroupObjects.Using(backend).OrderBy(records.GroupFields.ID.Asc()).All(t.Context())
	check(t, err)
	data, err := records.DatumObjects.Using(backend).OrderBy(records.DatumFields.ID.Asc()).All(t.Context())
	check(t, err)
	measures, err := records.MeasureObjects.Using(backend).OrderBy(records.MeasureFields.ID.Asc()).All(t.Context())
	check(t, err)
	details, err := records.DetailObjects.Using(backend).OrderBy(records.DetailFields.ID.Asc()).All(t.Context())
	check(t, err)
	return []any{groups, data, measures, details}
}

func TestRelatedRuntime(t *testing.T) {
	withRelatedBackends(t, func(t *testing.T, backend relatedBackend, vendor string, physical relatedPhysicalWriter) {
		seed := seedRelated(t, backend)
		before := relatedStorage(t, backend)
		relations, err := project.BindRelations()
		check(t, err)
		f, target := records.MeasureFields, relations.RecordsMeasure.Datum
		added := orm.AddExpressions(orm.F(f.Amount), target.Amount)
		projection := orm.Project2(f.Code, added, func(code string, value orm.Optional[int64]) []query.Value {
			cell := query.Null()
			if number, present := value.Get(); present {
				cell = query.Integer(number)
			}
			return []query.Value{query.String(code), cell}
		})
		t.Run("typed_dynamic_and_nulls", func(t *testing.T) {
			probe := &relatedReadProbe{Session: backend}
			base := records.MeasureObjects.Using(probe).OrderBy(f.ID.Asc())
			typed, err := orm.SelectInto(t.Context(), base, projection)
			check(t, err)
			api, err := project.UsingSession(probe)
			check(t, err)
			selected, err := api.RecordsMeasure.ProjectExpressions(orm.DynamicF("code"), orm.DynamicF("amount").Add(orm.DynamicF("datum__amount")))
			check(t, err)
			dynamic, err := project.SelectRecordsMeasureInto(t.Context(), api.RecordsMeasure.OrderBy(f.ID.Asc()), selected)
			check(t, err)
			if !reflect.DeepEqual(typed, dynamic) || !probe.plans[0].Equal(probe.plans[1]) {
				t.Fatal("typed/dynamic related values or AST diverged", typed, dynamic)
			}
			want := []query.Value{query.Integer(8), query.Null(), query.Null(), query.Integer(-9), query.Integer(4)}
			for index, row := range typed {
				if !row[1].Equal(want[index]) {
					t.Fatal("optional row or target NULL was lost", typed)
				}
			}
			left, right := orm.Add(orm.F(f.Amount), int64(1)), orm.Multiply(target.Amount, int64(2))
			compare := orm.ExactExpressions(left, right)
			native, err := orm.SelectInto(t.Context(), base.Filter(orm.Not(compare)), orm.Project1(f.Code, func(code string) string { return code }))
			check(t, err)
			if !reflect.DeepEqual(native, []string{"d", "e"}) {
				t.Fatal("computed NOT borrowed nullable field compensation", native)
			}
			predicate, err := api.RecordsMeasure.ExpressionPredicate(orm.DynamicNot(orm.DynamicF("amount").Add(int64(1)).Exact(orm.DynamicF("datum__amount").Multiply(int64(2)))))
			check(t, err)
			untyped, err := project.SelectRecordsMeasureInto(t.Context(), api.RecordsMeasure.OrderBy(f.ID.Asc()).Filter(predicate), orm.Project1(f.Code, func(code string) string { return code }))
			check(t, err)
			if !reflect.DeepEqual(native, untyped) || !probe.plans[2].Equal(probe.plans[3]) {
				t.Fatal("typed/dynamic RHS comparison diverged")
			}
			assertRelatedJSON(t, observeRelated(t, backend, "forward_chain_add"), `[{"code":"a","value":15},{"code":"b","value":null},{"code":"c","value":null},{"code":"d","value":-7},{"code":"e","value":11}]`)
			assertRelatedJSON(t, observeRelated(t, backend, "case_rhs_related"), `[{"code":"a","value":"same"},{"code":"b","value":"other"},{"code":"c","value":"other"},{"code":"d","value":"other"},{"code":"e","value":"other"}]`)
		})
		t.Run("related_results_and_join_ownership", func(t *testing.T) {
			probe := &relatedReadProbe{Session: backend}
			both := orm.AddExpressions(added, target.Amount)
			base := records.MeasureObjects.Using(probe).OrderBy(both.Desc().NullsLast(), f.ID.Asc())
			selected, err := orm.SelectQuery(base, orm.Project2(f.Code, both, func(code string, value orm.Optional[int64]) string { return code }))
			check(t, err)
			values, err := selected.All(t.Context())
			check(t, err)
			if !reflect.DeepEqual(values, []string{"a", "e", "d", "b", "c"}) {
				t.Fatal("related ordering or NULL placement", values)
			}
			sql, args, err := sqlite.Compile(selected.Plan())
			check(t, err)
			if strings.Count(sql, `JOIN "gdj_related_datum"`) != 1 || len(args) != 0 {
				t.Fatal("repeated read paths did not share one canonical join", sql, args)
			}
			whole, err := base.Distinct().All(t.Context())
			check(t, err)
			if len(whole) != 5 {
				t.Fatal("hidden related ordering changed model cardinality")
			}
			assertRelatedJSON(t, observeRelated(t, backend, "reverse_add"), `[{"code":"a","value":12},{"code":"b","value":null},{"code":"c","value":12},{"code":"d","value":null},{"code":"e","value":null}]`)
			assertRelatedJSON(t, observeRelated(t, backend, "mixed_route"), `[{"code":"a","value":3},{"code":"b","value":null},{"code":"c","value":null},{"code":"d","value":null},{"code":"e","value":null}]`)
			assertRelatedJSON(t, observeRelated(t, backend, "related_group"), `[{"key":null,"sum":null,"avg":null,"count":0},{"key":-4,"sum":-9,"avg":-9,"count":1},{"key":3,"sum":12,"avg":6,"count":2}]`)
			assertRelatedJSON(t, observeRelated(t, backend, "filtered_count"), `[{"enabled":false,"count":0},{"enabled":true,"count":1}]`)
			assertRelatedJSON(t, observeRelated(t, backend, "aggregate_computed"), `{"sum":3,"avg":1,"count":3}`)
			assertRelatedJSON(t, observeRelated(t, backend, "predicate_or_missing"), `["a","c"]`)
			for _, test := range []struct {
				predicate        orm.Predicate[records.Measure]
				minimum, maximum int64
			}{
				{target.Flag.Exact(true), -5, -2},
				{orm.ExactExpressions(orm.Add(orm.F(f.Amount), int64(1)), orm.Multiply(target.Amount, int64(2))), 5, 5},
			} {
				values, err := orm.AggregateInto(t.Context(), records.MeasureObjects.Using(backend).Filter(test.predicate),
					orm.Aggregate2(orm.Min(f.Amount), orm.Max(f.Amount), func(a, b orm.Optional[int64]) [2]orm.Optional[int64] { return [2]orm.Optional[int64]{a, b} }))
				check(t, err)
				minimum, hasMin := values[0].Get()
				maximum, hasMax := values[1].Get()
				if !hasMin || !hasMax || minimum != test.minimum || maximum != test.maximum {
					t.Fatal("root MIN/MAX lost related filter membership", values)
				}
			}
		})
		t.Run("result_scopes_and_page", func(t *testing.T) {
			probe := &relatedReadProbe{Session: backend}
			left, right := orm.Add(orm.F(f.Amount), int64(1)), orm.Multiply(target.Amount, int64(2))
			base, err := orm.SelectQuery(records.MeasureObjects.Using(probe).OrderBy(f.ID.Asc()), orm.Project3(f.Code, left, right,
				func(code string, left, right orm.Optional[int64]) string { return code }))
			check(t, err)
			stage, err := orm.Derive(base)
			check(t, err)
			code := orm.SelectedField(stage, f.Code)
			l, r := orm.SelectedField(stage, left), orm.SelectedField(stage, right)
			selected, err := orm.SelectResults(stage, orm.Project1(code, func(code string) string { return code }))
			check(t, err)
			selected = selected.Filter(orm.ExactExpressions(l, r)).OrderBy(code.Asc())
			page, err := selected.Page(t.Context(), 1, 0)
			check(t, err)
			if page.Total != 1 || !reflect.DeepEqual(page.Rows, []string{"a"}) || probe.calls() != 1 {
				t.Fatal("selected RHS page lost source boundary", page, probe.calls())
			}
			past, err := selected.Page(t.Context(), 1, 1)
			check(t, err)
			if past.Total != 1 || len(past.Rows) != 0 || past.Rows == nil {
				t.Fatal("past-end page lost total")
			}
			predicate, err := stage.Predicate(orm.DynamicColumn(1).Exact(orm.DynamicColumn(2)))
			check(t, err)
			dynamic, err := orm.SelectResults(stage, orm.Project1(code, func(code string) string { return code }))
			check(t, err)
			if !dynamic.Filter(predicate).OrderBy(code.Asc()).Plan().Equal(selected.Plan()) {
				t.Fatal("selected RHS typed/dynamic plans differ")
			}
			otherStage, err := orm.Derive(base)
			check(t, err)
			foreign := orm.SelectedField(otherStage, right)
			count := probe.calls()
			if rows, err := selected.Filter(orm.ExactExpressions(l, foreign)).All(t.Context()); err == nil || rows != nil || probe.calls() != count {
				t.Fatal("equal-shaped foreign RHS crossed result scope", err)
			}
		})
		t.Run("source_assignment_and_input_rejection", func(t *testing.T) {
			probe := &relatedReadProbe{Session: backend}
			api, err := project.UsingSession(probe)
			check(t, err)
			for _, input := range []orm.DynamicExpression{orm.DynamicF("datum__missing"), orm.DynamicF("datum__measures__amount"), orm.DynamicF("datum__group__data__amount"), orm.DynamicF("amount").Add(orm.DynamicF("datum__flag")), orm.DynamicValue(nil)} {
				if _, err := api.RecordsMeasure.Expression(input); err == nil {
					t.Fatal("invalid related expression accepted")
				}
			}
			if _, err := orm.BindExpression(records.MeasureObjects.Using(probe), orm.DynamicF("datum__amount")); err == nil {
				t.Fatal("root-only query invented project authority")
			}
			if _, err := api.RecordsMeasure.ExpressionPredicate(orm.DynamicF("enabled").LessThan(orm.DynamicF("datum__flag"))); err == nil {
				t.Fatal("ordered Boolean comparison accepted")
			}
			if _, err := api.RecordsMeasure.ExpressionPredicate(orm.DynamicF("amount").Exact(orm.DynamicF("datum__flag"))); err == nil {
				t.Fatal("mixed RHS kind accepted")
			}
			foreign := orm.NewNullableIntegerField[records.Measure](ir.Field{Name: "amount", GoName: "Amount", Column: "amount", Kind: ir.FieldInteger, Nullable: true})
			source := records.MeasureObjects.Using(probe)
			if rows, err := source.Filter(orm.ExactExpressions(orm.F(f.Amount), orm.F(foreign))).All(t.Context()); err == nil || rows != nil {
				t.Fatal("RHS forged source nullability")
			}
			hidden := orm.Case(orm.Value[records.Measure](int64(0)), orm.When(orm.ExactExpressions(orm.F(f.Amount), target.Amount), orm.Value[records.Measure](int64(1))))
			for _, value := range []orm.ScalarOperand[records.Measure, int64]{target.Amount, orm.Add(target.Amount, int64(1)), hidden} {
				count, err := source.Filter(f.ID.In()).Update(t.Context(), orm.AssignExpression(f.Amount, value))
				if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
					t.Fatal("related assignment was not rejected before empty-source elision", count, err)
				}
			}
			if probe.calls() != 0 {
				t.Fatal("invalid expressions performed I/O")
			}
			rollback := errors.New("related filtered update rollback")
			err = backend.Atomic(t.Context(), func(session db.Session) error {
				predicate := orm.ExactExpressions(orm.Add(orm.F(f.Amount), int64(1)), orm.Multiply(target.Amount, int64(2)))
				count, err := records.MeasureObjects.Using(session).Filter(predicate).Update(t.Context(), orm.Assign(f.Other, int64(99)))
				check(t, err)
				if count != 1 {
					t.Fatal("read relation comparison no longer selects write membership", count)
				}
				value, err := records.MeasureObjects.Using(session).Filter(f.Code.Exact("a")).Get(t.Context())
				check(t, err)
				if value.Other == nil || *value.Other != 99 {
					t.Fatal("filtered update did not affect its selected row")
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal("filtered update rollback failed", err)
			}
		})
		t.Run("cache_context_and_cursor_failure", func(t *testing.T) {
			probe := &relatedReadProbe{Session: backend}
			selected, err := orm.SelectQuery(records.MeasureObjects.Using(probe).OrderBy(f.ID.Asc()), projection)
			check(t, err)
			first, err := selected.All(t.Context())
			check(t, err)
			first[0][1] = query.Integer(900)
			second, err := selected.All(t.Context())
			check(t, err)
			if !second[0][1].Equal(query.Integer(8)) || probe.calls() != 1 {
				t.Fatal("related result cache leaked cells or reran")
			}
			var wait sync.WaitGroup
			failures := make(chan error, 8)
			for range 8 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					rows, err := selected.All(t.Context())
					if err == nil && (len(rows) != 5 || !rows[0][1].Equal(query.Integer(8))) {
						err = errors.New("related cached value changed")
					}
					failures <- err
				}()
			}
			wait.Wait()
			close(failures)
			for err := range failures {
				check(t, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if rows, err := selected.All(ctx); !errors.Is(err, context.Canceled) || rows != nil || probe.calls() != 1 {
				t.Fatal("warm cache ignored cancellation")
			}
			for _, fault := range []string{"scan", "iteration", "close"} {
				failed := &relatedReadProbe{Session: backend, fault: fault}
				built := 0
				value, err := orm.SelectInto(t.Context(), records.MeasureObjects.Using(failed), orm.Project1(added, func(value orm.Optional[int64]) orm.Optional[int64] { built++; return value }))
				if err == nil || value != nil || built != 0 || failed.closed.Load() != 1 {
					t.Fatal("failed related cursor published a prefix or leaked", fault, err, built)
				}
			}
			var escaped orm.ResultQuery[records.Measure, []query.Value]
			check(t, backend.Atomic(t.Context(), func(session db.Session) error {
				var err error
				escaped, err = orm.SelectQuery(records.MeasureObjects.Using(session), projection)
				if err != nil {
					return err
				}
				_, err = escaped.All(t.Context())
				return err
			}))
			if rows, err := escaped.All(t.Context()); err == nil || rows != nil {
				t.Fatal("related cache escaped its session lifetime")
			}
		})
		t.Run("physical_target_domains_and_native_errors", func(t *testing.T) {
			base := records.MeasureObjects.Using(backend).Filter(f.Code.Exact("a"))
			value := orm.Add(target.Amount, int64(math.MaxInt64))
			built := 0
			rows, err := orm.SelectInto(t.Context(), base, orm.Project1(value, func(value orm.Optional[int64]) orm.Optional[int64] { built++; return value }))
			if err == nil || rows != nil || built != 0 {
				t.Fatal("native related overflow published a value", err)
			}
			if vendor == "postgres" {
				var native *pgconn.PgError
				if !errors.As(err, &native) || native.Code != "22003" {
					t.Fatal("native overflow SQLSTATE lost", err)
				}
			}
			zero, err := decimal.Parse("0")
			check(t, err)
			invalid := "'not-a-decimal'"
			if vendor == "postgres" {
				invalid = "'NaN'::numeric"
			}
			check(t, physical("datum", "price", invalid, seed.data["d1"].ID))
			defer func() {
				_, err := records.DatumObjects.Using(backend).Filter(records.DatumFields.ID.Exact(seed.data["d1"].ID)).Update(t.Context(), orm.Assign(records.DatumFields.Price, *seed.data["d1"].Price))
				check(t, err)
			}()
			if rows, err := orm.SelectInto(t.Context(), base, orm.Project1(orm.Scalar(target.Price), func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value })); err == nil || rows != nil {
				t.Fatal("related Decimal bypassed terminal precision/physical validation")
			}
			unselected := orm.Case(orm.Value[records.Measure](zero), orm.When(f.Code.Exact("never"), target.Price))
			values, err := orm.SelectInto(t.Context(), base, orm.Project1(unselected, func(value orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { return value }))
			check(t, err)
			if len(values) != 1 || !values[0].Valid() {
				t.Fatal("unselected related CASE branch was evaluated")
			}
		})
		if !reflect.DeepEqual(before, relatedStorage(t, backend)) {
			t.Fatal("related reads or rolled-back writes changed stored state")
		}
	})
}
