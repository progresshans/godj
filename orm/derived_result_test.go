package orm

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestDerivedResultsShareTypedAndDynamicScope(t *testing.T) {
	backend := resultBackendForRows([][]any{{"selected", int64(4), int64(3)}})
	group := groupTestQuery(t, backend)
	source, err := Derive(group)
	if err != nil {
		t.Fatal(err)
	}
	fields := newResultTestFields()
	note, count := SelectedField(source, fields.Note), SelectedAggregate(source, CountRows[resultTestModel]())
	computed := Subtract(count, int64(1))
	type row struct {
		note     *string
		total    int64
		computed Optional[int64]
	}
	projection := CombineProjections(Project2(note, count, func(note *string, count int64) row { return row{note: note, total: count} }),
		Project1(computed, func(value Optional[int64]) Optional[int64] { return value }), func(value row, computed Optional[int64]) row {
			value.computed = computed
			return value
		})
	typed, err := SelectResults(source, projection)
	if err != nil {
		t.Fatal(err)
	}
	typed = typed.Filter(computed.GreaterThanOrEqual(0)).OrderBy(count.Desc())
	dynamicProjection, err := source.Project(DynamicColumn(0), DynamicColumn(1), DynamicColumn(1).Subtract(1))
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := SelectResults(source, dynamicProjection)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := source.Predicate(DynamicColumn(1).Subtract(1).GreaterThanOrEqual(0))
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := source.Expression(DynamicColumn(1))
	if err != nil {
		t.Fatal(err)
	}
	dynamic = dynamic.Filter(predicate).OrderBy(ordering.Desc())
	if !typed.Plan().Equal(dynamic.Plan()) || len(backend.plans) != 0 {
		t.Fatal("typed and dynamic results differ or binding performed I/O")
	}
	values, err := typed.All(t.Context())
	if err != nil || len(values) != 1 || values[0].note == nil || *values[0].note != "selected" || values[0].total != 4 {
		t.Fatal(values, err)
	}
	if value, valid := values[0].computed.Get(); !valid || value != 3 {
		t.Fatal("computed cell lost its result type", value, valid)
	}
	foreign, err := Derive(group)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SelectResults(foreign, projection); err == nil {
		t.Fatal("equal-shaped foreign result scope accepted")
	}
	if _, err := SelectResults(source, Project1(SelectedField(source, fields.ID), func(value int64) int64 { return value })); err == nil {
		t.Fatal("unselected row field crossed the stage")
	}
	for _, expression := range []DynamicExpression{DynamicF("note"), DynamicColumn(2), DynamicCase(DynamicColumn(1), DynamicWhen(DynamicF("id").Exact(1), 0))} {
		if _, err := source.Expression(expression); err == nil {
			t.Fatal("foreign dynamic reference accepted")
		}
	}
	if _, err := BindExpression(newResultTestQuerySet(backend), DynamicColumn(0)); err == nil {
		t.Fatal("result ordinal bound as a model field")
	}
	if len(backend.plans) != 1 {
		t.Fatal("invalid scope performed I/O")
	}
}

func TestResultQueryOwnsCachedCellsAndOrderedInput(t *testing.T) {
	backend := resultBackendForRows([][]any{{"canonical"}})
	fields := newResultTestFields()
	input, err := SelectQuery(newResultTestQuerySet(backend).Filter(fields.ID.GreaterThan(0)).OrderBy(fields.ID.Desc()),
		Project1(fields.Note, func(value *string) *string { t.Fatal("input builder was evaluated by Derive"); return value }))
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.Limit(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.Filter(fields.ID.LessThan(4)).All(t.Context()); err == nil {
		t.Fatal("filter moved before an existing slice")
	}
	stage, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	column := SelectedField(stage, fields.Note)
	built := 0
	selected, err := SelectResults(stage, Project1(column, func(value *string) *string { built++; return value }))
	if err != nil {
		t.Fatal(err)
	}
	selected = selected.Filter(column.IsNull(false)).OrderBy(column.Asc())
	captured, _, present := selected.Plan().DerivedSource()
	if !present || !captured.Equal(input.Plan()) || len(backend.plans) != 0 {
		t.Fatal("stage did not retain its complete input")
	}
	first, err := selected.All(t.Context())
	if err != nil || len(first) != 1 || *first[0] != "canonical" {
		t.Fatal(first, err)
	}
	*first[0] = "caller mutation"
	second, err := selected.All(t.Context())
	if err != nil || *second[0] != "canonical" || first[0] == second[0] || built != 2 || len(backend.plans) != 1 {
		t.Fatal("caller mutated scanned-cell cache", second, err, built)
	}
	if count, err := selected.Count(t.Context()); err != nil || count != 1 || len(backend.plans) != 1 {
		t.Fatal("warm result count discarded its cache", count, err)
	}
	for _, fresh := range []ResultQuery[ResultOf[resultTestModel], *string]{selected.Fresh(), selected.Filter(), selected.OrderBy(column.Desc()), selected.Distinct()} {
		before := len(backend.plans)
		if _, err := fresh.All(t.Context()); err != nil || len(backend.plans) != before+1 {
			t.Fatal("refinement reused prior evaluation", err)
		}
	}
}

func TestResultQueryFailuresDoNotInvokeBuildersOrFillCache(t *testing.T) {
	failure, cleanup := errors.New("result iteration failure"), errors.New("result cleanup failure")
	for _, stage := range []string{"query with rows", "nil rows", "scan", "iteration and close", "cancel on close"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rows := &resultTestRows{values: [][]any{{"partial"}}}
			var returned db.Rows = rows
			switch stage {
			case "scan":
				rows.scanErr = failure
			case "iteration and close":
				rows.iterationErr, rows.closeErr = failure, cleanup
			case "cancel on close":
				returned = &groupClosingRows{resultTestRows: rows, close: cancel}
			}
			backend := &resultTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call > 0 {
					return &resultTestRows{values: [][]any{{"complete"}}}, nil
				}
				if stage == "query with rows" {
					return returned, failure
				}
				if stage == "nil rows" {
					return nil, nil
				}
				return returned, nil
			}}
			built := 0
			selected, err := SelectQuery(newResultTestQuerySet(backend), Project1(newResultTestFields().Note, func(value *string) *string { built++; return value }))
			if err != nil {
				t.Fatal(err)
			}
			if values, err := selected.All(ctx); err == nil || values != nil || built != 0 {
				t.Fatal("failed cursor invoked a builder or published values", values, err, built)
			} else if stage == "iteration and close" && (!errors.Is(err, failure) || !errors.Is(err, cleanup)) {
				t.Fatal("lost lifecycle failures", err)
			}
			if stage != "nil rows" && rows.closeCalls != 1 {
				t.Fatal("cursor closed incorrectly", rows.closeCalls)
			}
			values, err := selected.All(t.Context())
			if err != nil || len(values) != 1 || *values[0] != "complete" || built != 1 || len(backend.plans) != 2 {
				t.Fatal("failed result was cached", values, err, built)
			}
		})
	}
}

func TestResultCompositionPreservesWideTypesAndBounds(t *testing.T) {
	fields := newResultTestFields()
	left := Project4(fields.ID, fields.Title, fields.Note, fields.Published, func(id int64, title string, note *string, published bool) resultProjection4 {
		return resultProjection4{ID: id, Title: title, Note: note, Published: published}
	})
	plus, minus := Add(F(fields.ID), int64(2)), Subtract(F(fields.ID), int64(2))
	right := Project2(plus, minus, func(a, b Optional[int64]) [2]Optional[int64] { return [2]Optional[int64]{a, b} })
	type six struct {
		model   resultProjection4
		numeric [2]Optional[int64]
	}
	combined := CombineProjections(left, right, func(a resultProjection4, b [2]Optional[int64]) six { return six{a, b} })
	backend := resultBackendForRows([][]any{{int64(5), "wide", "owned", true, int64(7), int64(3)}})
	values, err := SelectInto(t.Context(), newResultTestQuerySet(backend), combined)
	if err != nil || len(values) != 1 || values[0].model.ID != 5 || *values[0].model.Note != "owned" {
		t.Fatal(values, err)
	}
	if a, _ := values[0].numeric[0].Get(); a != 7 {
		t.Fatal(values)
	}
	if b, _ := values[0].numeric[1].Get(); b != 3 {
		t.Fatal(values)
	}
	aggregate := CombineAggregates(Aggregate2(CountRows[resultTestModel](), Count(fields.Note), func(a, b int64) [2]int64 { return [2]int64{a, b} }),
		Aggregate1(Max(fields.ID), func(value Optional[int64]) Optional[int64] { return value }), func(counts [2]int64, maximum Optional[int64]) []int64 {
			value, _ := maximum.Get()
			return []int64{counts[0], counts[1], value}
		})
	aggregateBackend := resultBackendForRows([][]any{{int64(6), int64(4), int64(9)}})
	value, err := AggregateInto(t.Context(), newResultTestQuerySet(aggregateBackend), aggregate)
	if err != nil || !reflect.DeepEqual(value, []int64{6, 4, 9}) {
		t.Fatal(value, err)
	}
	duplicate := CombineProjections(Project1(fields.ID, func(n int64) int64 { return n }), Project1(fields.ID, func(n int64) int64 { return n }), func(a, b int64) int64 { return a + b })
	if _, err := SelectQuery(newResultTestQuerySet(backend), duplicate); err == nil {
		t.Fatal("duplicate composed selection accepted")
	}
	if _, err := SelectQuery(newResultTestQuerySet(backend), CombineProjections(left, right, (func(resultProjection4, [2]Optional[int64]) six)(nil))); err == nil {
		t.Fatal("nil composition builder accepted")
	}
	wide := Projection[resultTestModel, int64]{expressions: make([]query.ResultExpression, query.MaxProjectionExpressions), newDecoder: func() resultDecoder[int64] {
		t.Fatal("wide projection built before validation")
		return resultDecoder[int64]{}
	}}
	if _, err := SelectQuery(newResultTestQuerySet(backend), CombineProjections(wide, Project1(fields.ID, func(n int64) int64 { return n }), func(a, b int64) int64 { return a + b })); err == nil {
		t.Fatal("composition exceeded its selection budget")
	}
}

func TestResultCountAndReaggregationRetainSlicedInput(t *testing.T) {
	fields := newResultTestFields()
	backend := &resultTestBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
		if plan.ResultShape().IsCountAll() {
			return &resultTestRows{values: [][]any{{int64(2)}}}, nil
		}
		return &resultTestRows{values: [][]any{{int64(6)}}}, nil
	}}
	input, err := SelectQuery(newResultTestQuerySet(backend).OrderBy(fields.ID.Desc()), Project1(fields.ID, func(n int64) int64 { t.Fatal("input builder ran"); return n }))
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.Limit(2)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := input.Count(t.Context()); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	countInput, _, present := backend.plans[0].DerivedSource()
	if !present || !countInput.Equal(input.Plan()) {
		t.Fatal("cold count moved input slice or ordering")
	}
	stage, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	value := SelectedField(stage, fields.ID)
	sum := Sum(Numeric(value))
	aggregate, err := AggregateResults(stage, Aggregate1(sum, func(n Optional[int64]) Optional[int64] { return n }))
	if err != nil {
		t.Fatal(err)
	}
	total, err := aggregate.One(t.Context())
	if got, valid := total.Get(); err != nil || !valid || got != 6 {
		t.Fatal(total, err)
	}
	if _, err := aggregate.Limit(1); err == nil {
		t.Fatal("aggregate output slice moved before aggregate")
	}
	next, err := Derive(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	selected := SelectedAggregate(next, sum)
	operand := OptionalOperand(selected)
	if operand.expression.ResultKind() != query.FieldInteger || !operand.expression.Nullable() {
		t.Fatal("optional result changed NULL/domain")
	}
	if _, err := SelectResults(next, Project1(Add(operand, int64(1)), func(n Optional[int64]) Optional[int64] { return n })); err != nil {
		t.Fatal(err)
	}
}

func TestResultPageChecksEnvelopeWithoutWarmingAll(t *testing.T) {
	for _, test := range []struct {
		name   string
		values [][]any
		valid  bool
	}{
		{"page", [][]any{{int64(2), int64(1), "a"}, {int64(2), int64(1), "b"}}, true},
		{"empty", [][]any{{int64(0), nil, nil}}, true},
		{"missing_count", nil, false},
		{"inconsistent_total", [][]any{{int64(2), int64(1), "a"}, {int64(3), int64(1), "b"}}, false},
		{"missing_row", [][]any{{int64(2), int64(1), "a"}}, false},
		{"sentinel_with_cell", [][]any{{int64(0), nil, "a"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := &resultTestRows{values: test.values}
			backend := &resultTestBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
				if call > 0 {
					return &resultTestRows{values: [][]any{{"all"}}}, nil
				}
				if plan.ResultShape().Kind() != query.ResultPage {
					t.Error("not a result page envelope")
				}
				return rows, nil
			}}
			built := 0
			selected, err := SelectQuery(newResultTestQuerySet(backend), Project1(newResultTestFields().Note, func(value *string) *string { built++; return value }))
			if err != nil {
				t.Fatal(err)
			}
			page, err := selected.Page(t.Context(), 2, 0)
			if test.valid {
				if err != nil || page.Rows == nil || int64(len(page.Rows)) != page.Total || built != len(page.Rows) {
					t.Fatal(page, err, built)
				}
			} else if err == nil || page.Total != 0 || page.Rows != nil || built != 0 {
				t.Fatal("invalid page published", page, err, built)
			}
			if rows.closeCalls != 1 {
				t.Fatal("page close count", rows.closeCalls)
			}
			values, err := selected.All(t.Context())
			if err != nil || len(values) != 1 || *values[0] != "all" || len(backend.plans) != 2 {
				t.Fatal("page filled All cache", values, err)
			}
		})
	}
}

func TestResultQueryRetainsSessionAcrossStagesAndCachedPublication(t *testing.T) {
	expired := errors.New("result session expired")
	for _, terminal := range []string{"all", "count", "page", "one"} {
		t.Run(terminal, func(t *testing.T) {
			backend := &getSessionBackend{expired: expired}
			backend.active.Store(true)
			backend.rows = &resultTestRows{values: [][]any{{int64(1)}}}
			fields := newResultTestFields()
			input, err := SelectQuery(newResultTestQuerySet(backend), Project1(fields.ID, func(value int64) int64 { return value }))
			if err != nil {
				t.Fatal(err)
			}
			stage, err := Derive(input)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := SelectResults(stage, Project1(SelectedField(stage, fields.ID), func(value int64) int64 { return value }))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := selected.All(t.Context()); err != nil {
				t.Fatal(err)
			}
			backend.active.Store(false)
			var failure error
			switch terminal {
			case "all":
				_, failure = selected.All(t.Context())
			case "count":
				_, failure = selected.Count(t.Context())
			case "page":
				_, failure = selected.Page(t.Context(), 1, 0)
			case "one":
				aggregate, err := AggregateResults(stage, Aggregate1(CountRows[ResultOf[resultTestModel]](), func(n int64) int64 { return n }))
				if err != nil {
					t.Fatal(err)
				}
				_, failure = aggregate.One(t.Context())
			}
			if !errors.Is(failure, expired) {
				t.Fatal("result outlived its source session", failure)
			}
		})
	}
}
