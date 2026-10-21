package orm

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type namedTestDescriptor struct {
	resultTestDescriptor
	mode string
}

func (namedTestDescriptor) PrimaryKey(value resultTestModel) (query.Value, bool) {
	return query.Integer(value.ID), true
}

func (descriptor namedTestDescriptor) NewProjectionScan() ProjectionScan[resultTestModel] {
	if descriptor.mode == "nil" {
		return nil
	}
	if descriptor.mode == "typed_nil" {
		return (*namedTestScan)(nil)
	}
	return &namedTestScan{mode: descriptor.mode}
}

type namedTestScan struct {
	id        sql.NullInt64
	title     sql.NullString
	note      sql.NullString
	published sql.NullBool
	mode      string
}

func (scan *namedTestScan) Destinations() []any {
	values := []any{&scan.id, &scan.title, &scan.note, &scan.published}
	if scan.mode == "short" {
		return values[:3]
	}
	if scan.mode == "nil_destination" {
		values[1] = (*sql.NullString)(nil)
	}
	return values
}

func (scan *namedTestScan) Decode() (resultTestModel, query.Value, ProjectionPresence) {
	value := resultTestModel{ID: scan.id.Int64, Title: scan.title.String, Published: scan.published.Bool}
	if scan.note.Valid {
		note := scan.note.String
		value.Note = &note
	}
	if !scan.id.Valid || !scan.title.Valid || !scan.published.Valid || scan.mode == "invalid" {
		return value, query.Value{}, ProjectionInvalid
	}
	if scan.mode == "absent" {
		return value, query.Null(), ProjectionAbsent
	}
	key := query.Integer(value.ID)
	if scan.mode == "foreign_key" {
		key = query.Integer(value.ID + 1)
	}
	return value, key, ProjectionPresent
}

func namedTestQuery(backend db.Queryer) QuerySet[resultTestModel] {
	return NewManager[resultTestModel](namedTestDescriptor{}).Using(backend)
}

func TestNamedQueryPreservesModelCodecsAndCachedOwnership(t *testing.T) {
	bytes := []byte{1, 2, 3}
	rows := &resultTestRows{values: [][]any{{int64(5), "five", "memo", true, int64(8), bytes}}}
	rows.onNext = func(n int) {
		if n > 1 {
			bytes[0] = 99
		}
	}
	backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
	fields := newResultTestFields()
	first, adjusted, err := Annotate(namedTestQuery(backend), "adjusted", Add(F(fields.ID), int64(3)))
	if err != nil {
		t.Fatal(err)
	}
	named, err := AnnotateDynamic(first, Named("blob", DynamicValue(binaryvalue.Value{Data: string([]byte{1, 2, 3})})))
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.plans) != 0 {
		t.Fatal("annotation construction performed I/O")
	}
	values, err := named.All(t.Context())
	if err != nil || len(values) != 1 || values[0].Value.ID != 5 || *values[0].Value.Note != "memo" {
		t.Fatal(values, err)
	}
	value, err := adjusted.Get(values[0])
	if n, valid := value.Get(); err != nil || !valid || n != 8 {
		t.Fatal("typed named value lost Optional codec", value, err)
	}
	dynamic, err := named.Column("adjusted")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := dynamic.Get(values[0])
	if n, ok := raw.Integer(); err != nil || !ok || n != 8 {
		t.Fatal("dynamic named value differs from typed cell", raw, err)
	}
	blob, err := named.Column("blob")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = blob.Get(values[0])
	owned, ok := raw.Binary()
	if err != nil || !ok || !reflect.DeepEqual(owned.Bytes(), []byte{1, 2, 3}) {
		t.Fatal("named binary retained cursor storage", raw, err)
	}
	owned.Data = "caller"
	*values[0].Value.Note = "caller mutation"
	names := values[0].Names()
	names[0] = "caller"
	cached, err := named.All(t.Context())
	if err != nil || len(backend.plans) != 1 || *cached[0].Value.Note != "memo" || !reflect.DeepEqual(cached[0].Names(), []string{"adjusted", "blob"}) {
		t.Fatal("named model cache leaked caller state", cached, err)
	}
	raw, err = blob.Get(cached[0])
	if data, ok := raw.Binary(); err != nil || !ok || data.Bytes()[0] != 1 {
		t.Fatal("named binary retrieval leaked caller state", raw, err)
	}
	_, foreign, err := Annotate(namedTestQuery(backend), "adjusted", Add(F(fields.ID), int64(3)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := foreign.Get(cached[0]); err == nil {
		t.Fatal("equal-named foreign column read a cached cell")
	}
}

func TestNamedQueryProjectionAndExplicitStageKeepAuthority(t *testing.T) {
	backend := resultBackendForRows([][]any{{int64(5), int64(8)}})
	fields := newResultTestFields()
	source, err := SelectQuery(newResultTestQuerySet(backend), Project1(fields.ID, func(n int64) []int64 { return []int64{n} }))
	if err != nil {
		t.Fatal(err)
	}
	named, column, err := Annotate(source, "adjusted", Add(F(fields.ID), int64(3)))
	if err != nil {
		t.Fatal(err)
	}
	typed, err := SelectNamed(named, Project2(fields.ID, column, func(id int64, n Optional[int64]) int64 { v, _ := n.Get(); return id + v }))
	if err != nil {
		t.Fatal(err)
	}
	dynamic, err := named.ProjectExpressions(DynamicF("id"), DynamicF("adjusted"))
	if err != nil {
		t.Fatal(err)
	}
	dynamicRows, err := SelectNamed(named, dynamic)
	if err != nil || !typed.Plan().Equal(dynamicRows.Plan()) {
		t.Fatal("typed/dynamic named selection has different AST", err)
	}
	again, err := Names(typed)
	if err != nil || len(again.Plan().ResultShape().Expressions()) != 2 {
		t.Fatal("selected named column was duplicated", err)
	}
	values, err := again.All(t.Context())
	if err != nil || len(values) != 1 || values[0].Value != 13 {
		t.Fatal(values, err)
	}
	if n, err := column.Get(values[0]); err != nil || !n.Valid() {
		t.Fatal(n, err)
	}
	baseOnly, err := SelectNamed(named, Project1(fields.ID, func(n int64) int64 { return n }))
	if err != nil {
		t.Fatal(err)
	}
	baseNames, err := Names(baseOnly)
	if err != nil || len(baseNames.Plan().ResultShape().Expressions()) != 1 {
		t.Fatal("unselected definition became published", err)
	}
	stage, err := Derive(typed)
	if err != nil {
		t.Fatal(err)
	}
	selected := SelectedField(stage, column)
	next, current, err := Annotate(stage, "next", Add(OptionalOperand(selected), int64(2)))
	if err != nil {
		t.Fatal(err)
	}
	if err := next.input.plan.ValidateScalar(column.column.value.expression); err == nil {
		t.Fatal("model-stage annotation crossed an explicit result stage")
	}
	if _, err := next.Expression(DynamicF("adjusted")); err == nil {
		t.Fatal("unbound original name crossed an explicit result stage")
	}
	if _, err := next.Expression(DynamicF(current.Name())); err != nil {
		t.Fatal("current result-stage name failed", err)
	}
	if err := named.input.plan.ValidateScalar(current.column.value.expression); err == nil {
		t.Fatal("unexpectedly accepted result-stage operand")
	}
}

func TestNamedDynamicBatchRebindingAliasesAndPreIOErrors(t *testing.T) {
	backend := resultBackendForRows(nil)
	base := namedTestQuery(backend)
	named, err := AnnotateDynamic(base, Named("first", DynamicF("id").Add(1)), Named("dependent", DynamicF("first").Multiply(2)), Named("first", DynamicValue(100)))
	if err != nil {
		t.Fatal(err)
	}
	bindings := named.Plan().AnnotationBindings()
	if len(bindings) != 3 {
		t.Fatal("redefinition lost definition history")
	}
	before := named.Plan()
	for _, batch := range [][]NamedExpression{
		{Named("future", DynamicF("later")), Named("later", DynamicValue(1))},
		{Named("cycle", DynamicF("cycle").Add(1))},
		{Named("valid", DynamicValue(1)), Named("bad", DynamicF("missing"))},
		{Named("id", DynamicValue(1))},
		{Named("bad__name", DynamicValue(1))},
		{Named("sum", DynamicF("id")).Sum().Sum()},
		{Named("scalar", DynamicF("id")).Distinct()},
		{NamedCount("count").Distinct()},
		{Named("scalar", DynamicF("id")).Filter(DynamicF("id").Exact(1))},
	} {
		if _, err := AnnotateDynamic(named, batch...); err == nil {
			t.Fatal("invalid named batch accepted", batch)
		}
		if !before.Equal(named.Plan()) {
			t.Fatal("failed batch changed its input")
		}
	}
	hidden, err := AliasDynamic(named, Named("hidden", DynamicF("id").Add(7)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hidden.ProjectExpressions(DynamicF("hidden")); err == nil {
		t.Fatal("hidden alias was directly selectable")
	}
	promoted, err := AnnotateDynamic(hidden, Named("hidden", DynamicF("hidden")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := promoted.ProjectExpressions(DynamicF("hidden")); err != nil {
		t.Fatal("explicit alias promotion failed", err)
	}
	aggregated, err := AnnotateDynamic(base, Named("sum", DynamicF("id")).Sum().Filter(DynamicF("id").GreaterThan(0)), Named("double", DynamicF("sum").Multiply(2)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AnnotateDynamic(aggregated, Named("nested", DynamicF("sum")).Sum()); err == nil {
		t.Fatal("nested aggregate accepted without an explicit stage")
	}
	if _, err := AnnotateDynamic(aggregated, NamedCount("filtered").Filter(DynamicF("sum").GreaterThan(0))); err == nil {
		t.Fatal("aggregate FILTER consumed a same-stage aggregate")
	}
	if len(backend.plans) != 0 {
		t.Fatal("definition validation performed I/O")
	}
}

func TestNamedModelDescriptorFailuresDoNotPublishOrCache(t *testing.T) {
	for _, mode := range []string{"nil", "typed_nil", "short", "nil_destination", "invalid", "absent", "foreign_key"} {
		t.Run(mode, func(t *testing.T) {
			rows := &resultTestRows{values: [][]any{{int64(1), "one", nil, true}}}
			backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
			source := NewManager[resultTestModel](namedTestDescriptor{mode: mode}).Using(backend)
			named, err := Names(source)
			construction := mode == "nil" || mode == "typed_nil" || mode == "short" || mode == "nil_destination"
			if construction {
				if err == nil || len(backend.plans) != 0 {
					t.Fatal("invalid projection protocol reached I/O", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			values, err := named.All(t.Context())
			if err == nil || values != nil || rows.closeCalls != 1 || named.rows.evaluation == nil {
				t.Fatal("invalid model projection published values", values, err)
			}
			if _, cached := named.rows.evaluation.cachedValues(); cached {
				t.Fatal("invalid model projection warmed cache")
			}
		})
	}
	backend := resultBackendForRows(nil)
	if _, err := Names(newResultTestQuerySet(backend)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("descriptor without projection capability was accepted", err)
	}
}

func TestNamedQueryFailuresKeepBuildersAndCacheCold(t *testing.T) {
	failed, cleanup := errors.New("named cursor failure"), errors.New("named close failure")
	for _, mode := range []string{"scan", "invalid_cell", "iteration_and_close", "cancel_on_close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rows := &resultTestRows{values: [][]any{{int64(1), int64(2)}}}
			var returned db.Rows = rows
			switch mode {
			case "scan":
				rows.scanErr = failed
			case "invalid_cell":
				rows.values[0][1] = "invalid numeric value"
			case "iteration_and_close":
				rows.iterationErr, rows.closeErr = failed, cleanup
			case "cancel_on_close":
				returned = &groupClosingRows{resultTestRows: rows, close: cancel}
			}
			backend := &resultTestBackend{query: func(n int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if n > 0 {
					return &resultTestRows{values: [][]any{{int64(3), int64(4)}}}, nil
				}
				return returned, nil
			}}
			built := 0
			fields := newResultTestFields()
			base, err := SelectQuery(newResultTestQuerySet(backend), Project1(fields.ID, func(n int64) []int64 { built++; return []int64{n} }))
			if err != nil {
				t.Fatal(err)
			}
			named, column, err := Annotate(base, "plus", Add(F(fields.ID), int64(1)))
			if err != nil {
				t.Fatal(err)
			}
			if values, err := named.All(ctx); err == nil || values != nil || built != 0 {
				t.Fatal("failed named cursor invoked builder or published values", values, err, built)
			} else if mode == "iteration_and_close" && (!errors.Is(err, failed) || !errors.Is(err, cleanup)) {
				t.Fatal("named cursor lost joined errors", err)
			}
			if rows.closeCalls != 1 {
				t.Fatal("named cursor was not closed once", rows.closeCalls)
			}
			values, err := named.All(t.Context())
			if err != nil || len(values) != 1 || values[0].Value[0] != 3 || built != 1 || len(backend.plans) != 2 {
				t.Fatal("failed named read was cached", values, err, built)
			}
			value, err := column.Get(values[0])
			if n, valid := value.Get(); err != nil || !valid || n != 4 {
				t.Fatal("retry named value failed", value, err)
			}
			values[0].Value[0] = 99
			cached, err := named.All(t.Context())
			if err != nil || cached[0].Value[0] != 3 || built != 2 || len(backend.plans) != 2 {
				t.Fatal("DTO cache shared caller mutation", cached, err)
			}
		})
	}
}

func TestNamedQueryRetainsSessionAndRejectsSlicedRefinement(t *testing.T) {
	expired := errors.New("named session expired")
	backend := &getSessionBackend{expired: expired}
	backend.active.Store(true)
	backend.rows = &resultTestRows{values: [][]any{{int64(1), "one", nil, true, int64(2)}}}
	fields := newResultTestFields()
	named, _, err := Annotate(namedTestQuery(backend), "plus", Add(F(fields.ID), int64(1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := named.All(t.Context()); err != nil {
		t.Fatal(err)
	}
	backend.active.Store(false)
	if _, err := named.All(t.Context()); !errors.Is(err, expired) {
		t.Fatal("cached model escaped session", err)
	}
	if _, err := named.Count(t.Context()); !errors.Is(err, expired) {
		t.Fatal("cached count escaped session", err)
	}
	if _, err := named.Exists(t.Context()); !errors.Is(err, expired) {
		t.Fatal("cached existence escaped session", err)
	}
	if _, err := named.Page(t.Context(), 1, 0); !errors.Is(err, expired) {
		t.Fatal("page escaped session", err)
	}
	limited, err := named.Limit(1)
	if err != nil {
		t.Fatal(err)
	}
	if limited.Filter(fields.ID.Exact(1)).ConfigurationError() == nil || limited.OrderBy(fields.ID.Desc()).ConfigurationError() == nil || limited.Distinct().ConfigurationError() == nil {
		t.Fatal("named slice allowed an ambiguous refinement")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := named.All(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cached named row ignored canceled context", err)
	}
}
