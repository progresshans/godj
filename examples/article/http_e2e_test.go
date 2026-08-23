package article_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/webapp"
	"github.com/progresshans/godj/query"
)

func TestArticleHTTPEndToEndProjectionReportFilteringAndPagination(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-http-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	provisionArticles(t, backend)
	application, err := webapp.NewApplication(backend)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	wantQueries := uint64(0)

	first := getBody(t, client, server.URL+webapp.ArticleListPath, http.StatusOK)
	wantQueries += 2
	if !strings.HasPrefix(http.DetectContentType([]byte(first)), "text/html") {
		t.Fatalf("response is not HTML: %q", first)
	}
	if strings.Contains(first, "<script>") || !strings.Contains(first, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("article title was not HTML-escaped: %q", first)
	}
	nullArticle := articleElement(t, first, "1")
	if !strings.Contains(nullArticle, `class="summary summary-null"`) {
		t.Fatalf("NULL summary representation = %q", nullArticle)
	}
	emptyArticle := articleElement(t, first, "2")
	if !strings.Contains(emptyArticle, `<p class="summary"></p>`) || strings.Contains(emptyArticle, "summary-null") {
		t.Fatalf("empty summary representation = %q", emptyArticle)
	}
	if strings.Index(first, `data-article-id="1"`) > strings.Index(first, `data-article-id="2"`) {
		t.Fatalf("articles are not ordered by primary key: %q", first)
	}
	assertArticleListMetadata(t, first, 2, "2", 0, 20, 2)
	assertArticleQueryCount(t, backend, wantQueries)

	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (3, 'Inserted Later', TRUE, NULL)`); err != nil {
		t.Fatal(err)
	}
	second := getBody(t, client, server.URL+webapp.ArticleListPath, http.StatusOK)
	wantQueries += 2
	if !strings.Contains(second, `data-article-id="3"`) || !strings.Contains(second, "Inserted Later") {
		t.Fatalf("second request reused a stale QuerySet result: %q", second)
	}
	assertArticleListMetadata(t, second, 3, "3", 0, 20, 3)
	assertArticleQueryCount(t, backend, wantQueries)

	published := getBody(t, client, server.URL+webapp.ArticleListPath+"?published=true", http.StatusOK)
	wantQueries += 2
	if !strings.Contains(published, `data-article-id="1"`) || !strings.Contains(published, `data-article-id="3"`) ||
		strings.Contains(published, `data-article-id="2"`) {
		t.Fatalf("published=true response = %q", published)
	}
	assertArticleListMetadata(t, published, 2, "3", 0, 20, 2)

	unpublished := getBody(t, client, server.URL+webapp.ArticleListPath+"?published=false", http.StatusOK)
	wantQueries += 2
	if !strings.Contains(unpublished, `data-article-id="2"`) || strings.Contains(unpublished, `data-article-id="1"`) ||
		strings.Contains(unpublished, `data-article-id="3"`) {
		t.Fatalf("published=false response = %q", unpublished)
	}
	assertArticleListMetadata(t, unpublished, 1, "2", 0, 20, 1)

	paged := getBody(t, client, server.URL+webapp.ArticleListPath+"?offset=1&limit=1", http.StatusOK)
	wantQueries += 2
	if !strings.Contains(paged, `data-article-id="2"`) || strings.Contains(paged, `data-article-id="1"`) ||
		strings.Contains(paged, `data-article-id="3"`) {
		t.Fatalf("offset/limit response = %q", paged)
	}
	assertArticleListMetadata(t, paged, 3, "3", 1, 1, 1)

	outOfRange := getBody(t, client, server.URL+webapp.ArticleListPath+"?offset=99&limit=1", http.StatusOK)
	wantQueries += 2
	if !strings.Contains(outOfRange, `<li class="empty">No articles.</li>`) {
		t.Fatalf("out-of-range offset response = %q", outOfRange)
	}
	assertArticleListMetadata(t, outOfRange, 3, "3", 99, 1, 0)

	capped := getBody(t, client, server.URL+webapp.ArticleListPath+"?limit=1000", http.StatusOK)
	wantQueries += 2
	assertArticleListMetadata(t, capped, 3, "3", 0, 100, 3)

	unknown := getBody(t, client, server.URL+webapp.ArticleListPath+"?unknown=ignored&limit=1", http.StatusOK)
	wantQueries += 2
	if !strings.Contains(unknown, `data-article-id="1"`) || strings.Contains(unknown, `data-article-id="2"`) {
		t.Fatalf("unknown query parameter changed known pagination semantics: %q", unknown)
	}
	assertArticleListMetadata(t, unknown, 3, "3", 0, 1, 1)
	assertArticleQueryCount(t, backend, wantQueries)

	badQueries := []string{
		"?published=yes",
		"?published=True",
		"?published=true&published=false",
		"?offset=-1",
		"?offset=2147483648",
		"?offset=1&offset=2",
		"?limit=zero",
		"?limit=0",
		"?limit=1&limit=2",
		"?limit=1;offset=2",
	}
	for _, rawQuery := range badQueries {
		body := getBody(t, client, server.URL+webapp.ArticleListPath+rawQuery, http.StatusBadRequest)
		if body != "Bad Request\n" {
			t.Fatalf("invalid query %q body = %q, want deterministic Bad Request", rawQuery, body)
		}
	}
	assertArticleQueryCount(t, backend, wantQueries)

	if queries := backend.QueryCount(); queries != wantQueries {
		t.Fatalf("query count before concurrent requests = %d, want %d", queries, wantQueries)
	}

	const concurrentRequests = 24
	var wait sync.WaitGroup
	failures := make(chan error, concurrentRequests)
	for index := 0; index < concurrentRequests; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, requestErr := client.Get(server.URL + webapp.ArticleListPath)
			if requestErr != nil {
				failures <- requestErr
				return
			}
			defer response.Body.Close()
			body, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				failures <- readErr
				return
			}
			if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `data-article-id="3"`) {
				failures <- errors.New("concurrent response did not contain the latest article")
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	wantQueries += 2 * concurrentRequests
	assertArticleQueryCount(t, backend, wantQueries)

	missing := getResponse(t, client, http.MethodGet, server.URL+"/missing/")
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.StatusCode)
	}
	wrongMethod := getResponse(t, client, http.MethodPost, server.URL+webapp.ArticleListPath)
	defer wrongMethod.Body.Close()
	if wrongMethod.StatusCode != http.StatusMethodNotAllowed || wrongMethod.Header.Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d Allow=%q", wrongMethod.StatusCode, wrongMethod.Header.Get("Allow"))
	}
}

func assertArticleListMetadata(t *testing.T, body string, matching int64, latestID string, offset, limit, returned int) {
	t.Helper()
	wantReport := fmt.Sprintf(`id="article-report" data-matching-count="%d"`, matching)
	if !strings.Contains(body, wantReport) {
		t.Fatalf("report metadata %q is missing from %q", wantReport, body)
	}
	if latestID == "" {
		if strings.Contains(body, "data-latest-id=") {
			t.Fatalf("empty report unexpectedly contains latest ID: %q", body)
		}
	} else if wantLatest := `data-latest-id="` + latestID + `"`; !strings.Contains(body, wantLatest) {
		t.Fatalf("latest metadata %q is missing from %q", wantLatest, body)
	}
	wantPagination := fmt.Sprintf(`id="article-pagination" data-offset="%d" data-limit="%d" data-page-count="%d"`, offset, limit, returned)
	if !strings.Contains(body, wantPagination) {
		t.Fatalf("pagination metadata %q is missing from %q", wantPagination, body)
	}
}

func assertArticleQueryCount(t *testing.T, backend *sqlite.Backend, want uint64) {
	t.Helper()
	if got := backend.QueryCount(); got != want {
		t.Fatalf("Article HTTP query count = %d, want %d", got, want)
	}
}

func TestArticleHTTPPropagatesClientCancellationToBackend(t *testing.T) {
	backend := &cancelBackend{entered: make(chan struct{}), canceled: make(chan struct{})}
	application, err := webapp.NewApplication(backend)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application)
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+webapp.ArticleListPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		response, requestErr := server.Client().Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- requestErr
	}()
	waitSignal(t, backend.entered, "backend query")
	cancel()
	waitSignal(t, backend.canceled, "backend cancellation")
	if requestErr := waitValue(t, result, "HTTP client cancellation"); requestErr == nil {
		t.Fatal("client request error = nil after cancellation")
	}
}

func provisionArticles(t *testing.T, backend *sqlite.Backend) {
	t.Helper()
	statements := []string{
		`CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, '<script>alert(1)</script>', TRUE, NULL),
  (2, 'Empty Summary', FALSE, '')`,
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func getBody(t *testing.T, client *http.Client, address string, status int) string {
	t.Helper()
	response := getResponse(t, client, http.MethodGet, address)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("GET %s status = %d, want %d; body=%q", address, response.StatusCode, status, body)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	return string(body)
}

func getResponse(t *testing.T, client *http.Client, method, address string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func articleElement(t *testing.T, body, id string) string {
	t.Helper()
	start := strings.Index(body, `data-article-id="`+id+`"`)
	if start < 0 {
		t.Fatalf("article %s is missing from %q", id, body)
	}
	endOffset := strings.Index(body[start:], "</li>")
	if endOffset < 0 {
		t.Fatalf("article %s has no closing list item", id)
	}
	return body[start : start+endOffset]
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitValue[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

type cancelBackend struct {
	entered  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (b *cancelBackend) Query(ctx context.Context, _ query.Plan) (db.Rows, error) {
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	close(b.canceled)
	return nil, ctx.Err()
}

func (*cancelBackend) Insert(context.Context, query.InsertPlan) (int64, error) {
	return 0, errors.New("unexpected insert")
}

func (*cancelBackend) Update(context.Context, query.UpdatePlan) (int64, error) {
	return 0, errors.New("unexpected update")
}

func (*cancelBackend) Delete(context.Context, query.DeletePlan) (int64, error) {
	return 0, errors.New("unexpected delete")
}

var _ interface {
	db.Queryer
	db.Mutator
} = (*cancelBackend)(nil)
