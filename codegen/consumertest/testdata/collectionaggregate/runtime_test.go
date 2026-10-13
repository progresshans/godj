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
	"time"

	"example.com/godj-project-bundle/catalog"
	"example.com/godj-project-bundle/project"
	"example.com/godj-project-bundle/records"
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

type collectionReadProbe struct {
	collectionBackend
	mu     sync.Mutex
	plans  []query.Plan
	closed atomic.Int64
	fault  string
}

func (p *collectionReadProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.mu.Lock()
	p.plans = append(p.plans, plan)
	p.mu.Unlock()
	if p.fault == "query" {
		return nil, errors.New("collection query failure")
	}
	rows, err := p.collectionBackend.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	return &collectionRowsProbe{Rows: rows, owner: p}, nil
}
func (p *collectionReadProbe) calls() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.plans) }

type collectionRowsProbe struct {
	db.Rows
	owner *collectionReadProbe
}

func (r *collectionRowsProbe) Scan(values ...any) error {
	if r.owner.fault == "scan" {
		return errors.New("collection scan failure")
	}
	return r.Rows.Scan(values...)
}
func (r *collectionRowsProbe) Err() error {
	if r.owner.fault == "iteration" {
		return errors.Join(r.Rows.Err(), errors.New("collection iteration failure"))
	}
	return r.Rows.Err()
}
func (r *collectionRowsProbe) Close() error {
	r.owner.closed.Add(1)
	err := r.Rows.Close()
	if r.owner.fault == "close" {
		return errors.Join(err, errors.New("collection close failure"))
	}
	return err
}

// A borrowed session deliberately does not acquire root-backend coordination
// capabilities from collectionReadProbe. Its lifetime authority remains the
// actual transaction session, including while results are warm.
type collectionSessionProbe struct {
	db.Session
	validator db.SessionValidator
	calls     int
}

func (p *collectionSessionProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	p.calls++
	return p.Session.Query(ctx, plan)
}
func (p *collectionSessionProbe) ValidateSession(ctx context.Context) error {
	return p.validator.ValidateSession(ctx)
}

func assertCollectionJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	data, err := json.Marshal(actual)
	check(t, err)
	var left, right any
	check(t, json.Unmarshal(data, &left))
	check(t, json.Unmarshal([]byte(expected), &right))
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("collection observation = %s, want %s", data, expected)
	}
}
func collectionStorage(t *testing.T, backend db.Queryer) any {
	t.Helper()
	categories, err := catalog.CategoryObjects.Using(backend).OrderBy(catalog.CategoryFields.ID.Asc()).All(t.Context())
	check(t, err)
	labels, err := catalog.LabelObjects.Using(backend).OrderBy(catalog.LabelFields.ID.Asc()).All(t.Context())
	check(t, err)
	tickets, err := records.TicketObjects.Using(backend).OrderBy(records.TicketFields.ID.Asc()).All(t.Context())
	check(t, err)
	links, err := records.LinkObjects.Using(backend).OrderBy(records.LinkFields.ID.Asc()).All(t.Context())
	check(t, err)
	details, err := records.DetailObjects.Using(backend).OrderBy(records.DetailFields.ID.Asc()).All(t.Context())
	check(t, err)
	return []any{categories, labels, tickets, links, details}
}

func TestCollectionRuntime(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, vendor string, physical collectionPhysicalWriter) {
		seed := seedCollection(t, backend)
		before := collectionStorage(t, backend)
		relations, err := project.BindRelations()
		check(t, err)
		target, f := relations.CatalogLabel.Tickets, catalog.LabelFields
		t.Run("typed_dynamic_and_nullable_values", func(t *testing.T) {
			probe := &collectionReadProbe{collectionBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			typed := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate4(orm.Count(target.ID), orm.Count(target.ID).Distinct(), orm.Sum(target.Amount), orm.Avg(target.Amount),
				func(total, unique int64, sum orm.Optional[int64], average orm.Optional[float64]) map[string]any {
					return map[string]any{"total": total, "unique": unique, "sum": optionalValue(sum), "average": optionalValue(average)}
				}))
			dynamic := collectionGroup(t, api.CatalogLabel, orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__id"}, orm.DynamicAggregateInput{Kind: query.ResultCount, Field: "tickets__id", Distinct: true}, orm.DynamicAggregateInput{Kind: query.ResultSum, Field: "tickets__amount"}, orm.DynamicAggregateInput{Kind: query.ResultAvg, Field: "tickets__amount"})
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("typed/dynamic collection AST differs")
			}
			actual, err := typed.All(t.Context())
			check(t, err)
			want := `[{"code":"blue","total":2,"unique":2,"sum":-1,"average":-0.5},{"code":"foreign","total":1,"unique":1,"sum":null,"average":null},{"code":"none","total":0,"unique":0,"sum":null,"average":null},{"code":"nullonly","total":1,"unique":1,"sum":null,"average":null},{"code":"orphan","total":1,"unique":1,"sum":9,"average":9},{"code":"red","total":4,"unique":3,"sum":9,"average":3}]`
			assertCollectionJSON(t, actual, want)
			assertCollectionJSON(t, collectionRows(t, dynamic, "code", "total", "unique", "sum", "average"), want)
			if probe.calls() != 2 || !probe.plans[0].Equal(probe.plans[1]) {
				t.Fatal("typed/dynamic executed plans differ")
			}
			// Boolean is a countable field, but its nullable row value is not ordered.
			grouped := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate1(orm.Count(target.Enabled), func(n int64) map[string]any { return map[string]any{"present": n} }))
			rows, err := grouped.All(t.Context())
			check(t, err)
			assertCollectionJSON(t, rows, `[{"code":"blue","present":2},{"code":"foreign","present":0},{"code":"none","present":0},{"code":"nullonly","present":1},{"code":"orphan","present":1},{"code":"red","present":3}]`)
		})
		t.Run("join_multiplicity_filter_scopes_and_parameters", func(t *testing.T) {
			assertCollectionJSON(t, observeCollection(t, backend, "chained_filters"), `[{"code":"blue","total":2,"amount":4,"average":2,"opened":2},{"code":"red","total":6,"amount":18,"average":3,"opened":4}]`)
			assertCollectionJSON(t, observeCollection(t, backend, "chained_filters_reversed"), `[{"code":"blue","total":2,"amount":-1,"average":-0.5,"opened":2},{"code":"red","total":6,"amount":12,"average":2,"opened":6}]`)
			assertCollectionJSON(t, observeCollection(t, backend, "independent_collection_counts"), `[{"code":"a","labels":16,"tickets":16,"amount":16},{"code":"b","labels":1,"tickets":1,"amount":null},{"code":"empty","labels":0,"tickets":0,"amount":null}]`)
			assertCollectionJSON(t, observeCollection(t, backend, "independent_distinct_aggregates"), `[{"code":"a","labels":4,"tickets":4,"amount":4},{"code":"b","labels":1,"tickets":1,"amount":null},{"code":"empty","labels":0,"tickets":0,"amount":null}]`)
			assertCollectionJSON(t, observeCollection(t, backend, "nested_collections"), `[{"code":"blue","total":4,"unique":2},{"code":"foreign","total":2,"unique":2},{"code":"none","total":0,"unique":0},{"code":"nullonly","total":1,"unique":1},{"code":"orphan","total":1,"unique":1},{"code":"red","total":9,"unique":3}]`)
			// FILTER uses row joins; root negation uses correlated existence. Null
			// intermediary rows count the root but cannot count an absent target.
			assertCollectionJSON(t, observeCollection(t, backend, "negated_count"), `[{"code":"blue","total":0},{"code":"foreign","total":1},{"code":"none","total":0},{"code":"nullonly","total":0},{"code":"orphan","total":1},{"code":"red","total":2}]`)
			assertCollectionJSON(t, observeCollection(t, backend, "negated_root_count"), `[{"code":"blue","total":0},{"code":"foreign","total":1},{"code":"none","total":1},{"code":"nullonly","total":0},{"code":"orphan","total":1},{"code":"red","total":3}]`)
			api, err := project.Using(backend)
			check(t, err)
			value := orm.Sum(target.Amount).Where(target.Amount.Exact(2))
			source := api.CatalogLabel.Filter(target.Amount.GreaterThan(1)).Filter(target.Amount.LessThan(10))
			grouped := typedCollectionGroup(t, source, orm.Aggregate1(value, func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
			plan := grouped.Plan()
			sql, args, err := sqlite.Compile(plan)
			check(t, err)
			if strings.Count(sql, `JOIN "main"."gdj_collection_ticket"`) != 2 || strings.Count(sql, `JOIN "main"."gdj_collection_link"`) != 2 || !reflect.DeepEqual(args, []any{int64(2), int64(1), int64(10)}) {
				t.Fatal("aggregate added a multiplier or reordered parameters", sql, args)
			}
			_, err = grouped.All(t.Context())
			check(t, err)
			if !plan.Equal(grouped.Plan()) {
				t.Fatal("compiler rewrote public aggregate identity")
			}
		})
		t.Run("result_identity_having_page_and_derived", func(t *testing.T) {
			probe := &collectionReadProbe{collectionBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			total := orm.Count(target.ID)
			grouped := namedCollectionAggregate(t, api.CatalogLabel, orm.Aggregate1(total, func(n int64) map[string]any { return map[string]any{"total": n} }))
			page, err := grouped.Having(total.GreaterThanOrEqual(2)).Page(t.Context(), 1, 1)
			check(t, err)
			if page.Total != 2 || len(page.Rows) != 1 || page.Rows[0]["code"] != "red" || probe.calls() != 1 {
				t.Fatal("collection HAVING page lost total or used multiple statements", page, probe.calls())
			}
			past, err := grouped.Page(t.Context(), 2, 50)
			check(t, err)
			if past.Total != 6 || len(past.Rows) != 0 || past.Rows == nil {
				t.Fatal("past-end collection page lost total", past)
			}
			source := api.CatalogLabel.Filter(target.Enabled.Exact(true)).Filter(target.Amount.GreaterThan(0))
			sum := orm.Sum(target.Amount)
			selected := typedCollectionGroup(t, source, orm.Aggregate1(sum, func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
			selected = selected.Having(sum.GreaterThan(orm.Some(int64(10))))
			stage, err := orm.Derive(selected)
			check(t, err)
			column := orm.SelectedAggregate(stage, sum)
			result, err := orm.AggregateResults(stage, orm.Aggregate1(orm.Sum(orm.Numeric(orm.OptionalOperand(column))), func(n orm.Optional[int64]) orm.Optional[int64] { return n }))
			check(t, err)
			value, err := result.One(t.Context())
			check(t, err)
			if n, ok := value.Get(); !ok || n != 18 {
				t.Fatal("bound latest-scope aggregate lost selected identity", value)
			}
			other, err := orm.Derive(selected)
			check(t, err)
			foreign := orm.SelectedAggregate(other, sum)
			calls := probe.calls()
			if _, err := orm.SelectResults(stage, orm.Project1(foreign, func(n orm.Optional[int64]) orm.Optional[int64] { return n })); err == nil {
				t.Fatal("foreign aggregate result scope accepted")
			}
			if probe.calls() != calls {
				t.Fatal("foreign result scope performed I/O")
			}
			assertCollectionJSON(t, observeCollection(t, backend, "derived_aggregate"), `{"amount":9,"maximum":4}`)
		})
		t.Run("source_scope_invalid_metadata_and_bounds", func(t *testing.T) {
			probe := &collectionReadProbe{collectionBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			valid := []orm.DynamicAggregateInput{{Kind: query.ResultCount, Field: "tickets__id"}}
			if _, err := orm.GroupValues(catalog.LabelObjects.Using(probe), []string{"code"}, valid); err == nil {
				t.Fatal("root query invented cross-app authority")
			}
			for _, inputs := range [][]orm.DynamicAggregateInput{
				{{Kind: query.ResultCount, Field: "tickets__missing"}}, {{Kind: query.ResultCount, Field: "tickets__document"}}, {{Kind: query.ResultMin, Field: "tickets__enabled"}},
				{{Kind: query.ResultCountAll, Field: "tickets__id"}}, {{Kind: query.ResultMin, Field: "tickets__amount", Distinct: true}},
				{{Kind: query.ResultSum, Expression: orm.DynamicF("tickets__amount").Add(int64(1))}},
				{{Kind: query.ResultCount, Field: "tickets__id", Filter: []orm.LookupInput{{Key: "tickets__category__missing", Value: 1}}}},
				make([]orm.DynamicAggregateInput, query.MaxAggregateExpressions+1),
			} {
				if _, err := api.CatalogLabel.GroupValues([]string{"code"}, inputs); err == nil {
					t.Fatal("invalid collection aggregate accepted", inputs)
				}
			}
			if _, err := api.CatalogLabel.GroupValues([]string{"tickets__code"}, valid); err == nil {
				t.Fatal("collection aggregate leaked into group key")
			}
			if _, err := api.CatalogLabel.Expression(orm.DynamicF("tickets__amount")); err == nil {
				t.Fatal("collection became a scalar expression")
			}
			if _, err := orm.AggregateQuery(catalog.LabelObjects.Using(probe), orm.Aggregate1(orm.Count(orm.Scalar(target.Amount)), func(n int64) int64 { return n })); err == nil {
				t.Fatal("scalar wrapper bypassed collection cardinality")
			}
			invalidField := orm.NewNullableIntegerField[catalog.Label](ir.Field{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldInteger, Nullable: true})
			if _, err := orm.AggregateQuery(catalog.LabelObjects.Using(probe).Filter(f.ID.In()), orm.Aggregate1(orm.Count(invalidField), func(n int64) int64 { return n })); err == nil {
				t.Fatal("forged source field survived empty source")
			}
			limited, err := api.CatalogLabel.Limit(0)
			check(t, err)
			for _, source := range []project.CatalogLabelQuery{limited, api.CatalogLabel.Distinct(), api.CatalogLabel.OrderBy(f.ID.Asc()), api.CatalogLabel.SelectForUpdate(orm.RowLockOptions{})} {
				if _, err := source.GroupValues([]string{"code"}, valid); err == nil {
					t.Fatal("unsupported grouped source state accepted")
				}
			}
			grouped := collectionCountQuery(t, probe)
			for _, bad := range []orm.GroupedQuery[catalog.Label, orm.GroupRow]{grouped.Having(orm.Sum(target.Amount).GreaterThan(orm.Some(int64(1)))), grouped.HavingDynamic(orm.GroupLookupInput{Column: 1, Lookup: query.LookupExact, Value: "1"}), grouped.OrderBy(f.ID.Asc())} {
				if rows, err := bad.All(t.Context()); err == nil || rows != nil {
					t.Fatal("unselected or mistyped aggregate handle accepted")
				}
			}
			for _, bounds := range [][2]int{{-1, 0}, {1, -1}} {
				if _, err := grouped.Page(t.Context(), bounds[0], bounds[1]); err == nil {
					t.Fatal("negative page bound accepted")
				}
			}
			if probe.calls() != 0 {
				t.Fatal("invalid collection construction performed I/O", probe.calls())
			}
		})
		t.Run("cache_context_and_cursor_failures", func(t *testing.T) {
			probe := &collectionReadProbe{collectionBackend: backend}
			grouped := collectionCountQuery(t, probe)
			var wait sync.WaitGroup
			failures := make(chan error, 8)
			for range 8 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					rows, err := grouped.All(t.Context())
					if err == nil && (len(rows) != 6 || !rows[0].Aggregates[0].Equal(query.Integer(2))) {
						err = errors.New("collection concurrent cache changed")
					}
					failures <- err
				}()
			}
			wait.Wait()
			close(failures)
			for err := range failures {
				check(t, err)
			}
			if probe.calls() != 1 || probe.closed.Load() != 1 {
				t.Fatal("cold collection cache duplicated query or leaked cursor", probe.calls())
			}
			first, err := grouped.All(t.Context())
			check(t, err)
			first[0].Keys[0] = query.String("mutated")
			first[0].Aggregates[0] = query.Integer(900)
			second, err := grouped.All(t.Context())
			check(t, err)
			if !second[0].Keys[0].Equal(query.String("blue")) || !second[0].Aggregates[0].Equal(query.Integer(2)) {
				t.Fatal("collection cache leaked published cells")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if rows, err := grouped.All(ctx); !errors.Is(err, context.Canceled) || rows != nil || probe.calls() != 1 {
				t.Fatal("warm collection cache ignored cancellation")
			}
			if _, err := grouped.Page(ctx, 1, 0); !errors.Is(err, context.Canceled) || probe.calls() != 1 {
				t.Fatal("collection page ignored cancellation")
			}
			_, err = grouped.Fresh().All(t.Context())
			check(t, err)
			if probe.calls() != 2 {
				t.Fatal("fresh collection result reused old evaluation")
			}
			for _, fault := range []string{"query", "scan", "iteration", "close"} {
				failed := &collectionReadProbe{collectionBackend: backend, fault: fault}
				api, err := project.Using(failed)
				check(t, err)
				built := 0
				value := typedCollectionGroup(t, api.CatalogLabel, orm.Aggregate1(orm.Count(target.ID), func(n int64) int64 { built++; return n }))
				rows, err := value.All(t.Context())
				expectedClosed := int64(1)
				if fault == "query" {
					expectedClosed = 0
				}
				if err == nil || rows != nil || built != 0 || failed.closed.Load() != expectedClosed {
					t.Fatal("collection failure published a prefix or leaked", fault, err, built, failed.closed.Load())
				}
				failed.fault = ""
				retried, err := value.All(t.Context())
				check(t, err)
				if len(retried) != 6 || failed.calls() != 2 {
					t.Fatal("failed collection cache poisoned retry", fault)
				}
				failed.fault = fault
				built = 0
				page, err := value.Page(t.Context(), 2, 0)
				if err == nil || page.Rows != nil || page.Total != 0 || built != 0 {
					t.Fatal("failed collection page published a prefix", fault, page, err)
				}
			}
		})
		t.Run("borrowed_session_lifetime", func(t *testing.T) {
			rollback := errors.New("collection rollback")
			for _, commit := range []bool{false, true} {
				var warm, cold orm.GroupedQuery[catalog.Label, orm.GroupRow]
				var escaped orm.ResultQuery[orm.ResultOf[catalog.Label], int64]
				var probe *collectionSessionProbe
				err := backend.Atomic(t.Context(), func(session db.Session) error {
					validator, ok := session.(db.SessionValidator)
					if !ok {
						return errors.New("session has no lifetime validator")
					}
					probe = &collectionSessionProbe{Session: session, validator: validator}
					warm = collectionCountQuery(t, probe)
					cold = collectionCountQuery(t, probe)
					rows, err := warm.All(t.Context())
					if err != nil {
						return err
					}
					if len(rows) != 6 {
						return errors.New("borrowed aggregate rows")
					}
					stage, err := orm.Derive(warm)
					if err != nil {
						return err
					}
					escaped, err = orm.AggregateResults(stage, orm.Aggregate1(orm.CountRows[orm.ResultOf[catalog.Label]](), func(n int64) int64 { return n }))
					if err != nil {
						return err
					}
					if _, err = escaped.One(t.Context()); err != nil {
						return err
					}
					if !commit {
						return rollback
					}
					return nil
				})
				if commit {
					check(t, err)
				} else if !errors.Is(err, rollback) {
					t.Fatal("collection rollback cause lost", err)
				}
				calls := probe.calls
				for _, value := range []orm.GroupedQuery[catalog.Label, orm.GroupRow]{warm, cold, warm.Fresh()} {
					if rows, err := value.All(t.Context()); err == nil || rows != nil {
						t.Fatal("collection result escaped session lifetime", commit)
					}
					if page, err := value.Page(t.Context(), 1, 0); err == nil || page.Rows != nil || page.Total != 0 {
						t.Fatal("collection page escaped session lifetime")
					}
				}
				if _, err := escaped.One(t.Context()); err == nil {
					t.Fatal("derived collection cache escaped session lifetime")
				}
				if probe.calls != calls {
					t.Fatal("closed collection session performed query")
				}
			}
		})
		t.Run("native_numeric_domains_and_failures", func(t *testing.T) {
			rollback := errors.New("collection numeric rollback")
			err := backend.Atomic(t.Context(), func(session db.Session) error {
				_, err := records.TicketObjects.Using(session).Filter(records.TicketFields.Code.Exact("a")).Update(t.Context(), orm.Assign(records.TicketFields.Amount, int64(math.MaxInt64)))
				if err != nil {
					return err
				}
				built := 0
				_, err = orm.AggregateInto(t.Context(), catalog.LabelObjects.Using(session).Filter(f.Code.Exact("red")), orm.Aggregate1(orm.Sum(target.Amount), func(n orm.Optional[int64]) orm.Optional[int64] { built++; return n }))
				if err == nil || built != 0 {
					return errors.New("collection integer overflow published a value")
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			// Check physical field domains on the related operand before aggregation.
			invalid := "'not-a-decimal'"
			if vendor == "postgres" {
				invalid = "'NaN'::numeric"
			}
			check(t, physical("ticket", "price", invalid, seed.tickets["a"].ID))
			func() {
				defer func() {
					_, err := records.TicketObjects.Using(backend).Filter(records.TicketFields.ID.Exact(seed.tickets["a"].ID)).Update(t.Context(), orm.Assign(records.TicketFields.Price, *seed.tickets["a"].Price))
					check(t, err)
				}()
				source := catalog.LabelObjects.Using(backend).Filter(f.Code.Exact("red"))
				for _, value := range []orm.AggregateExpression[catalog.Label, orm.Optional[decimal.Decimal]]{orm.Sum(target.Price), orm.Avg(target.Price), orm.Min(target.Price), orm.Max(target.Price)} {
					built := 0
					_, err := orm.AggregateInto(t.Context(), source, orm.Aggregate1(value, func(n orm.Optional[decimal.Decimal]) orm.Optional[decimal.Decimal] { built++; return n }))
					if err == nil || built != 0 {
						t.Fatal("collection Decimal bypassed operand/result validation", err)
					}
				}
			}()
			// Aggregate result precision can exceed a field's decimal_places; MIN
			// continues to decode its actual field domain.
			api, err := project.Using(backend)
			check(t, err)
			values := typedCollectionGroup(t, api.CatalogLabel.Filter(f.Code.Exact("blue")), orm.Aggregate3(orm.Sum(target.Price), orm.Avg(target.Price), orm.Min(target.Price), func(sum, average, minimum orm.Optional[decimal.Decimal]) [3]orm.Optional[decimal.Decimal] {
				return [3]orm.Optional[decimal.Decimal]{sum, average, minimum}
			}))
			rows, err := values.All(t.Context())
			check(t, err)
			if len(rows) != 1 {
				t.Fatal("typed decimal collection row count")
			}
			for i, want := range []string{"10.25", "5.125", "-2.25"} {
				n, ok := rows[0][i].Get()
				if !ok || n.String() != want {
					t.Fatal("collection decimal domain narrowed", i, n, want)
				}
			}
			durationValues := typedCollectionGroup(t, api.CatalogLabel.Filter(f.Code.Exact("blue")), orm.Aggregate2(orm.Sum(target.Elapsed), orm.Avg(target.Elapsed), func(sum, average orm.Optional[duration.Duration]) [2]orm.Optional[duration.Duration] {
				return [2]orm.Optional[duration.Duration]{sum, average}
			}))
			times, err := durationValues.All(t.Context())
			check(t, err)
			for i, want := range []int64{7, 4} {
				n, ok := times[0][i].Get()
				micros, err := n.TotalMicroseconds()
				check(t, err)
				if !ok || micros != want {
					t.Fatal("collection duration rounding", i, micros)
				}
			}
		})
		t.Run("ordered_field_codecs", func(t *testing.T) {
			probe := &collectionReadProbe{collectionBackend: backend}
			api, err := project.Using(probe)
			check(t, err)
			source := api.CatalogLabel.Filter(f.Code.Exact("blue"))
			type ordered struct {
				Binary   orm.Optional[binaryvalue.Value]
				Identity orm.Optional[uuid.UUID]
				Date     orm.Optional[calendar.Date]
				Time     orm.Optional[clock.Time]
				Stamp    orm.Optional[time.Time]
			}
			typed := typedCollectionGroup(t, source, orm.Aggregate5(orm.Min(target.Binary), orm.Max(target.Identity), orm.Min(target.Date), orm.Max(target.Time), orm.Min(target.Stamp), func(a orm.Optional[binaryvalue.Value], b orm.Optional[uuid.UUID], c orm.Optional[calendar.Date], d orm.Optional[clock.Time], e orm.Optional[time.Time]) ordered {
				return ordered{a, b, c, d, e}
			}))
			dynamic := collectionGroup(t, source, orm.DynamicAggregateInput{Kind: query.ResultMin, Field: "tickets__binary"}, orm.DynamicAggregateInput{Kind: query.ResultMax, Field: "tickets__identity"}, orm.DynamicAggregateInput{Kind: query.ResultMin, Field: "tickets__date"}, orm.DynamicAggregateInput{Kind: query.ResultMax, Field: "tickets__time"}, orm.DynamicAggregateInput{Kind: query.ResultMin, Field: "tickets__stamp"})
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("related ordered typed/dynamic plans differ")
			}
			rows, err := typed.All(t.Context())
			check(t, err)
			if len(rows) != 1 {
				t.Fatal("ordered collection row count")
			}
			binary, ok := rows[0].Binary.Get()
			if !ok || !reflect.DeepEqual(binary.Bytes(), []byte{1, 0, 255}) {
				t.Fatal("related binary ordering")
			}
			identity, ok := rows[0].Identity.Get()
			if !ok || identity.String() != "00000000-0000-0000-0000-000000000002" {
				t.Fatal("related UUID ordering")
			}
			date, ok := rows[0].Date.Get()
			if !ok || date.String() != "2026-01-01" {
				t.Fatal("related date ordering")
			}
			clockTime, ok := rows[0].Time.Get()
			if !ok || clockTime.String() != "12:30:02.123456" {
				t.Fatal("related time ordering")
			}
			stamp, ok := rows[0].Stamp.Get()
			if !ok || !stamp.Equal(time.Date(2026, time.January, 1, 12, 30, 0, 0, time.UTC)) {
				t.Fatal("related datetime ordering")
			}
			assertCollectionJSON(t, collectionRows(t, dynamic, "code", "binary", "identity", "date", "time", "stamp"), `[{"code":"blue","binary":"0100ff","identity":"00000000-0000-0000-0000-000000000002","date":"2026-01-01","time":"12:30:02.123456","stamp":"2026-01-01T12:30:00Z"}]`)
			empty := typedCollectionGroup(t, api.CatalogLabel.Filter(f.Code.In("none", "nullonly")), orm.Aggregate1(orm.Min(target.Date), func(n orm.Optional[calendar.Date]) orm.Optional[calendar.Date] { return n }))
			absent, err := empty.All(t.Context())
			check(t, err)
			if len(absent) != 2 || absent[0].Valid() || absent[1].Valid() {
				t.Fatal("missing/all-null ordered collection acquired a value")
			}
		})
		if !reflect.DeepEqual(before, collectionStorage(t, backend)) {
			t.Fatal("collection observations changed storage")
		}
	})
}
