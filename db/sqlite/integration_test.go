package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
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

	authorIn, err := query.NewInCondition(authorID, []query.Value{
		query.Integer(3),
		query.Integer(1),
		query.Integer(2),
	})
	if err != nil {
		t.Fatalf("NewInCondition(author) error = %v", err)
	}
	inPlan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID, reviewerID}).
		WithConditions(authorIn).
		WithOrderings(query.NewOrdering(id, query.Ascending))
	inStatement, inArguments, err := sqlite.Compile(inPlan)
	if err != nil {
		t.Fatalf("Compile(root IN) error = %v", err)
	}
	if strings.Contains(inStatement, " JOIN ") || !strings.Contains(inStatement, `"author_id" IN (?, ?, ?)`) {
		t.Fatalf("root IN SQL is not join-free: %s", inStatement)
	}
	if want := []any{int64(3), int64(1), int64(2)}; fmt.Sprint(inArguments) != fmt.Sprint(want) {
		t.Fatalf("root IN arguments = %#v, want %#v", inArguments, want)
	}

	before = backend.QueryCount()
	rows, err = backend.Query(ctx, inPlan)
	if err != nil {
		t.Fatalf("root IN Query() error = %v", err)
	}
	identifiers = identifiers[:0]
	for rows.Next() {
		var gotID, gotAuthorID int64
		var gotTitle string
		var gotReviewerID any
		if err := rows.Scan(&gotID, &gotTitle, &gotAuthorID, &gotReviewerID); err != nil {
			t.Fatalf("root IN Scan() error = %v", err)
		}
		identifiers = append(identifiers, gotID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("root IN Rows.Err() = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("root IN Rows.Close() = %v", err)
	}
	if fmt.Sprint(identifiers) != "[10 11 12]" {
		t.Fatalf("root IN identifiers = %v, want [10 11 12]", identifiers)
	}
	if got := backend.QueryCount() - before; got != 1 {
		t.Fatalf("root IN query count = %d, want 1", got)
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

func TestSQLiteBackendExecutesRequiredAndNullableForwardProjections(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "relation-projection-"+t.Name())
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
			t.Fatalf("provision relation projection fixture: %v", err)
		}
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	rootColumns := []query.FieldRef{id, title, authorID, reviewerID}
	targetColumns := []query.FieldRef{targetID, targetName}

	requiredPlan := query.NewPlan("blog_post", rootColumns).WithOrderings(query.NewOrdering(id, query.Ascending))
	requiredPlan, err = requiredPlan.WithRelationProjection(forwardProjection(t, authorID, targetID, targetColumns))
	if err != nil {
		t.Fatal(err)
	}
	requiredBefore := backend.QueryCount()
	requiredRows, err := backend.Query(ctx, requiredPlan)
	if err != nil {
		t.Fatalf("required projection Query() error = %v", err)
	}
	var required []string
	for requiredRows.Next() {
		var postID, gotAuthorID, authorPrimaryKey int64
		var gotTitle, authorName string
		var gotReviewerID any
		if err := requiredRows.Scan(&postID, &gotTitle, &gotAuthorID, &gotReviewerID, &authorPrimaryKey, &authorName); err != nil {
			t.Fatalf("required projection Scan() error = %v", err)
		}
		required = append(required, fmt.Sprintf("%d:%d:%s", postID, authorPrimaryKey, authorName))
	}
	if err := requiredRows.Err(); err != nil {
		t.Fatalf("required projection Rows.Err() = %v", err)
	}
	if err := requiredRows.Close(); err != nil {
		t.Fatalf("required projection Rows.Close() = %v", err)
	}
	if fmt.Sprint(required) != "[10:1:Ada 11:1:Ada 12:3:Cleo]" {
		t.Fatalf("required projection rows = %v", required)
	}
	if got := backend.QueryCount() - requiredBefore; got != 1 {
		t.Fatalf("required projection query count = %d, want 1", got)
	}

	nullablePlan := query.NewPlan("blog_post", rootColumns).WithOrderings(query.NewOrdering(id, query.Ascending))
	nullableProjection := forwardProjection(t, reviewerID, targetID, targetColumns)
	nullablePlan, err = nullablePlan.WithRelationProjection(nullableProjection)
	if err != nil {
		t.Fatal(err)
	}
	nullableBefore := backend.QueryCount()
	nullableRows, err := backend.Query(ctx, nullablePlan)
	if err != nil {
		t.Fatalf("nullable projection Query() error = %v", err)
	}
	var nullable []string
	for nullableRows.Next() {
		var postID, gotAuthorID int64
		var gotTitle string
		var gotReviewerID, reviewerPrimaryKey, reviewerName any
		if err := nullableRows.Scan(&postID, &gotTitle, &gotAuthorID, &gotReviewerID, &reviewerPrimaryKey, &reviewerName); err != nil {
			t.Fatalf("nullable projection Scan() error = %v", err)
		}
		nullable = append(nullable, fmt.Sprintf("%d:%v:%v", postID, reviewerPrimaryKey, reviewerName))
	}
	if err := nullableRows.Err(); err != nil {
		t.Fatalf("nullable projection Rows.Err() = %v", err)
	}
	if err := nullableRows.Close(); err != nil {
		t.Fatalf("nullable projection Rows.Close() = %v", err)
	}
	if fmt.Sprint(nullable) != "[10:2:Bob 11:<nil>:<nil> 12:2:Bob]" {
		t.Fatalf("nullable projection rows = %v", nullable)
	}
	if got := backend.QueryCount() - nullableBefore; got != 1 {
		t.Fatalf("nullable projection query count = %d, want 1", got)
	}

	forgedPath, err := query.NewNullableForwardRelationIsNullPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		reviewerID,
		ir.ModelIdentity{AppLabel: "people", ModelName: "person"},
		"authors_author",
		"id",
	)
	if err != nil {
		t.Fatal(err)
	}
	forgedPlan := query.NewPlan("blog_post", rootColumns).WithConditions(
		query.NewRelatedCondition(forgedPath, query.LookupIsNull, query.Boolean(true)),
	)
	forgedPlan, err = forgedPlan.WithRelationProjection(nullableProjection)
	if err != nil {
		t.Fatal(err)
	}
	beforeForged := backend.QueryCount()
	rows, err := backend.Query(ctx, forgedPlan)
	if rows != nil {
		_ = rows.Close()
		t.Fatal("forged source-key projection returned rows")
	}
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != query.CategoryQuery || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("forged source-key projection Query() error = %v, want query_error/invalid_plan", err)
	}
	if got := backend.QueryCount() - beforeForged; got != 0 {
		t.Fatalf("forged source-key projection query count = %d, want 0", got)
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

func TestSQLiteAtomicRelationBeginImmediateRejectsNoWaitCompetingWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relation-busy.sqlite")
	backend, writer := openFileRelationDatabases(t, ctx, path, 1)
	provisionFileRelationFixture(t, ctx, backend)
	assertFileRelationForeignKeys(t, ctx, writer)

	entered := make(chan struct{})
	release := make(chan struct{})
	atomicResult := make(chan error, 1)
	go func() {
		atomicResult <- backend.AtomicRelation(ctx, func(db.RelationSession) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	_, busyErr := writer.ExecContext(ctx, `INSERT INTO "authors_author" ("id", "name") VALUES (3, 'Cleo')`)
	assertSQLiteCode(t, busyErr, sqlite3.SQLITE_BUSY)
	close(release)
	if err := <-atomicResult; err != nil {
		t.Fatalf("AtomicRelation() error = %v", err)
	}
	var rows int
	if err := writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM "authors_author" WHERE "id" = 3`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("competing writer rows = %d, want 0", rows)
	}
}

func TestSQLiteAtomicRelationBusyBeginDiscardsWithoutCallbackOrRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relation-busy-begin.sqlite")
	backend, locker := openFileRelationDatabases(t, ctx, path, 1)
	provisionFileRelationFixture(t, ctx, backend)

	connection, err := locker.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	called := 0
	err = backend.AtomicRelation(ctx, func(db.RelationSession) error {
		called++
		return nil
	})
	assertSQLiteCode(t, err, sqlite3.SQLITE_BUSY)
	if called != 0 {
		t.Fatalf("callback calls after BUSY BEGIN = %d, want 0", called)
	}
	if errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
		errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
		t.Fatalf("BUSY BEGIN error used outcome-unknown marker: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.AtomicRelation(ctx, func(db.RelationSession) error {
		called++
		return nil
	}); err != nil {
		t.Fatalf("AtomicRelation() after BUSY discard error = %v", err)
	}
	if called != 1 {
		t.Fatalf("successful callback calls = %d, want 1", called)
	}
}

func TestSQLiteAtomicRelationWaitingFKWriterCannotCreatePostCommitOrphan(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relation-fk-race.sqlite")
	backend, writer := openFileRelationDatabases(t, ctx, path, 5000)
	provisionFileRelationFixture(t, ctx, backend)
	assertFileRelationForeignKeys(t, ctx, writer)

	entered := make(chan struct{})
	release := make(chan struct{})
	atomicResult := make(chan error, 1)
	go func() {
		atomicResult <- backend.AtomicRelation(ctx, func(session db.RelationSession) error {
			close(entered)
			<-release
			setNull := query.NewRelationSetNullPlan(
				"blog_post",
				query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true),
				query.Integer(2),
			)
			if rows, err := session.RelationSetNull(ctx, setNull); err != nil || rows != 2 {
				return fmt.Errorf("SET_NULL = (%d, %v), want (2, nil)", rows, err)
			}
			deleteTarget := query.NewDeletePlan(
				"authors_author",
				query.NewFieldRef("id", "id", query.FieldInteger, false),
				query.Integer(2),
			)
			if rows, err := session.Delete(ctx, deleteTarget); err != nil || rows != 1 {
				return fmt.Errorf("Delete = (%d, %v), want (1, nil)", rows, err)
			}
			return nil
		})
	}()
	<-entered

	writerStarted := make(chan struct{})
	writerResult := make(chan error, 1)
	go func() {
		close(writerStarted)
		_, err := writer.ExecContext(ctx,
			`INSERT INTO "blog_post" ("id", "author_id", "reviewer_id") VALUES (99, 2, NULL)`,
		)
		writerResult <- err
	}()
	<-writerStarted
	select {
	case err := <-writerResult:
		t.Fatalf("waiting writer returned before relation COMMIT: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(release)
	if err := <-atomicResult; err != nil {
		t.Fatalf("AtomicRelation() error = %v", err)
	}
	var writerErr error
	select {
	case writerErr = <-writerResult:
	case <-time.After(5 * time.Second):
		t.Fatal("waiting writer did not finish after relation COMMIT")
	}
	assertSQLiteCode(t, writerErr, sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY)

	var orphanRows int
	if err := writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM "blog_post" WHERE "id" = 99`).Scan(&orphanRows); err != nil {
		t.Fatal(err)
	}
	var targetRows int
	if err := writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM "authors_author" WHERE "id" = 2`).Scan(&targetRows); err != nil {
		t.Fatal(err)
	}
	var reviewerRows int
	if err := writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM "blog_post" WHERE "reviewer_id" = 2`).Scan(&reviewerRows); err != nil {
		t.Fatal(err)
	}
	if orphanRows != 0 || targetRows != 0 || reviewerRows != 0 {
		t.Fatalf("post-race orphan/target/reviewer rows = %d/%d/%d, want 0/0/0", orphanRows, targetRows, reviewerRows)
	}
}

func openFileRelationDatabases(t *testing.T, ctx context.Context, path string, busyTimeout int) (*sqlite.Backend, *sql.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=rwc&_pragma=foreign_keys(1)&_busy_timeout=%d", filepath.ToSlash(path), busyTimeout)
	backend, err := sqlite.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = backend.Close()
		t.Fatalf("sql.Open() error = %v", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	if err := writer.PingContext(ctx); err != nil {
		_ = writer.Close()
		_ = backend.Close()
		t.Fatalf("writer PingContext() error = %v", err)
	}
	var foreignKeys int
	if err := writer.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		_ = writer.Close()
		_ = backend.Close()
		t.Fatalf("writer foreign_keys = (%d, %v), want (1, nil)", foreignKeys, err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("writer Close() error = %v", err)
		}
		if err := backend.Close(); err != nil {
			t.Errorf("backend Close() error = %v", err)
		}
	})
	return backend, writer
}

func provisionFileRelationFixture(t *testing.T, ctx context.Context, backend *sqlite.Backend) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE "authors_author" ("id" INTEGER NOT NULL PRIMARY KEY, "name" TEXT NOT NULL)`,
		`CREATE TABLE "blog_post" (` +
			`"id" INTEGER NOT NULL PRIMARY KEY, ` +
			`"author_id" INTEGER NOT NULL REFERENCES "authors_author" ("id") ON DELETE NO ACTION, ` +
			`"reviewer_id" INTEGER NULL REFERENCES "authors_author" ("id") ON DELETE NO ACTION)`,
		`INSERT INTO "authors_author" ("id", "name") VALUES (1, 'Ada'), (2, 'Bob')`,
		`INSERT INTO "blog_post" ("id", "author_id", "reviewer_id") VALUES (10, 1, 2), (11, 1, 2)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("provision relation fixture: %v", err)
		}
	}
}

func assertFileRelationForeignKeys(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	var tableSQL string
	if err := database.QueryRowContext(ctx, `SELECT "sql" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" = 'blog_post'`).Scan(&tableSQL); err != nil {
		t.Fatalf("read blog_post table definition: %v", err)
	}
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_list("blog_post")`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var (
			id, sequence                  int
			table, from, to               string
			onUpdate, onDelete, matchType string
		)
		if err := rows.Scan(&id, &sequence, &table, &from, &to, &onUpdate, &onDelete, &matchType); err != nil {
			t.Fatal(err)
		}
		if table != "authors_author" || to != "id" || sequence != 0 || onUpdate != "NO ACTION" || matchType != "NONE" {
			t.Fatalf("foreign key %q shape = table=%q to=%q seq=%d update=%q match=%q", from, table, to, sequence, onUpdate, matchType)
		}
		seen[from] = onDelete
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen["author_id"] != "NO ACTION" || seen["reviewer_id"] != "NO ACTION" {
		t.Fatalf("foreign-key delete policies = %v for definition %q", seen, tableSQL)
	}
}

func assertSQLiteCode(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("SQLite error = nil, want code %d", code)
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != code {
		t.Fatalf("SQLite error = %v (code %d), want code %d", err, func() int {
			if sqliteErr == nil {
				return 0
			}
			return sqliteErr.Code()
		}(), code)
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
