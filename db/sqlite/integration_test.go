package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
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

func TestSQLiteBackendExecutesRequiredJoinAndNullableSourceKeyTrimPreIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "relation-join-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(200) NOT NULL)`,
		`CREATE TABLE "blog_post" ("id" INTEGER NOT NULL PRIMARY KEY, "title" VARCHAR(200) NOT NULL, "author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id"), "reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id"))`,
		`INSERT INTO "authors_author" ("id", "name") VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cleo')`,
		`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (10, 'Alpha', 1, 2), (11, 'Beta', 1, NULL), (12, 'Gamma', 3, 2)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("provision relation fixture: %v", err)
		}
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	namePath := integrationRelationPath(t, "blog_post", "author_id", authorName)
	idPath := integrationRelationPath(t, "blog_post", "author_id", targetID)
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID, reviewerID}).WithConditions(
		query.NewRelatedCondition(namePath, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(idPath, query.LookupExact, query.Integer(1)),
	).WithOrderings(query.NewOrdering(id, query.Ascending))

	before := backend.QueryCount()
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatalf("relation Query() error = %v", err)
	}
	var identifiers []int64
	for rows.Next() {
		var gotID, gotAuthorID int64
		var gotTitle string
		var gotReviewerID any
		if err := rows.Scan(&gotID, &gotTitle, &gotAuthorID, &gotReviewerID); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		identifiers = append(identifiers, gotID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Rows.Err() = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close() = %v", err)
	}
	if fmt.Sprint(identifiers) != "[10 11]" {
		t.Fatalf("relation identifiers = %v, want [10 11]", identifiers)
	}
	if got := backend.QueryCount() - before; got != 1 {
		t.Fatalf("relation query count = %d, want 1", got)
	}

	nullablePath, err := query.NewNullableForwardRelationIsNullPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		reviewerID,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		"id",
	)
	if err != nil {
		t.Fatalf("NewNullableForwardRelationIsNullPath() error = %v", err)
	}
	nullablePlan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID, reviewerID}).WithConditions(
		query.NewRelatedCondition(nullablePath, query.LookupIsNull, query.Boolean(true)),
	).WithOrderings(query.NewOrdering(id, query.Ascending))
	statement, arguments, err := sqlite.Compile(nullablePlan)
	if err != nil {
		t.Fatalf("Compile(nullable isnull) error = %v", err)
	}
	if strings.Contains(statement, " JOIN ") || !strings.Contains(statement, `"t0"."reviewer_id" IS NULL`) {
		t.Fatalf("nullable source-key SQL did not trim the join: %s", statement)
	}
	if len(arguments) != 0 {
		t.Fatalf("nullable source-key arguments = %#v, want empty", arguments)
	}

	before = backend.QueryCount()
	rows, err = backend.Query(ctx, nullablePlan)
	if err != nil {
		t.Fatalf("nullable source-key Query() error = %v", err)
	}
	identifiers = identifiers[:0]
	for rows.Next() {
		var gotID, gotAuthorID int64
		var gotTitle string
		var gotReviewerID any
		if err := rows.Scan(&gotID, &gotTitle, &gotAuthorID, &gotReviewerID); err != nil {
			t.Fatalf("nullable source-key Scan() error = %v", err)
		}
		identifiers = append(identifiers, gotID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("nullable source-key Rows.Err() = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("nullable source-key Rows.Close() = %v", err)
	}
	if fmt.Sprint(identifiers) != "[11]" {
		t.Fatalf("nullable source-key identifiers = %v, want [11]", identifiers)
	}
	if got := backend.QueryCount() - before; got != 1 {
		t.Fatalf("nullable source-key query count = %d, want 1", got)
	}

	invalidNullable := query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(nullablePath, query.LookupIsNull, query.Boolean(true)),
	)
	before = backend.QueryCount()
	_, err = backend.Query(ctx, invalidNullable)
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("invalid nullable source-key Query() error = %v, want invalid_plan", err)
	}
	if got := backend.QueryCount() - before; got != 0 {
		t.Fatalf("invalid nullable source-key query count = %d, want 0", got)
	}

	wrongRoot := integrationRelationPath(t, "other_post", "author_id", authorName)
	invalid := query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupExact, query.String("Ada")),
	)
	before = backend.QueryCount()
	_, err = backend.Query(ctx, invalid)
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("invalid relation Query() error = %v, want invalid_plan", err)
	}
	if got := backend.QueryCount() - before; got != 0 {
		t.Fatalf("invalid relation query count = %d, want 0", got)
	}
}

func TestSQLiteBackendExecutesReverseJoinAndRejectsRootMismatchPreIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "reverse-relation-join-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" VARCHAR(200) NOT NULL)`,
		`CREATE TABLE "blog_post" ("id" INTEGER NOT NULL PRIMARY KEY, "title" VARCHAR(200) NOT NULL, "author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id"), "reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id"))`,
		`INSERT INTO "authors_author" ("id", "name") VALUES (1, 'Ada'), (2, 'Bob'), (3, 'Cleo')`,
		`INSERT INTO "blog_post" ("id", "title", "author_id", "reviewer_id") VALUES (10, 'Alpha', 1, 2), (11, 'Beta', 1, NULL), (12, 'Gamma', 3, 2)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("provision reverse relation fixture: %v", err)
		}
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	titlePath, err := query.NewReverseRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", "posts", false,
		query.NewFieldRef("title", "title", query.FieldString, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	postIDPath, err := query.NewReverseRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", "posts", false,
		query.NewFieldRef("id", "id", query.FieldInteger, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("authors_author", []query.FieldRef{id, name}).WithConditions(
		query.NewRelatedCondition(titlePath, query.LookupExact, query.String("Alpha")),
		query.NewRelatedCondition(postIDPath, query.LookupExact, query.Integer(10)),
	).WithOrderings(query.NewOrdering(id, query.Ascending))
	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile(reverse relation) error = %v", err)
	}
	if strings.Count(statement, " INNER JOIN ") != 1 ||
		!strings.Contains(statement, `INNER JOIN "blog_post" AS "t1" ON "t0"."id" = "t1"."author_id"`) {
		t.Fatalf("reverse relation SQL did not invert and reuse the join: %s", statement)
	}
	if want := []any{"Alpha", int64(10)}; fmt.Sprint(arguments) != fmt.Sprint(want) {
		t.Fatalf("reverse relation arguments = %#v, want %#v", arguments, want)
	}

	before := backend.QueryCount()
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatalf("reverse relation Query() error = %v", err)
	}
	var identifiers []int64
	for rows.Next() {
		var gotID int64
		var gotName string
		if err := rows.Scan(&gotID, &gotName); err != nil {
			t.Fatalf("reverse relation Scan() error = %v", err)
		}
		identifiers = append(identifiers, gotID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reverse relation Rows.Err() = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("reverse relation Rows.Close() = %v", err)
	}
	if fmt.Sprint(identifiers) != "[1]" {
		t.Fatalf("reverse relation identifiers = %v, want [1]", identifiers)
	}
	if got := backend.QueryCount() - before; got != 1 {
		t.Fatalf("reverse relation query count = %d, want 1", got)
	}

	wrongRoot, err := query.NewReverseRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"other_author", "id", "posts", false,
		query.NewFieldRef("title", "title", query.FieldString, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	invalid := query.NewPlan("authors_author", []query.FieldRef{id, name}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupExact, query.String("Alpha")),
	)
	before = backend.QueryCount()
	_, err = backend.Query(ctx, invalid)
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("invalid reverse relation Query() error = %v, want invalid_plan", err)
	}
	if got := backend.QueryCount() - before; got != 0 {
		t.Fatalf("invalid reverse relation query count = %d, want 0", got)
	}
}

func integrationRelationPath(t *testing.T, sourceTable, sourceColumn string, terminal query.FieldRef) query.RelationPath {
	t.Helper()
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		sourceTable, "author", sourceColumn,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
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
