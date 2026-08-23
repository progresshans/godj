package article_test

import (
	"context"
	"errors"
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

func TestArticleHTTPEndToEndAndRequestLocalQueryCache(t *testing.T) {
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

	first := getBody(t, client, server.URL+webapp.ArticleListPath, http.StatusOK)
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

	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (3, 'Inserted Later', TRUE, NULL)`); err != nil {
		t.Fatal(err)
	}
	second := getBody(t, client, server.URL+webapp.ArticleListPath, http.StatusOK)
	if !strings.Contains(second, `data-article-id="3"`) || !strings.Contains(second, "Inserted Later") {
		t.Fatalf("second request reused a stale QuerySet result: %q", second)
	}
	if queries := backend.QueryCount(); queries != 2 {
		t.Fatalf("query count after two requests = %d, want 2", queries)
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
	if queries := backend.QueryCount(); queries != 2+concurrentRequests {
		t.Fatalf("query count after concurrent requests = %d, want %d", queries, 2+concurrentRequests)
	}

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
