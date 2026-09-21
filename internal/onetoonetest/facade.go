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
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type facadeObserver struct {
	ProductBackend
	mu    sync.Mutex
	plans []query.Plan
	fail  error
	rows  db.Rows
}

func (b *facadeObserver) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	b.mu.Lock()
	b.plans = append(b.plans, plan)
	failure, rows := b.fail, b.rows
	b.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	if rows != nil {
		return rows, nil
	}
	return b.ProductBackend.Query(ctx, plan)
}
func (b *facadeObserver) snapshot() []query.Plan {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]query.Plan(nil), b.plans...)
}

type facadeRead[S any] func(*testing.T, context.Context, *S, string, map[string]any)

func facadeEdge[S, T any](name string, get func(context.Context, *S) (*T, bool, error), key func(*T) int64, fields func(*T) map[string]any, children ...facadeRead[T]) facadeRead[S] {
	return func(t *testing.T, ctx context.Context, source *S, prefix string, row map[string]any) {
		t.Helper()
		path := prefix + name
		var value *T
		if source != nil {
			target, present, err := get(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			if present {
				if target == nil {
					t.Fatal("present facade target is nil")
				}
				value = target
			} else if target != nil {
				t.Fatal("absent facade target is non-nil")
			}
		}
		row[path] = nil
		if value != nil {
			row[path] = key(value)
		}
		if fields != nil {
			for field, v := range fields(value) {
				row[path+"_"+field] = v
			}
		}
		for _, child := range children {
			child(t, ctx, value, path+"__", row)
		}
	}
}

func RunFacade(t *testing.T, backend ProductBackend, dialect string, compile func(query.Plan) (string, error), queryCount func() uint64) {
	t.Helper()
	ctx := t.Context()
	seedLookupProduct(t, backend)
	observed := &facadeObserver{ProductBackend: backend}
	models, err := project.Using(observed)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	report := func(children ...facadeRead[project.ReportsReport]) facadeRead[project.TicketsTicket] {
		return facadeEdge("report", func(ctx context.Context, v *project.TicketsTicket) (*project.ReportsReport, bool, error) {
			return v.Report(ctx)
		}, func(v *project.ReportsReport) int64 { return v.ID }, nil, children...)
	}
	optional := func(children ...facadeRead[project.ReportsOptionalReport]) facadeRead[project.TicketsTicket] {
		return facadeEdge("optional_report", func(ctx context.Context, v *project.TicketsTicket) (*project.ReportsOptionalReport, bool, error) {
			return v.OptionalReport(ctx)
		}, func(v *project.ReportsOptionalReport) int64 { return v.ID }, nil, children...)
	}
	review := func(children ...facadeRead[project.ReportsReview]) facadeRead[project.TicketsTicket] {
		return facadeEdge("review", func(ctx context.Context, v *project.TicketsTicket) (*project.ReportsReview, bool, error) {
			return v.Review(ctx)
		}, func(v *project.ReportsReview) int64 { return v.ID }, func(v *project.ReportsReview) map[string]any {
			values := map[string]any{"score": nil, "approved": nil}
			if v != nil {
				if v.Score != nil {
					values["score"] = *v.Score
				}
				if v.Approved != nil {
					values["approved"] = *v.Approved
				}
			}
			return values
		}, children...)
	}
	reportOwner := func(children ...facadeRead[project.TicketsTicket]) facadeRead[project.ReportsReport] {
		return facadeEdge("ticket", func(ctx context.Context, v *project.ReportsReport) (*project.TicketsTicket, bool, error) {
			parent, err := v.Ticket(ctx)
			return parent, err == nil, err
		}, func(v *project.TicketsTicket) int64 { return v.ID }, nil, children...)
	}
	optionalOwner := func(children ...facadeRead[project.TicketsTicket]) facadeRead[project.ReportsOptionalReport] {
		return facadeEdge("ticket", func(ctx context.Context, v *project.ReportsOptionalReport) (*project.TicketsTicket, bool, error) {
			return v.Ticket(ctx)
		}, func(v *project.TicketsTicket) int64 { return v.ID }, nil, children...)
	}
	reviewOwner := func(children ...facadeRead[project.TicketsTicket]) facadeRead[project.ReportsReview] {
		return facadeEdge("ticket", func(ctx context.Context, v *project.ReportsReview) (*project.TicketsTicket, bool, error) {
			parent, err := v.Ticket(ctx)
			return parent, err == nil, err
		}, func(v *project.TicketsTicket) int64 { return v.ID }, nil, children...)
	}
	r := models.TicketsTicket.Related
	cases := []struct {
		name      string
		paths     []string
		selectors []project.TicketsTicketRelationSelector
		read      []facadeRead[project.TicketsTicket]
		filters   []orm.Predicate[tickets.Ticket]
	}{
		{name: "report", paths: []string{"report"}, selectors: []project.TicketsTicketRelationSelector{r.Report}, read: []facadeRead[project.TicketsTicket]{report()}},
		{name: "optional", paths: []string{"optional_report"}, selectors: []project.TicketsTicketRelationSelector{r.OptionalReport}, read: []facadeRead[project.TicketsTicket]{optional()}},
		{name: "review", paths: []string{"review"}, selectors: []project.TicketsTicketRelationSelector{r.Review}, read: []facadeRead[project.TicketsTicket]{review()}},
		{name: "multiple", paths: []string{"report", "optional_report", "review"}, selectors: []project.TicketsTicketRelationSelector{r.Review, r.OptionalReport, r.Report}, read: []facadeRead[project.TicketsTicket]{report(), optional(), review()}},
		{name: "present", paths: []string{"report"}, selectors: []project.TicketsTicketRelationSelector{r.Report}, read: []facadeRead[project.TicketsTicket]{report()}, filters: []orm.Predicate[tickets.Ticket]{reverse.TicketsTicket.Report.IsNull(false)}},
		{name: "absent", paths: []string{"report"}, selectors: []project.TicketsTicketRelationSelector{r.Report}, read: []facadeRead[project.TicketsTicket]{report()}, filters: []orm.Predicate[tickets.Ticket]{reverse.TicketsTicket.Report.IsNull(true)}},
		{name: "or_absent", paths: []string{"report", "review"}, selectors: []project.TicketsTicketRelationSelector{r.Report, r.Review}, read: []facadeRead[project.TicketsTicket]{report(), review()}, filters: []orm.Predicate[tickets.Ticket]{orm.Or(reverse.TicketsTicket.Report.Note.Exact("original"), reverse.TicketsTicket.Review.IsNull(true))}},
		{name: "reverse_forward", paths: []string{"report__ticket"}, selectors: []project.TicketsTicketRelationSelector{r.Report.WithChildren(models.ReportsReport.Related.Ticket)}, read: []facadeRead[project.TicketsTicket]{report(reportOwner())}},
		{name: "reverse_forward_reverse", paths: []string{"report__ticket__review"}, selectors: []project.TicketsTicketRelationSelector{r.Report.WithChildren(models.ReportsReport.Related.Ticket.WithChildren(r.Review))}, read: []facadeRead[project.TicketsTicket]{report(reportOwner(review()))}},
		{name: "optional_forward_reverse", paths: []string{"optional_report__ticket__report"}, selectors: []project.TicketsTicketRelationSelector{r.OptionalReport.WithChildren(models.ReportsOptionalReport.Related.Ticket.WithChildren(r.Report))}, read: []facadeRead[project.TicketsTicket]{optional(optionalOwner(report()))}},
		{name: "review_forward_reverse", paths: []string{"review__ticket__report"}, selectors: []project.TicketsTicketRelationSelector{r.Review.WithChildren(models.ReportsReview.Related.Ticket.WithChildren(r.Report))}, read: []facadeRead[project.TicketsTicket]{review(reviewOwner(report()))}},
		{name: "repeated_declaration", paths: []string{"report__ticket__report"}, selectors: []project.TicketsTicketRelationSelector{r.Report.WithChildren(models.ReportsReport.Related.Ticket.WithChildren(r.Report))}, read: []facadeRead[project.TicketsTicket]{report(reportOwner(report()))}},
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "one-to-one-django61-"+dialect+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Eager          []eagerReference `json:"eager"`
		OutgoingDelete struct {
			BlockedRows []int64 `json:"blocked_rows"`
			Rows        []int64 `json:"rows"`
			Deleted     int64   `json:"deleted"`
		} `json:"outgoing_delete"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	expected := map[string]eagerReference{}
	for _, row := range reference.Eager {
		if _, duplicate := expected[row.Name]; duplicate {
			t.Fatal("duplicate reference")
		}
		expected[row.Name] = row
	}
	if len(cases) != len(expected) {
		t.Fatal("facade reference inventory mismatch")
	}
	source := models.TicketsTicket.OrderBy(tickets.TicketFields.ID.Asc())
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			want, found := expected[test.name]
			if !found {
				t.Fatal("missing reference")
			}
			filtered := source.Filter(test.filters...)
			dynamic, err := filtered.SelectRelatedPaths(test.paths...)
			if err != nil {
				t.Fatal(err)
			}
			typed := filtered.SelectRelated(test.selectors...)
			var canonical query.Plan
			for i, eager := range []project.TicketsTicketEagerQuery{typed, dynamic} {
				before := queryCount()
				count, err := eager.Count(ctx)
				if err != nil || count != int64(len(want.Rows)) || queryCount()-before != 1 {
					t.Fatal("cold facade count", count, err, queryCount()-before)
				}
				before = queryCount()
				start := len(observed.snapshot())
				values, err := eager.All(ctx)
				if err != nil {
					t.Fatal(err)
				}
				plans := observed.snapshot()[start:]
				if len(plans) != 1 || queryCount()-before != want.LoadSelects {
					t.Fatal("eager facade performed extra SQL or Query calls", len(plans), queryCount()-before)
				}
				if i == 0 {
					canonical = plans[0]
				} else if !canonical.Equal(plans[0]) {
					t.Fatal("typed and dynamic facade plans differ")
				}
				statement, err := compile(plans[0])
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(statement, " LEFT OUTER JOIN ") != want.LeftJoins || strings.Count(statement, " INNER JOIN ") != want.InnerJoins {
					t.Fatal("facade joins differ from reference", statement)
				}
				start = len(observed.snapshot())
				before = queryCount()
				rows := make([]map[string]any, 0, len(values))
				for _, value := range values {
					row := map[string]any{"root": value.ID}
					for _, read := range test.read {
						read(t, ctx, value, "", row)
					}
					rows = append(rows, row)
				}
				actual, _ := json.Marshal(rows)
				expected, _ := json.Marshal(want.Rows)
				if string(actual) != string(expected) {
					t.Fatalf("facade rows: %s\nreference: %s", actual, expected)
				}
				if _, ok, err := eager.First(ctx); err != nil || ok != (len(values) > 0) {
					t.Fatal("warm First", ok, err)
				}
				if _, err := eager.All(ctx); err != nil {
					t.Fatal(err)
				}
				if count, err := eager.Count(ctx); err != nil || count != int64(len(values)) {
					t.Fatal(count, err)
				}
				if queryCount() != before || len(observed.snapshot()) != start {
					t.Fatal("warm facade graph performed I/O")
				}
			}
		})
	}
	t.Run("lazy_child_select_is_observed", func(t *testing.T) {
		owner, found, err := source.Filter(tickets.TicketFields.ID.Exact(1)).First(ctx)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		before := queryCount()
		child, present, err := owner.Report(ctx)
		if err != nil || !present || child.ID != 1 || queryCount()-before != 1 {
			t.Fatal("child-table SELECT not observed", present, queryCount()-before, err)
		}
		before = queryCount()
		again, present, err := owner.Report(ctx)
		if err != nil || !present || again != child || queryCount() != before {
			t.Fatal("lazy facade cache did not retain identity", err)
		}
	})
	t.Run("query_derivation_identity_and_clone", func(t *testing.T) {
		eager, err := source.SelectRelatedPaths("report__ticket__review", "review")
		if err != nil {
			t.Fatal(err)
		}
		values, err := eager.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		before := queryCount()
		child, present, err := values[0].Report(ctx)
		if err != nil || !present {
			t.Fatal(present, err)
		}
		same, _, err := values[0].Report(ctx)
		if err != nil || same != child {
			t.Fatal("same wrapper lost target identity")
		}
		parent, err := child.Ticket(ctx)
		if err != nil || parent == values[0] {
			t.Fatal("reciprocal occurrence was aliased", err)
		}
		review, _, err := parent.Review(ctx)
		if err != nil {
			t.Fatal(err)
		}
		*review.Score = 999
		ownReview, _, err := values[0].Review(ctx)
		if err != nil || *ownReview.Score != 5 {
			t.Fatal("sibling occurrence shared mutable field", err)
		}
		again, err := eager.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		other, _, err := again[0].Report(ctx)
		if err != nil || other == child {
			t.Fatal("separate materialization shared target pointer")
		}
		if queryCount() != before {
			t.Fatal("graph copies lost cached descendants")
		}
		offset, err := eager.Fresh().Distinct().Offset(1)
		if err != nil {
			t.Fatal(err)
		}
		limited, err := offset.Limit(2)
		if err != nil {
			t.Fatal(err)
		}
		slice, err := limited.All(ctx)
		if err != nil || len(slice) != 2 || slice[0].ID != 2 || slice[1].ID != 3 {
			t.Fatal("derived eager rows", err)
		}
		first, found, err := limited.Fresh().First(ctx)
		if err != nil || !found || first.ID != 2 {
			t.Fatal("cold eager First", found, err)
		}
	})
	t.Run("new_owner_save_and_forward_assignment", func(t *testing.T) {
		parent, err := models.TicketsTicket.New(tickets.Ticket{Subject: "created"})
		if err != nil {
			t.Fatal("unsaved reverse owner could not be wrapped", err)
		}
		before := len(observed.snapshot())
		if _, present, err := parent.Report(ctx); present || !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeMissingPrimaryKey}) || len(observed.snapshot()) != before {
			t.Fatal("unsaved owner reverse access", present, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := parent.Report(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation precedence", err)
		}
		if err := parent.Save(ctx); err != nil || parent.ID == 0 {
			t.Fatal("save parent", err)
		}
		if _, present, err := parent.Report(ctx); err != nil || present {
			t.Fatal("saved owner retained unsaved reverse handle", present, err)
		}
		child, err := models.ReportsReport.New(reports.Report{Note: "new child"})
		if err != nil {
			t.Fatal(err)
		}
		assigned, err := child.WithTicket(parent)
		if err != nil {
			t.Fatal(err)
		}
		before = len(observed.snapshot())
		owner, err := assigned.Ticket(ctx)
		if err != nil || owner != parent || len(observed.snapshot()) != before {
			t.Fatal("forward assignment identity", err)
		}
		if err := assigned.Save(ctx); err != nil {
			t.Fatal(err)
		}
		if _, present, err := parent.Report(ctx); err != nil || present {
			t.Fatal("external child save overwrote cached absence", present, err)
		}
		fresh, err := models.TicketsTicket.Fresh().Filter(tickets.TicketFields.ID.Exact(parent.ID)).OrderBy(tickets.TicketFields.ID.Asc()).SelectRelatedPaths("report__ticket")
		if err != nil {
			t.Fatal(err)
		}
		loaded, found, err := fresh.First(ctx)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		saved, present, err := loaded.Report(ctx)
		if err != nil || !present || saved.ID != assigned.ID {
			t.Fatal("fresh facade did not observe saved child", present, err)
		}
		copied := *loaded
		if _, _, err := copied.Report(ctx); err == nil {
			t.Fatal("copied facade accepted")
		}
		loaded.ID++
		if _, _, err := loaded.Report(ctx); !errors.Is(err, &query.Error{Code: query.CodePrimaryKeyUpdateField}) {
			t.Fatal("mutated owner PK accepted", err)
		}
	})
	t.Run("forward_change_preserves_selected_reverse_sibling", func(t *testing.T) {
		certificate, err := reports.CertificateObjects.Create(ctx, backend, reports.NewCertificateCreate(1, "sealed"))
		if err != nil {
			t.Fatal(err)
		}
		eager, err := models.ReportsReport.Filter(reports.ReportFields.ID.Exact(1)).OrderBy(reports.ReportFields.ID.Asc()).SelectRelatedPaths("ticket", "certificate__report")
		if err != nil {
			t.Fatal(err)
		}
		report, found, err := eager.First(ctx)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		before := queryCount()
		child, present, err := report.Certificate(ctx)
		if err != nil || !present || child.ID != certificate.ID {
			t.Fatal("selected reverse sibling", present, err)
		}
		other, found, err := models.TicketsTicket.Filter(tickets.TicketFields.ID.Exact(2)).OrderBy(tickets.TicketFields.ID.Asc()).First(ctx)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		before = queryCount()
		derived, err := report.WithTicket(other)
		if err != nil {
			t.Fatal(err)
		}
		same, present, err := derived.Certificate(ctx)
		if err != nil || !present || same != child {
			t.Fatal("WithTicket lost reverse sibling identity", err)
		}
		snapshot, err := same.Report(ctx)
		if err != nil || snapshot.TicketID != 1 {
			t.Fatal("reverse subtree was mutated with parent FK", err)
		}
		report.TicketID = 2
		same, present, err = report.Certificate(ctx)
		if err != nil || !present || same != child {
			t.Fatal("raw FK reconciliation lost reverse cache", err)
		}
		if queryCount() != before {
			t.Fatal("forward update discarded selected reverse graph")
		}
	})
	t.Run("invalid_paths_origins_and_retry", func(t *testing.T) {
		before := len(observed.snapshot())
		for _, paths := range [][]string{nil, {""}, {"links"}, {"report__links"}, {"report__ticket__links"}, {"report", "review__missing"}, {"Report"}, {"report__"}, {strings.Repeat("report__ticket__", 32) + "report"}} {
			if _, err := source.SelectRelatedPaths(paths...); err == nil {
				t.Fatal("invalid eager path accepted", paths)
			}
		}
		other, err := project.Using(observed)
		if err != nil {
			t.Fatal(err)
		}
		for _, eager := range []project.TicketsTicketEagerQuery{source.SelectRelated(other.TicketsTicket.Related.Report), source.SelectRelated(r.Report.WithChildren(other.ReportsReport.Related.Ticket)), source.SelectRelated(nil)} {
			if rows, err := eager.All(ctx); err == nil || rows != nil {
				t.Fatal("invalid origin selection accepted", err)
			}
			if _, err := eager.Count(ctx); err == nil {
				t.Fatal("invalid selector Count accepted")
			}
		}
		if len(observed.snapshot()) != before {
			t.Fatal("invalid selection performed I/O")
		}
		failure := errors.New("facade read failure")
		observed.fail = failure
		eager := source.SelectRelated(r.Report)
		if values, err := eager.All(ctx); values != nil || !errors.Is(err, failure) {
			t.Fatal("facade failure published rows", err)
		}
		observed.fail = nil
		if values, err := eager.All(ctx); err != nil || len(values) != 5 {
			t.Fatal("facade retry failed", len(values), err)
		}
		// A valid first joined row followed by an unreadable row must not publish
		// a partial facade graph. The same query must remain retryable.
		observed.rows = &productRows{index: -1, values: [][]any{{sql.NullInt64{Int64: 1, Valid: true}, sql.NullString{String: "first", Valid: true}, sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: 1, Valid: true}, sql.NullString{String: "original", Valid: true}}, {}}}
		fresh := source.SelectRelated(r.Report)
		if values, err := fresh.All(ctx); err == nil || values != nil {
			t.Fatal("scan failure published graph", err)
		}
		observed.rows = nil
		if _, err := fresh.All(ctx); err != nil {
			t.Fatal("scan failure poisoned retry", err)
		}
	})
	t.Run("incoming_protection_with_outgoing_fk", func(t *testing.T) {
		original, err := reports.ReportObjects.Create(ctx, backend, reports.NewReportCreate(4, "deletable"))
		if err != nil {
			t.Fatal(err)
		}
		child, err := reports.CertificateObjects.Create(ctx, backend, reports.NewCertificateCreate(original.ID, "protect"))
		if err != nil {
			t.Fatal(err)
		}
		deleters, err := project.BindRelationDeleters()
		if err != nil {
			t.Fatal(err)
		}
		retained := original
		if reference.OutgoingDelete.Deleted != 1 || len(reference.OutgoingDelete.BlockedRows) != 3 || len(reference.OutgoingDelete.Rows) != 3 {
			t.Fatal("outgoing delete reference is missing")
		}
		if count, err := deleters.ReportsReport.Delete(ctx, backend, &retained); count != 0 || !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) || retained.ID != original.ID || retained.TicketID != original.TicketID {
			t.Fatal("PROTECT changed an owner with an outgoing FK", count, err)
		}
		ownerRows, ownerErr := tickets.TicketObjects.Using(backend).Filter(tickets.TicketFields.ID.Exact(original.TicketID)).Count(ctx)
		reportRows, reportErr := reports.ReportObjects.Using(backend).Filter(reports.ReportFields.ID.Exact(original.ID)).Count(ctx)
		certificateRows, certificateErr := reports.CertificateObjects.Using(backend).Filter(reports.CertificateFields.ID.Exact(child.ID)).Count(ctx)
		if ownerErr != nil || reportErr != nil || certificateErr != nil || ownerRows != reference.OutgoingDelete.BlockedRows[0] || reportRows != reference.OutgoingDelete.BlockedRows[1] || certificateRows != reference.OutgoingDelete.BlockedRows[2] {
			t.Fatal("protected rows differ from reference", ownerRows, reportRows, certificateRows, ownerErr, reportErr, certificateErr)
		}
		certificateID := child.ID
		if count, err := reports.CertificateObjects.Delete(ctx, backend, &child); err != nil || count != 1 {
			t.Fatal("delete referencing certificate", count, err)
		}
		if count, err := deleters.ReportsReport.Delete(ctx, backend, &retained); err != nil || count != reference.OutgoingDelete.Deleted || retained.ID != 0 || retained.TicketID != original.TicketID || retained.Note != original.Note {
			t.Fatal("delete did not preserve non-primary memory", count, retained, err)
		}
		if count, err := tickets.TicketObjects.Using(backend).Filter(tickets.TicketFields.ID.Exact(original.TicketID)).Count(ctx); err != nil || count != reference.OutgoingDelete.Rows[0] {
			t.Fatal("delete changed the referenced owner", count, err)
		}
		if count, err := reports.ReportObjects.Using(backend).Filter(reports.ReportFields.ID.Exact(original.ID)).Count(ctx); err != nil || count != reference.OutgoingDelete.Rows[1] {
			t.Fatal("report row was not deleted", count, err)
		}
		if count, err := reports.CertificateObjects.Using(backend).Filter(reports.CertificateFields.ID.Exact(certificateID)).Count(ctx); err != nil || count != reference.OutgoingDelete.Rows[2] {
			t.Fatal("certificate row was not deleted", count, err)
		}
	})

}
