package article_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/query"
)

func TestArticleHTTPBooleanSearchProjectionReportAndBounds(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-search-http-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	provisionSearchArticles(t, backend)
	captured := &articleSearchCaptureBackend{delegate: backend}
	application, err := webapp.NewApplication(captured)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	wantQueries := uint64(0)

	orSearch := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {"go"}}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, orSearch, []string{"1", "2", "3", "4"}, []string{"5", "6", "7"})
	if !strings.Contains(articleElement(t, orSearch, "1"), `class="summary summary-null"`) {
		t.Fatalf("title-side OR match with NULL summary was lost: %q", orSearch)
	}
	assertArticleListMetadata(t, orSearch, 4, "4", 0, 20, 4)
	assertArticleQueryCount(t, backend, wantQueries)

	beforeCombined := len(captured.snapshotPlans())
	combined := getBody(t, client, articleSearchURL(server.URL, url.Values{
		"q":             {"go"},
		"published":     {"true"},
		"exclude_title": {"draft"},
	}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, combined, []string{"1", "2"}, []string{"3", "4", "5", "6", "7"})
	assertArticleListMetadata(t, combined, 2, "2", 0, 20, 2)
	assertArticleBooleanSearchPlans(t, captured.snapshotPlans()[beforeCombined:], "go", true, "draft")
	assertArticleQueryCount(t, backend, wantQueries)

	paged := getBody(t, client, articleSearchURL(server.URL, url.Values{
		"q":             {"go"},
		"exclude_title": {"draft"},
		"offset":        {"1"},
		"limit":         {"1"},
	}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, paged, []string{"2"}, []string{"1", "3", "4", "5", "6", "7"})
	assertArticleListMetadata(t, paged, 3, "4", 1, 1, 1)
	assertArticleQueryCount(t, backend, wantQueries)

	percent := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {"100%"}}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, percent, []string{"5"}, []string{"6"})
	assertArticleListMetadata(t, percent, 1, "5", 0, 20, 1)

	underscore := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {"under_"}}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, underscore, []string{"5"}, []string{"6"})
	assertArticleListMetadata(t, underscore, 1, "5", 0, 20, 1)

	emptyQuery := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {""}}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, emptyQuery, []string{"1", "2", "3", "4", "5", "6", "7"}, nil)
	assertArticleListMetadata(t, emptyQuery, 7, "7", 0, 20, 7)

	emptyExclude := getBody(t, client, articleSearchURL(server.URL, url.Values{"exclude_title": {""}}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, emptyExclude, nil, []string{"1", "2", "3", "4", "5", "6", "7"})
	assertArticleListMetadata(t, emptyExclude, 0, "", 0, 20, 0)

	noMatch := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {"absent"}}), http.StatusOK)
	wantQueries += 2
	assertArticleIDs(t, noMatch, nil, []string{"1", "2", "3", "4", "5", "6", "7"})
	assertArticleListMetadata(t, noMatch, 0, "", 0, 20, 0)

	multibyteAtCap := strings.Repeat("한", 85)
	if len(multibyteAtCap) != 255 {
		t.Fatalf("test setup multibyte query bytes = %d, want 255", len(multibyteAtCap))
	}
	atCap := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {multibyteAtCap}}), http.StatusOK)
	wantQueries += 2
	assertArticleListMetadata(t, atCap, 0, "", 0, 20, 0)
	exactCap := strings.Repeat("a", 256)
	if len(exactCap) != 256 {
		t.Fatalf("test setup exact-cap query bytes = %d, want 256", len(exactCap))
	}
	exactCapBody := getBody(t, client, articleSearchURL(server.URL, url.Values{"q": {exactCap}}), http.StatusOK)
	wantQueries += 2
	assertArticleListMetadata(t, exactCapBody, 0, "", 0, 20, 0)
	assertArticleQueryCount(t, backend, wantQueries)

	invalidQueries := []string{
		"q=go&q=rust",
		"exclude_title=draft&exclude_title=old",
		"q=" + strings.Repeat("a", 257),
		"exclude_title=" + strings.Repeat("b", 257),
		"q=" + url.QueryEscape(strings.Repeat("한", 86)),
		"q=%",
		"exclude_title=%ZZ",
		"q=%FF",
		"exclude_title=%C3%28",
		"q=%00",
		"exclude_title=%00",
	}
	for _, rawQuery := range invalidQueries {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "http://example.test"+webapp.ArticleListPath, nil)
		request.URL.RawQuery = rawQuery
		application.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || response.Body.String() != "Bad Request\n" {
			t.Fatalf("invalid raw query %q = status %d body %q", rawQuery, response.Code, response.Body.String())
		}
		assertArticleQueryCount(t, backend, wantQueries)
	}
}

func provisionSearchArticles(t *testing.T, backend *sqlite.Backend) {
	t.Helper()
	statements := []string{
		`CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, 'Go Launch', TRUE, NULL),
  (2, 'Rust Notes', TRUE, 'A Go summary'),
  (3, 'Go Draft', TRUE, 'Release candidate'),
  (4, 'Go Hidden', FALSE, NULL),
  (5, '100% Coverage', TRUE, 'under_score'),
  (6, '1000 Coverage', TRUE, 'underXscore'),
  (7, 'Other', TRUE, '')`,
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func articleSearchURL(serverURL string, values url.Values) string {
	return serverURL + webapp.ArticleListPath + "?" + values.Encode()
}

func assertArticleIDs(t *testing.T, body string, present, absent []string) {
	t.Helper()
	for _, id := range present {
		if !strings.Contains(body, `data-article-id="`+id+`"`) {
			t.Errorf("Article %s is missing from response", id)
		}
	}
	for _, id := range absent {
		if strings.Contains(body, `data-article-id="`+id+`"`) {
			t.Errorf("Article %s unexpectedly appears in response", id)
		}
	}
}

func assertArticleBooleanSearchPlans(t *testing.T, plans []query.Plan, search string, published bool, exclude string) {
	t.Helper()
	if len(plans) != 2 {
		t.Fatalf("combined search query plans = %d, want exactly 2", len(plans))
	}
	page, report := plans[0], plans[1]
	if page.ResultShape().Kind() != query.ResultProjection || report.ResultShape().Kind() != query.ResultAggregate {
		t.Fatalf("combined search result shapes = %q/%q, want projection/aggregate", page.ResultShape().Kind(), report.ResultShape().Kind())
	}
	pageWhere, pagePresent := page.Where()
	reportWhere, reportPresent := report.Where()
	if !pagePresent || !reportPresent || !pageWhere.Equal(reportWhere) {
		t.Fatal("page and report did not retain the same filtered source")
	}
	if !page.Distinct() || !report.Distinct() {
		t.Fatal("page and report did not retain Distinct")
	}
	orderings := page.Orderings()
	if len(orderings) != 1 || orderings[0].Field().Name() != "id" || orderings[0].Direction() != query.Ascending {
		t.Fatalf("page orderings = %#v, want stable ascending ID", orderings)
	}
	if offset, ok := page.Offset(); !ok || offset != 0 {
		t.Fatalf("page offset = %d/%t, want 0/true", offset, ok)
	}
	if limit, ok := page.Limit(); !ok || limit != 20 {
		t.Fatalf("page limit = %d/%t, want 20/true", limit, ok)
	}
	if len(report.Orderings()) != 0 {
		t.Fatalf("report unexpectedly retained page ordering: %#v", report.Orderings())
	}
	if _, ok := report.Offset(); ok {
		t.Fatal("report unexpectedly retained page offset")
	}
	if _, ok := report.Limit(); ok {
		t.Fatal("report unexpectedly retained page limit")
	}

	root := pageWhere.Children()
	if pageWhere.Kind() != query.ExpressionAnd || len(root) != 3 {
		t.Fatalf("combined where root = kind %d/%d children, want ordered AND/3", pageWhere.Kind(), len(root))
	}
	orChildren := root[0].Children()
	if root[0].Kind() != query.ExpressionOr || len(orChildren) != 2 {
		t.Fatalf("first where child = kind %d/%d children, want OR/2", root[0].Kind(), len(orChildren))
	}
	assertArticleSearchLeaf(t, orChildren[0], "title", query.LookupIContains, search, false)
	assertArticleSearchLeaf(t, orChildren[1], "summary", query.LookupIContains, search, false)
	assertArticleSearchLeaf(t, root[1], "published", query.LookupExact, "", published)
	notChildren := root[2].Children()
	if root[2].Kind() != query.ExpressionNot || len(notChildren) != 1 {
		t.Fatalf("third where child = kind %d/%d children, want NOT/1", root[2].Kind(), len(notChildren))
	}
	assertArticleSearchLeaf(t, notChildren[0], "title", query.LookupIContains, exclude, false)
}

func assertArticleSearchLeaf(t *testing.T, expression query.Expression, field string, lookup query.Lookup, text string, boolean bool) {
	t.Helper()
	condition, ok := expression.Condition()
	if expression.Kind() != query.ExpressionLeaf || !ok || condition.Field().Name() != field || condition.Lookup() != lookup {
		t.Fatalf("search leaf = kind %d field %q lookup %q, want leaf/%q/%q", expression.Kind(), condition.Field().Name(), condition.Lookup(), field, lookup)
	}
	if lookup == query.LookupExact {
		value, ok := condition.Value().Boolean()
		if !ok || value != boolean {
			t.Fatalf("Boolean search leaf value = %t/%t, want %t/true", value, ok, boolean)
		}
		return
	}
	value, ok := condition.Value().String()
	if !ok || value != text {
		t.Fatalf("text search leaf value = %q/%t, want %q/true", value, ok, text)
	}
}

type articleSearchCaptureBackend struct {
	delegate *sqlite.Backend
	mu       sync.Mutex
	plans    []query.Plan
}

func (backend *articleSearchCaptureBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.mu.Lock()
	backend.plans = append(backend.plans, plan)
	backend.mu.Unlock()
	return backend.delegate.Query(ctx, plan)
}

func (backend *articleSearchCaptureBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	return backend.delegate.Insert(ctx, plan)
}

func (backend *articleSearchCaptureBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	return backend.delegate.Update(ctx, plan)
}

func (backend *articleSearchCaptureBackend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	return backend.delegate.Delete(ctx, plan)
}

func (backend *articleSearchCaptureBackend) snapshotPlans() []query.Plan {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]query.Plan(nil), backend.plans...)
}

var _ interface {
	db.Queryer
	db.Mutator
} = (*articleSearchCaptureBackend)(nil)
