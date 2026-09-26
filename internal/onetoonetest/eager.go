package onetoonetest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/conformance/onetoonefixture/project"
	"github.com/progresshans/godj/conformance/onetoonefixture/reports"
	"github.com/progresshans/godj/conformance/onetoonefixture/tickets"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type eagerReference struct {
	Name        string           `json:"name"`
	Rows        []map[string]any `json:"rows"`
	LoadSelects uint64           `json:"load_selects"`
	LeftJoins   int              `json:"left_joins"`
	InnerJoins  int              `json:"inner_joins"`
}
type eagerNode[S any] struct {
	selection orm.RelatedSelection[S]
	read      func(*testing.T, context.Context, *orm.RelatedSelected[S], string, map[string]any)
}

func eagerEdge[S, T any](t *testing.T, name string, selection orm.RelatedSelect[S, T], key func(T) int64, fields func(T, bool) map[string]any, children ...eagerNode[T]) eagerNode[S] {
	t.Helper()
	for _, child := range children {
		selection = selection.WithChildren(child.selection)
	}
	return eagerNode[S]{selection: selection, read: func(t *testing.T, ctx context.Context, graph *orm.RelatedSelected[S], prefix string, row map[string]any) {
		path := prefix + name
		var value T
		var present bool
		var nested *orm.RelatedSelected[T]
		if graph != nil {
			handle, err := selection.Related(graph)
			if err != nil {
				t.Fatal(err)
			}
			value, present, err = handle.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(children) > 0 && present {
				var found bool
				nested, found, err = handle.SelectedGraph(ctx)
				if err != nil || !found {
					t.Fatal("selected descendant graph missing", found, err)
				}
			}
		}
		row[path] = nil
		if present {
			row[path] = key(value)
		}
		if fields != nil {
			for name, value := range fields(value, present) {
				row[path+"_"+name] = value
			}
		}
		for _, child := range children {
			child.read(t, ctx, nested, path+"__", row)
		}
	}}
}

// RunReverseEager compares generated typed selection trees with independent
// Django row/JOIN observations. Go cache ownership requires zero warm I/O even
// for reciprocal paths whose Django descriptors replace a populated instance.
func RunReverseEager(t *testing.T, backend ProductBackend, dialect string, compile func(query.Plan) (string, error), queryCount func() uint64) {
	t.Helper()
	ctx := t.Context()
	seedLookupProduct(t, backend)
	binding, err := project.Bind()
	if err != nil {
		t.Fatal(err)
	}
	forward, err := project.BindObjectsIn(binding)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindReverseObjectsIn(binding)
	if err != nil {
		t.Fatal(err)
	}
	predicates, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	r := reverse.TicketsTicket
	reviewFields := func(v reports.Review, present bool) map[string]any {
		result := map[string]any{"score": nil, "approved": nil}
		if present && v.Score != nil {
			result["score"] = *v.Score
		}
		if present && v.Approved != nil {
			result["approved"] = *v.Approved
		}
		return result
	}
	review := func(children ...eagerNode[reports.Review]) eagerNode[tickets.Ticket] {
		return eagerEdge(t, "review", r.SelectReview(), func(v reports.Review) int64 { return v.ID }, reviewFields, children...)
	}
	report := func(children ...eagerNode[reports.Report]) eagerNode[tickets.Ticket] {
		return eagerEdge(t, "report", r.SelectReport(), func(v reports.Report) int64 { return v.ID }, nil, children...)
	}
	optional := func(children ...eagerNode[reports.OptionalReport]) eagerNode[tickets.Ticket] {
		return eagerEdge(t, "optional_report", r.SelectOptionalReport(), func(v reports.OptionalReport) int64 { return v.ID }, nil, children...)
	}
	reportOwner := func(children ...eagerNode[tickets.Ticket]) eagerNode[reports.Report] {
		return eagerEdge(t, "ticket", forward.ReportsReport.SelectTicket(), func(v tickets.Ticket) int64 { return v.ID }, nil, children...)
	}
	optionalOwner := func(children ...eagerNode[tickets.Ticket]) eagerNode[reports.OptionalReport] {
		return eagerEdge(t, "ticket", forward.ReportsOptionalReport.SelectTicket(), func(v tickets.Ticket) int64 { return v.ID }, nil, children...)
	}
	reviewOwner := func(children ...eagerNode[tickets.Ticket]) eagerNode[reports.Review] {
		return eagerEdge(t, "ticket", forward.ReportsReview.SelectTicket(), func(v tickets.Ticket) int64 { return v.ID }, nil, children...)
	}
	cases := []struct {
		name    string
		nodes   []eagerNode[tickets.Ticket]
		filters []orm.Predicate[tickets.Ticket]
	}{
		{name: "report", nodes: []eagerNode[tickets.Ticket]{report()}},
		{name: "optional", nodes: []eagerNode[tickets.Ticket]{optional()}},
		{name: "review", nodes: []eagerNode[tickets.Ticket]{review()}},
		{name: "multiple", nodes: []eagerNode[tickets.Ticket]{review(), report(), optional()}},
		{name: "present", nodes: []eagerNode[tickets.Ticket]{report()}, filters: []orm.Predicate[tickets.Ticket]{predicates.TicketsTicket.Report.IsNull(false)}},
		{name: "absent", nodes: []eagerNode[tickets.Ticket]{report()}, filters: []orm.Predicate[tickets.Ticket]{predicates.TicketsTicket.Report.IsNull(true)}},
		{name: "or_absent", nodes: []eagerNode[tickets.Ticket]{report(), review()}, filters: []orm.Predicate[tickets.Ticket]{orm.Or(predicates.TicketsTicket.Report.Note.Exact("original"), predicates.TicketsTicket.Review.IsNull(true))}},
		{name: "reverse_forward", nodes: []eagerNode[tickets.Ticket]{report(reportOwner())}},
		{name: "reverse_forward_reverse", nodes: []eagerNode[tickets.Ticket]{report(reportOwner(review()))}},
		{name: "optional_forward_reverse", nodes: []eagerNode[tickets.Ticket]{optional(optionalOwner(report()))}},
		{name: "review_forward_reverse", nodes: []eagerNode[tickets.Ticket]{review(reviewOwner(report()))}},
		{name: "repeated_declaration", nodes: []eagerNode[tickets.Ticket]{report(reportOwner(report()))}},
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "one-to-one-django61-"+dialect+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Eager []eagerReference `json:"eager"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	expected := map[string]eagerReference{}
	for _, item := range reference.Eager {
		if _, exists := expected[item.Name]; exists {
			t.Fatal("duplicate reference", item.Name)
		}
		expected[item.Name] = item
	}
	if len(expected) != len(cases) {
		t.Fatal("eager reference inventory mismatch", len(expected), len(cases))
	}
	source := tickets.TicketObjects.Using(backend).OrderBy(tickets.TicketFields.ID.Asc())
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			want, found := expected[test.name]
			if !found {
				t.Fatal("reference case absent")
			}
			inputs := make([]orm.RelatedSelection[tickets.Ticket], len(test.nodes))
			for i, node := range test.nodes {
				inputs[i] = node.selection
			}
			eager := orm.SelectRelated(source.Filter(test.filters...), inputs...)
			if err := eager.ConfigurationError(); err != nil {
				t.Fatal(err)
			}
			statement, err := compile(eager.Plan())
			if err != nil {
				t.Fatal(err)
			}
			if left, inner := strings.Count(statement, " LEFT OUTER JOIN "), strings.Count(statement, " INNER JOIN "); left != want.LeftJoins || inner != want.InnerJoins {
				t.Fatalf("joins %d/%d, want %d/%d: %s", left, inner, want.LeftJoins, want.InnerJoins, statement)
			}
			before := queryCount()
			count, err := eager.Count(ctx)
			if err != nil || count != int64(len(want.Rows)) || queryCount()-before != 1 {
				t.Fatal("cold count", count, err, queryCount()-before)
			}
			before = queryCount()
			loaded, err := eager.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if queryCount()-before != want.LoadSelects {
				t.Fatal("initial SELECT count", queryCount()-before, want.LoadSelects)
			}
			before = queryCount()
			actual := make([]map[string]any, 0, len(loaded))
			for _, graph := range loaded {
				owner, err := graph.Source()
				if err != nil {
					t.Fatal(err)
				}
				row := map[string]any{"root": owner.ID}
				for _, node := range test.nodes {
					node.read(t, ctx, graph, "", row)
				}
				actual = append(actual, row)
			}
			got, _ := json.Marshal(actual)
			wantJSON, _ := json.Marshal(want.Rows)
			if string(got) != string(wantJSON) {
				t.Fatalf("rows differ\ngot %s\nwant %s", got, wantJSON)
			}
			if _, err := eager.All(ctx); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := eager.First(ctx); err != nil || ok != (len(loaded) > 0) {
				t.Fatal("warm first", ok, err)
			}
			if count, err := eager.Count(ctx); err != nil || count != int64(len(loaded)) {
				t.Fatal("warm count", count, err)
			}
			if queryCount() != before {
				t.Fatal("warm selected graph performed I/O")
			}
		})
	}
	t.Run("factory_cache_and_fresh", func(t *testing.T) {
		loaded, err := orm.SelectRelated(source, r.SelectReport(), r.SelectReview()).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		before := queryCount()
		object, err := r.FromSelected(loaded[0])
		if err != nil {
			t.Fatal(err)
		}
		handle, err := object.Report()
		if err != nil {
			t.Fatal(err)
		}
		original, present, err := handle.Get(ctx)
		if err != nil || !present {
			t.Fatal(present, err)
		}
		missingObject, err := r.FromSelected(loaded[3])
		if err != nil {
			t.Fatal(err)
		}
		missing, err := missingObject.Report()
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := missing.Get(ctx); err != nil || present {
			t.Fatal("missing warm child", present, err)
		}
		if queryCount() != before {
			t.Fatal("generated bridge discarded selected cache")
		}
		fourth, _ := loaded[3].Source()
		inserted, err := reports.ReportObjects.Create(ctx, backend, reports.NewReportCreate(fourth.ID, "new"))
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := missing.Get(ctx); err != nil || present {
			t.Fatal("warm missing cache changed externally")
		}
		fresh, err := missing.Fresh()
		if err != nil {
			t.Fatal(err)
		}
		value, present, err := fresh.Get(ctx)
		if err != nil || !present || value.ID != inserted.ID {
			t.Fatal("missing reverse Fresh did not reload by owner", value, present, err)
		}
		third, _ := loaded[2].Source()
		if _, err := reports.ReportObjects.Update(ctx, backend, original, (reports.ReportPatch{}).WithTicketID(third.ID)); err != nil {
			t.Fatal(err)
		}
		replacement, err := reports.ReportObjects.Create(ctx, backend, reports.NewReportCreate(original.TicketID, "replacement"))
		if err != nil {
			t.Fatal(err)
		}
		fresh, err = handle.Fresh()
		if err != nil {
			t.Fatal(err)
		}
		value, present, err = fresh.Get(ctx)
		if err != nil || !present || value.ID != replacement.ID {
			t.Fatal("present reverse Fresh retained old child PK", value, present, err)
		}
		// Every materialization and every Get owns nested pointer values.
		reviewHandle, err := r.SelectReview().Related(loaded[0])
		if err != nil {
			t.Fatal(err)
		}
		reviewValue, _, err := reviewHandle.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		*reviewValue.Score = 999
		again, _, err := reviewHandle.Get(ctx)
		if err != nil || again.Score == nil || *again.Score != 5 {
			t.Fatal("selected pointer field escaped ownership", again, err)
		}
		copied := *loaded[0]
		if _, err := copied.Source(); err == nil {
			t.Fatal("copied selected pointer accepted")
		}
	})
	t.Run("concurrent_all_and_configuration", func(t *testing.T) {
		reads := &countedQuery{Queryer: backend}
		eager := orm.SelectRelated(tickets.TicketObjects.Using(reads), r.SelectReport())
		var wg sync.WaitGroup
		failures := make(chan error, 16)
		results := make(chan *orm.RelatedSelected[tickets.Ticket], 16)
		for range 16 {
			wg.Go(func() {
				values, err := eager.All(ctx)
				if err != nil {
					failures <- err
					return
				}
				if len(values) != 4 {
					failures <- errors.New("missing eager rows")
					return
				}
				results <- values[0]
			})
		}
		wg.Wait()
		close(failures)
		close(results)
		for err := range failures {
			t.Error(err)
		}
		if reads.calls.Load() != 1 {
			t.Fatal("concurrent eager query evaluated repeatedly", reads.calls.Load())
		}
		unique := map[*orm.RelatedSelected[tickets.Ticket]]bool{}
		for result := range results {
			if unique[result] {
				t.Fatal("returned selected cache shared")
			}
			unique[result] = true
		}
		owner, err := orm.BindModel(binding, ir.ModelIdentity{AppLabel: "ototickets", ModelName: "ticket"}, tickets.TicketDescriptor{})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"links", "report__ticket", "missing"} {
			if _, err := orm.ResolveRelatedSelectPath(owner, path); err == nil {
				t.Fatal("invalid direct selection accepted", path)
			}
		}
		other, err := project.BindObjects()
		if err != nil {
			t.Fatal(err)
		}
		invalid := orm.SelectRelated(tickets.TicketObjects.Using(reads), r.SelectReport(other.ReportsReport.SelectTicket()))
		before := reads.calls.Load()
		if values, err := invalid.All(ctx); err == nil || values != nil || reads.calls.Load() != before {
			t.Fatal("cross-binding descendant performed I/O", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if values, err := eager.All(canceled); !errors.Is(err, context.Canceled) || values != nil || reads.calls.Load() != before {
			t.Fatal("canceled cache access", err)
		}
		nativeBefore := queryCount()
		zero, _ := tickets.TicketObjects.Using(reads).Limit(0)
		empty, err := orm.SelectRelated(zero, r.SelectReport()).All(ctx)
		if err != nil || len(empty) != 0 || reads.calls.Load() != before+1 || queryCount() != nativeBefore {
			t.Fatal("empty selection validation", empty, err)
		}
	})
	testReverseEagerFailures(t, r.SelectReport(), r.SelectReport(forward.ReportsReport.SelectTicket()))
}

func testReverseEagerFailures(t *testing.T, selection, nested orm.RelatedSelect[tickets.Ticket, reports.Report]) {
	t.Helper()
	ctx := t.Context()
	n := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	s := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	valid := []any{n(1), s("first"), n(7), n(1), s("child")}
	absent := []any{n(1), s("first"), sql.NullInt64{}, sql.NullInt64{}, sql.NullString{}}
	closeFailure := errors.New("eager rows close failure")
	cases := []struct {
		name     string
		rows     [][]any
		closeErr error
		code     string
	}{
		{name: "wrong_owner", rows: [][]any{{n(1), s("first"), n(7), n(99), s("child")}}, code: query.CodeRelatedObjectProjection},
		{name: "partial_child", rows: [][]any{{n(1), s("first"), sql.NullInt64{}, n(1), s("child")}}, code: query.CodeInvalidPlan},
		{name: "duplicate_children", rows: [][]any{valid, {n(1), s("first"), n(8), n(1), s("other")}}, code: query.CodeRelatedObjectCardinality},
		{name: "presence_conflict", rows: [][]any{valid, absent}, code: query.CodeRelatedObjectCardinality},
		{name: "late_invalid_row", rows: [][]any{valid, {n(2), s("second"), n(8), n(99), s("other")}}, code: query.CodeRelatedObjectProjection},
		{name: "rows_close", rows: [][]any{valid}, closeErr: closeFailure},
		{name: "scan_shape", rows: [][]any{{n(1)}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rows := &productRows{index: -1, values: test.rows, closeErr: test.closeErr}
			backend := &suppliedQuery{rows: rows}
			eager := orm.SelectRelated(tickets.TicketObjects.Using(backend), selection)
			values, err := eager.All(ctx)
			if err == nil || values != nil || rows.closed != 1 {
				t.Fatal("invalid rows published partial result", values, err, rows.closed)
			}
			if test.closeErr != nil && !errors.Is(err, test.closeErr) {
				t.Fatal("close cause lost", err)
			}
			if test.code != "" {
				var qerr *query.Error
				if !errors.As(err, &qerr) || qerr.Code != test.code {
					t.Fatal("wrong row error", err, test.code)
				}
			}
			backend.rows = &productRows{index: -1, values: [][]any{valid}}
			values, err = eager.All(ctx)
			if err != nil || len(values) != 1 {
				t.Fatal("failed result poisoned retry", err)
			}
		})
	}
	t.Run("absent_ancestor_rejects_present_descendant", func(t *testing.T) {
		values := append(append([]any(nil), absent...), n(1), s("unexpected grandchild"))
		backend := &suppliedQuery{rows: &productRows{index: -1, values: [][]any{values}}}
		if rows, err := orm.SelectRelated(tickets.TicketObjects.Using(backend), nested).All(ctx); err == nil || rows != nil {
			t.Fatal("absent ancestor published descendant", err)
		}
	})
	t.Run("query_failure_retries_without_partial_cache", func(t *testing.T) {
		failure := errors.New("eager query failure")
		backend := &countedQuery{Queryer: &suppliedQuery{rows: &productRows{index: -1, values: [][]any{valid}}}, fail: failure}
		eager := orm.SelectRelated(tickets.TicketObjects.Using(backend), selection)
		if values, err := eager.All(ctx); !errors.Is(err, failure) || values != nil {
			t.Fatal("query error lost", err)
		}
		backend.fail = nil
		if values, err := eager.All(ctx); err != nil || len(values) != 1 || backend.calls.Load() != 2 {
			t.Fatal("query error poisoned retry", err)
		}
	})
	t.Run("duplicate_owner_same_child_and_explicit_zero", func(t *testing.T) {
		for _, rows := range [][][]any{{valid, valid}, {{n(0), s("zero"), n(0), n(0), s("zero child")}}} {
			backend := &suppliedQuery{rows: &productRows{index: -1, values: rows}}
			values, err := orm.SelectRelated(tickets.TicketObjects.Using(backend), selection).All(ctx)
			if err != nil || len(values) != len(rows) {
				t.Fatal("valid repeated owner or explicit zero rejected", err)
			}
			handle, err := selection.Related(values[0])
			if err != nil {
				t.Fatal(err)
			}
			_, present, err := handle.Get(ctx)
			if err != nil || !present {
				t.Fatal(present, err)
			}
		}
	})
}
