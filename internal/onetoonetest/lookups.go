package onetoonetest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/conformance/onetoonefixture/project"
	"github.com/progresshans/godj/conformance/onetoonefixture/reports"
	"github.com/progresshans/godj/conformance/onetoonefixture/tickets"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type lookupReference struct {
	Name       string  `json:"name"`
	IDs        []int64 `json:"ids"`
	Selects    int64   `json:"selects"`
	LeftJoins  int     `json:"left_joins"`
	InnerJoins int     `json:"inner_joins"`
}

type lookupPair struct{ typed, dynamic orm.Predicate[tickets.Ticket] }

// RunReverseLookups uses authored inputs and actual generated types. Expected
// rows and join counts come from a separate public-Django process.
func RunReverseLookups(t *testing.T, backend ProductBackend, dialect string, compile func(query.Plan) (string, error), queryCount func() uint64) {
	t.Helper()
	ctx := t.Context()
	link := seedLookupProduct(t, backend)
	if compile == nil || queryCount == nil {
		t.Fatal("reverse lookup comparison requires native compilation and SQL observation")
	}
	bindings, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	r := bindings.TicketsTicket
	atom := func(typed orm.Predicate[tickets.Ticket], key string, value any) lookupPair {
		t.Helper()
		input := []orm.LookupInput{{Key: key, Value: value}}
		var parsed []orm.Predicate[tickets.Ticket]
		var err error
		if strings.Contains(key, "__") {
			parsed, err = r.ParseDynamic(nil, input)
		} else {
			parsed, err = orm.ParseDynamic[tickets.Ticket](tickets.TicketDescriptor{}, nil, input)
		}
		if err != nil || len(parsed) != 1 {
			t.Fatalf("dynamic input %s: %v", key, err)
		}
		return lookupPair{typed, parsed[0]}
	}
	not := func(p lookupPair) lookupPair { return lookupPair{orm.Not(p.typed), orm.Not(p.dynamic)} }
	and := func(a, b lookupPair) lookupPair {
		return lookupPair{orm.And(a.typed, b.typed), orm.And(a.dynamic, b.dynamic)}
	}
	or := func(a, b lookupPair) lookupPair {
		return lookupPair{orm.Or(a.typed, b.typed), orm.Or(a.dynamic, b.dynamic)}
	}
	absent := atom(r.Report.IsNull(true), "report__isnull", true)
	note := atom(r.Report.Note.Exact("original"), "report__note", "original")
	score := atom(r.Review.Score.Exact(5), "review__score", int64(5))
	scoreGTE := atom(r.Review.Score.GreaterThanOrEqual(5), "review__score__gte", int64(5))
	scoreNull := atom(r.Review.Score.IsNull(true), "review__score__isnull", true)
	approved := atom(r.Review.Approved.Exact(true), "review__approved", true)
	declined := atom(r.Review.Approved.Exact(false), "review__approved", false)
	title := atom(r.Review.Title.IContains("alpha"), "review__title__icontains", "alpha")
	empty := atom(r.Review.Score.In(), "review__score__in", []int64{})
	onlyFive := atom(r.Review.Score.In(5), "review__score__in", []int64{5})
	cases := []struct {
		name string
		pair lookupPair
	}{
		{"report_absent", absent}, {"report_present", atom(r.Report.IsNull(false), "report__isnull", false)},
		{"report_field_null", atom(r.Report.Note.IsNull(true), "report__note__isnull", true)},
		{"report_not_equal", not(note)}, {"report_or_absent", or(note, absent)},
		{"score_exact", score}, {"score_gt", atom(r.Review.Score.GreaterThan(5), "review__score__gt", int64(5))},
		{"score_gte", scoreGTE}, {"score_lt", atom(r.Review.Score.LessThan(12), "review__score__lt", int64(12))},
		{"score_lte", atom(r.Review.Score.LessThanOrEqual(12), "review__score__lte", int64(12))},
		{"score_null", scoreNull}, {"score_non_null", atom(r.Review.Score.IsNull(false), "review__score__isnull", false)},
		{"score_not_exact", not(score)}, {"score_in", atom(r.Review.Score.In(5, 12), "review__score__in", []int64{5, 12})},
		{"score_empty_in", empty}, {"score_not_in", not(onlyFive)}, {"score_not_empty_in", not(empty)},
		{"approved_true", approved}, {"approved_false", declined},
		{"approved_null", atom(r.Review.Approved.IsNull(true), "review__approved__isnull", true)},
		{"approved_not_true", not(approved)}, {"title_contains", title},
		{"title_literal_wildcards", atom(r.Review.Title.IContains("100%_"), "review__title__icontains", "100%_")},
		{"not_and", not(and(scoreGTE, title))}, {"not_or", not(or(score, absent))}, {"double_not", not(not(score))},
		{"same_edge_or", or(score, declined)}, {"different_edge_or", or(note, scoreNull)},
		{"root_or_reverse", or(atom(tickets.TicketFields.Subject.Exact("fourth"), "subject", "fourth"), score)},
		{"optional_absent", atom(r.OptionalReport.IsNull(true), "optional_report__isnull", true)},
		{"optional_present", atom(r.OptionalReport.IsNull(false), "optional_report__isnull", false)},
		{"mixed_collection_and", and(atom(r.Links.ID.Exact(link.ID), "links__id", link.ID), or(score, atom(r.Review.IsNull(true), "review__isnull", true)))},
		{"body_null", atom(r.Review.Body.IsNull(true), "review__body__isnull", true)},
		{"ratio_null", atom(r.Review.Ratio.IsNull(true), "review__ratio__isnull", true)},
		{"price_null", atom(r.Review.Price.IsNull(true), "review__price__isnull", true)},
		{"token_null", atom(r.Review.Token.IsNull(true), "review__token__isnull", true)},
		{"payload_null", atom(r.Review.Payload.IsNull(true), "review__payload__isnull", true)},
		{"day_null", atom(r.Review.Day.IsNull(true), "review__day__isnull", true)},
		{"at_null", atom(r.Review.At.IsNull(true), "review__at__isnull", true)},
		{"clock_null", atom(r.Review.Clock.IsNull(true), "review__clock__isnull", true)},
		{"elapsed_null", atom(r.Review.Elapsed.IsNull(true), "review__elapsed__isnull", true)},
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "one-to-one-django61-"+dialect+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Lookups []lookupReference `json:"lookups"`
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]lookupReference)
	for _, row := range reference.Lookups {
		if _, duplicate := expected[row.Name]; duplicate {
			t.Fatal("duplicate reference", row.Name)
		}
		expected[row.Name] = row
	}
	if len(expected) != len(cases) {
		t.Fatal("query case inventory differs", len(expected), len(cases))
	}
	reads := &countedQuery{Queryer: backend}
	for _, test := range cases {
		want, ok := expected[test.name]
		if !ok {
			t.Fatal("reference case missing", test.name)
		}
		delete(expected, test.name)
		t.Run(test.name, func(t *testing.T) {
			typed := tickets.TicketObjects.Using(reads).Filter(test.pair.typed).OrderBy(tickets.TicketFields.ID.Asc())
			dynamic := tickets.TicketObjects.Using(reads).Filter(test.pair.dynamic).OrderBy(tickets.TicketFields.ID.Asc())
			if !typed.Plan().Equal(dynamic.Plan()) {
				t.Fatal("typed/dynamic AST differs")
			}
			for name, qs := range map[string]orm.QuerySet[tickets.Ticket]{"typed": typed, "dynamic": dynamic} {
				before := queryCount()
				rows, err := qs.All(ctx)
				if err != nil {
					t.Fatal(name, err)
				}
				ids := make([]int64, len(rows))
				for i, row := range rows {
					ids[i] = row.ID
				}
				if !reflect.DeepEqual(ids, want.IDs) || int64(queryCount()-before) != want.Selects {
					t.Fatalf("%s IDs=%v selects=%d, want %v selects=%d", name, ids, queryCount()-before, want.IDs, want.Selects)
				}
				if count, err := qs.Fresh().Count(ctx); err != nil || count != int64(len(want.IDs)) {
					t.Fatal("cold count differs", count, err)
				}
			}
			if want.Selects != 0 {
				sql, err := compile(typed.Plan())
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(sql, " LEFT OUTER JOIN ") != want.LeftJoins || strings.Count(sql, " INNER JOIN ") != want.InnerJoins {
					t.Fatalf("join presence differs from Django: %s; want left=%d inner=%d", sql, want.LeftJoins, want.InnerJoins)
				}
			}
		})
	}
	if len(expected) != 0 {
		t.Fatal("reference observations not consumed", expected)
	}
	// Invalid collection operands and dynamic values must fail
	// even for an empty result, without issuing SQL or returning a partial batch.
	before := reads.calls.Load()
	for _, bad := range [][]orm.LookupInput{
		{{Key: "report__isnull", Value: "true"}}, {{Key: "review__score__gt", Value: true}},
		{{Key: "links__isnull", Value: "true"}}, {{Key: "links__id__gt", Value: "0"}},
		{{Key: "report__note", Value: "original"}, {Key: "review__unknown", Value: int64(1)}},
	} {
		if predicates, err := r.ParseDynamic(nil, bad); err == nil || predicates != nil {
			t.Fatal("invalid dynamic input published predicates", bad, err)
		}
	}
	if predicates, err := r.ParseDynamic(func(ir.Field, query.Lookup) bool { return false }, []orm.LookupInput{{Key: "review__isnull", Value: true}}); err == nil || predicates != nil {
		t.Fatal("reverse presence bypassed policy", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tickets.TicketObjects.Using(reads).Filter(r.Report.IsNull(true)).All(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if reads.calls.Load() != before {
		t.Fatal("invalid or canceled lookup performed I/O")
	}
	beforeSQL := queryCount()
	collection, err := tickets.TicketObjects.Using(reads).Limit(0)
	if err != nil {
		t.Fatal(err)
	}
	collection = collection.Filter(orm.Or(r.Links.ID.Exact(link.ID), r.Report.IsNull(true)))
	if rows, err := collection.All(ctx); err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("valid empty collection OR failed", err)
	}
	if queryCount() != beforeSQL {
		t.Fatal("valid empty collection executed SQL")
	}
}

func seedLookupProduct(t *testing.T, backend ProductBackend) reports.Link {
	t.Helper()
	ctx := t.Context()
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, productHistory(t), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	var parents []tickets.Ticket
	for _, name := range []string{"first", "second", "third", "fourth"} {
		parent, err := tickets.TicketObjects.Create(ctx, backend, tickets.NewTicketCreate(name))
		if err != nil {
			t.Fatal(err)
		}
		parents = append(parents, parent)
	}
	if _, err := reports.ReportObjects.Create(ctx, backend, reports.NewReportCreate(parents[0].ID, "original")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []reports.ReviewCreate{
		reports.NewReviewCreate(parents[0].ID).WithScore(5).WithTitle("Alpha 100%_").WithApproved(true),
		reports.NewReviewCreate(parents[1].ID),
		reports.NewReviewCreate(parents[2].ID).WithScore(12).WithTitle("Beta").WithApproved(false),
	} {
		if _, err := reports.ReviewObjects.Create(ctx, backend, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []reports.OptionalReportCreate{
		reports.NewOptionalReportCreate("linked").WithTicketID(parents[1].ID),
		reports.NewOptionalReportCreate("orphan"),
	} {
		if _, err := reports.OptionalReportObjects.Create(ctx, backend, input); err != nil {
			t.Fatal(err)
		}
	}
	link, err := reports.LinkObjects.Create(ctx, backend, reports.NewLinkCreate(parents[0].ID, "linked"))
	if err != nil {
		t.Fatal(err)
	}

	return link
}
