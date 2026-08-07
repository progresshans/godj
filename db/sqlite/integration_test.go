package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
)

func TestArticleQueryVerticalSlice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabase(t, ctx)
	base := models.ArticleObjects.Using(backend)

	exact, err := base.
		Filter(models.ArticleFields.Title.Exact("Alpine Guide")).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("exact All() error = %v", err)
	}
	assertIDs(t, exact, 1)

	icontains, err := base.
		Filter(models.ArticleFields.Title.IContains("django")).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("icontains All() error = %v", err)
	}
	assertIDs(t, icontains, 2, 3)

	chained, err := base.
		Filter(models.ArticleFields.Title.IContains("django")).
		Filter(models.ArticleFields.Published.Exact(true)).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("chained All() error = %v", err)
	}
	assertIDs(t, chained, 3)

	ordered := base.OrderBy(models.ArticleFields.Title.Desc(), models.ArticleFields.ID.Asc())
	ordered, err = ordered.Limit(2)
	if err != nil {
		t.Fatalf("Limit() error = %v", err)
	}
	limited, err := ordered.All(ctx)
	if err != nil {
		t.Fatalf("limited All() error = %v", err)
	}
	assertIDs(t, limited, 2, 4)

	nullRows, err := base.
		Filter(models.ArticleFields.Summary.IsNull(true)).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("isnull=true All() error = %v", err)
	}
	assertIDs(t, nullRows, 1, 4)
	for _, article := range nullRows {
		if article.Summary != nil {
			t.Fatalf("article %d summary = %q, want nil", article.ID, *article.Summary)
		}
	}

	nonNullRows, err := base.
		Filter(models.ArticleFields.Summary.IsNull(false)).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("isnull=false All() error = %v", err)
	}
	assertIDs(t, nonNullRows, 2, 3)
	if nonNullRows[0].Summary == nil || *nonNullRows[0].Summary != "ORM" {
		t.Fatalf("first non-null summary = %#v", nonNullRows[0].Summary)
	}
	if nonNullRows[1].Summary == nil || *nonNullRows[1].Summary != "" {
		t.Fatalf("empty summary = %#v", nonNullRows[1].Summary)
	}
	if nonNullRows[0].Summary == nonNullRows[1].Summary {
		t.Fatal("separate rows share a nullable string pointer")
	}

	nonNullableIsNull, err := base.
		Filter(models.ArticleFields.Published.IsNull(true)).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("null=False isnull=true All() error = %v", err)
	}
	assertIDs(t, nonNullableIsNull)
	nonNullableIsNotNull, err := base.
		Filter(models.ArticleFields.Published.IsNull(false)).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(ctx)
	if err != nil {
		t.Fatalf("null=False isnull=false All() error = %v", err)
	}
	assertIDs(t, nonNullableIsNotNull, 1, 2, 3, 4)
}

func TestSQLiteBackendPropagatesCanceledContextAndRemainsUsable(t *testing.T) {
	t.Parallel()

	background := context.Background()
	backend := openArticleDatabase(t, background)
	canceled, cancel := context.WithCancel(background)
	cancel()
	_, err := models.ArticleObjects.Using(backend).All(canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("All() error = %v, want context.Canceled", err)
	}

	rows, err := models.ArticleObjects.Using(backend).
		OrderBy(models.ArticleFields.ID.Asc()).
		All(background)
	if err != nil {
		t.Fatalf("All() after cancellation error = %v", err)
	}
	assertIDs(t, rows, 1, 2, 3, 4)
}

func TestSQLiteBackendInterruptsRunningStatement(t *testing.T) {
	t.Parallel()

	background := context.Background()
	backend, err := sqlite.OpenMemory(background, "running-cancellation")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(background, 20*time.Millisecond)
	defer cancel()
	_, err = backend.ExecContext(ctx, `
WITH RECURSIVE numbers(value) AS (
  SELECT 1
  UNION ALL
  SELECT value + 1 FROM numbers WHERE value < 100000000
)
SELECT sum(value) FROM numbers`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("long statement error = %v, want context deadline", err)
	}

	version, err := backend.SQLiteVersion(background)
	if err != nil {
		t.Fatalf("SQLiteVersion() after cancellation error = %v", err)
	}
	if version == "" {
		t.Fatal("SQLiteVersion() returned empty version")
	}
	if version != "3.53.3" {
		t.Fatalf("SQLiteVersion() = %q, want pinned 3.53.3", version)
	}
	t.Logf("backend=%s sqlite=%s", sqlite.DriverModule, version)
}

func TestSQLiteBackendCloseIsConcurrentSafeAndIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "concurrent-close")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, _ = backend.SQLiteVersion(ctx)
		}()
	}
	close(start)
	if err := backend.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	group.Wait()
	if err := backend.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := backend.SQLiteVersion(ctx); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("SQLiteVersion() after close error = %v, want invalid_plan", err)
	}
}

func TestSQLiteBackendQueryAndCloseDoNotRace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabaseWithoutCleanup(t, ctx, "query-close")
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, _ = models.ArticleObjects.Using(backend).
				OrderBy(models.ArticleFields.ID.Asc()).
				All(ctx)
		}()
	}
	close(start)
	if err := backend.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	group.Wait()
}

func openArticleDatabase(t *testing.T, ctx context.Context) *sqlite.Backend {
	t.Helper()
	backend := openArticleDatabaseWithoutCleanup(t, ctx, "article-"+t.Name())
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return backend
}

func openArticleDatabaseWithoutCleanup(t *testing.T, ctx context.Context, name string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.OpenMemory(ctx, name)
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	statements := []string{
		`CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, 'Alpine Guide', TRUE, NULL),
  (2, 'django Tips', FALSE, 'ORM'),
  (3, 'Django Deep Dive', TRUE, ''),
  (4, 'Other', TRUE, NULL)`,
	}
	for _, statement := range statements {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("provision SQLite database: %v", err)
		}
	}
	return backend
}

func assertIDs(t *testing.T, articles []models.Article, want ...int64) {
	t.Helper()
	got := make([]int64, len(articles))
	for index := range articles {
		got[index] = articles[index].ID
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("article IDs = %v, want %v", got, want)
	}
}
