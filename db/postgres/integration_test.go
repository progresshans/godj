package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPostgreSQLPhase1Integration(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL integration database: %v", redactConnectionError(err))
	}
	schema := fmt.Sprintf("godj_phase1_%d", time.Now().UnixNano())
	quotedSchema, err := quoteIdentifier(schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL integration admin connection: %v", err)
		}
	})

	createPhase1Tables(t, ctx, admin, quotedSchema)
	backend, err := Open(ctx, Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if backend.profile.versionNumber/10000 != CurrentServerMajor ||
		backend.profile.timezone != "UTC" || backend.profile.searchPath != "pg_catalog" ||
		backend.profile.clientEncoding != "UTF8" || backend.profile.serverEncoding != "UTF8" ||
		backend.profile.standardConformingStrings != "on" || backend.profile.databaseEncoding != "UTF8" ||
		backend.profile.databaseLocaleProvider != "c" || backend.profile.databaseLocale.Valid ||
		backend.profile.databaseCollation != "C" || backend.profile.databaseCType != "C" ||
		backend.schema != schema {
		t.Fatalf("profile = %#v schema=%q", backend.profile, backend.schema)
	}

	fields := phase1Fields()
	adaID := integrationInsert(t, ctx, backend, query.NewInsertPlanReturningKey(
		"authors_author",
		[]query.Assignment{query.NewAssignment(fields.authorName, query.String("Ada"))},
		fields.id,
	))
	bobID := integrationInsert(t, ctx, backend, query.NewInsertPlanReturningKey(
		"authors_author",
		[]query.Assignment{query.NewAssignment(fields.authorName, query.String("Bob"))},
		fields.id,
	))
	postID := integrationInsert(t, ctx, backend, phase1PostInsert(fields, "Hello 50%_Go", true, adaID, &bobID))
	secondPostID := integrationInsert(t, ctx, backend, phase1PostInsert(fields, "Second", false, adaID, nil))

	t.Run("scalar query update and delete", func(t *testing.T) {
		plan := query.NewPlan("blog_post", []query.FieldRef{
			fields.id, fields.title, fields.published, fields.summary, fields.authorKey, fields.editorKey,
		}).WithConditions(
			query.NewCondition(fields.title, query.LookupIContains, query.String("50%_")),
			query.NewCondition(fields.published, query.LookupExact, query.Boolean(true)),
		).WithOrderings(query.NewOrdering(fields.id, query.Ascending))
		rows, err := backend.Query(ctx, plan)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("query returned no row: %v", rows.Err())
		}
		var gotID, gotAuthor int64
		var gotTitle string
		var gotPublished bool
		var gotSummary sql.NullString
		var gotEditor sql.NullInt64
		if err := rows.Scan(&gotID, &gotTitle, &gotPublished, &gotSummary, &gotAuthor, &gotEditor); err != nil {
			t.Fatal(err)
		}
		if gotID != postID || gotTitle != "Hello 50%_Go" || !gotPublished || gotSummary.Valid ||
			gotAuthor != adaID || !gotEditor.Valid || gotEditor.Int64 != bobID || rows.Next() {
			t.Fatalf("scalar row = id:%d title:%q published:%t summary:%#v author:%d editor:%#v", gotID, gotTitle, gotPublished, gotSummary, gotAuthor, gotEditor)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		updated, err := backend.Update(ctx, query.NewUpdatePlan(
			"blog_post",
			[]query.Assignment{query.NewAssignment(fields.summary, query.String("updated"))},
			fields.id,
			query.Integer(postID),
		))
		if err != nil || updated != 1 {
			t.Fatalf("Update() = (%d, %v)", updated, err)
		}
		deleted, err := backend.Delete(ctx, query.NewDeletePlan("blog_post", fields.id, query.Integer(secondPostID)))
		if err != nil || deleted != 1 {
			t.Fatalf("Delete() = (%d, %v)", deleted, err)
		}
	})

	t.Run("forward reverse and eager relation reads", func(t *testing.T) {
		forward, err := query.NewForwardRelationPath(
			ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			"blog_post", "author", "author_id",
			ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			"authors_author", "id", false, fields.authorName,
		)
		if err != nil {
			t.Fatal(err)
		}
		forwardRows, err := backend.Query(ctx, query.NewPlan("blog_post", []query.FieldRef{fields.id, fields.title, fields.authorKey}).WithConditions(
			query.NewRelatedCondition(forward, query.LookupExact, query.String("Ada")),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got := collectForwardIDs(t, forwardRows); len(got) != 1 || got[0] != postID {
			t.Fatalf("forward IDs = %v", got)
		}

		reverse, err := query.NewReverseRelationPath(
			ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			"blog_post", "author", "author_id",
			ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			"authors_author", "id", "posts", false, fields.title,
		)
		if err != nil {
			t.Fatal(err)
		}
		reverseRows, err := backend.Query(ctx, query.NewPlan("authors_author", []query.FieldRef{fields.id, fields.authorName}).WithConditions(
			query.NewRelatedCondition(reverse, query.LookupExact, query.String("Hello 50%_Go")),
		))
		if err != nil {
			t.Fatal(err)
		}
		if got := collectTwoColumnIDs(t, reverseRows); len(got) != 1 || got[0] != adaID {
			t.Fatalf("reverse IDs = %v", got)
		}

		projection, err := query.NewForwardRelationProjection(
			ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
			"blog_post", fields.editorKey,
			ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			"authors_author", fields.id,
			[]query.FieldRef{fields.id, fields.authorName},
		)
		if err != nil {
			t.Fatal(err)
		}
		projectionPlan, err := query.NewPlan("blog_post", []query.FieldRef{fields.id, fields.title, fields.editorKey}).
			WithConditions(query.NewCondition(fields.id, query.LookupExact, query.Integer(postID))).
			WithRelationProjection(projection)
		if err != nil {
			t.Fatal(err)
		}
		projectionRows, err := backend.Query(ctx, projectionPlan)
		if err != nil {
			t.Fatal(err)
		}
		defer projectionRows.Close()
		if !projectionRows.Next() {
			t.Fatalf("projection returned no row: %v", projectionRows.Err())
		}
		var rootID int64
		var rootTitle, editorName string
		var editorKey, editorID int64
		if err := projectionRows.Scan(&rootID, &rootTitle, &editorKey, &editorID, &editorName); err != nil {
			t.Fatal(err)
		}
		if rootID != postID || rootTitle != "Hello 50%_Go" || editorKey != bobID || editorID != bobID || editorName != "Bob" {
			t.Fatalf("projection = %d %q %d %d %q", rootID, rootTitle, editorKey, editorID, editorName)
		}
	})

	t.Run("generated key and SQLSTATE failures", func(t *testing.T) {
		_, err := backend.Insert(ctx, query.NewInsertPlan(
			"authors_author",
			[]query.Assignment{query.NewAssignment(fields.authorName, query.String("missing returning"))},
		))
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
			t.Fatalf("missing returning error = %v", err)
		}
		_, err = backend.Insert(ctx, query.NewInsertPlanReturningKey(
			"authors_author",
			[]query.Assignment{
				query.NewAssignment(fields.id, query.Integer(999)),
				query.NewAssignment(fields.authorName, query.String("explicit key")),
			},
			fields.id,
		))
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
			t.Fatalf("explicit key error = %v", err)
		}
		_, err = backend.Insert(ctx, phase1PostInsert(fields, "missing relation", false, 999999, nil))
		if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectMissing}) {
			t.Fatalf("foreign-key error = %v", err)
		}
	})

	t.Run("atomic lifecycle", func(t *testing.T) {
		var retained db.Session
		committedTitle := "atomic committed"
		if err := backend.Atomic(ctx, func(session db.Session) error {
			retained = session
			_, err := session.Insert(ctx, phase1PostInsert(fields, committedTitle, false, adaID, nil))
			return err
		}); err != nil {
			t.Fatalf("Atomic(commit) error = %v", err)
		}
		if count := integrationTitleCount(t, ctx, backend, fields, committedTitle); count != 1 {
			t.Fatalf("committed count = %d", count)
		}
		if _, err := retained.Insert(ctx, phase1PostInsert(fields, "expired", false, adaID, nil)); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("expired session error = %v", err)
		}

		rollbackSignal := errors.New("rollback requested")
		rolledBackTitle := "atomic rolled back"
		err := backend.Atomic(ctx, func(session db.Session) error {
			if _, err := session.Insert(ctx, phase1PostInsert(fields, rolledBackTitle, false, adaID, nil)); err != nil {
				return err
			}
			return rollbackSignal
		})
		if !errors.Is(err, rollbackSignal) || integrationTitleCount(t, ctx, backend, fields, rolledBackTitle) != 0 {
			t.Fatalf("rollback error = %v", err)
		}

		atomicCtx, atomicCancel := context.WithCancel(ctx)
		canceledTitle := "atomic canceled"
		err = backend.Atomic(atomicCtx, func(session db.Session) error {
			if _, err := session.Insert(atomicCtx, phase1PostInsert(fields, canceledTitle, false, adaID, nil)); err != nil {
				return err
			}
			atomicCancel()
			return nil
		})
		if !errors.Is(err, context.Canceled) || integrationTitleCount(t, ctx, backend, fields, canceledTitle) != 0 {
			t.Fatalf("canceled atomic error = %v", err)
		}
	})

	t.Run("concurrent queries and close", func(t *testing.T) {
		plan := query.NewPlan("authors_author", []query.FieldRef{fields.id, fields.authorName}).
			WithOrderings(query.NewOrdering(fields.id, query.Ascending))
		const workers = 24
		var wait sync.WaitGroup
		errorsSeen := make(chan error, workers)
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				rows, err := backend.Query(ctx, plan)
				if err != nil {
					errorsSeen <- err
					return
				}
				defer rows.Close()
				count := 0
				for rows.Next() {
					var id int64
					var name string
					if err := rows.Scan(&id, &name); err != nil {
						errorsSeen <- err
						return
					}
					count++
				}
				if err := rows.Err(); err != nil {
					errorsSeen <- err
					return
				}
				if count != 2 {
					errorsSeen <- fmt.Errorf("author count = %d", count)
				}
			}()
		}
		wait.Wait()
		close(errorsSeen)
		for err := range errorsSeen {
			t.Errorf("concurrent query: %v", err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Query(ctx, plan); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
			t.Fatalf("post-close query error = %v", err)
		}
	})
}

type integrationPhase1Fields struct {
	id         query.FieldRef
	authorName query.FieldRef
	title      query.FieldRef
	published  query.FieldRef
	summary    query.FieldRef
	authorKey  query.FieldRef
	editorKey  query.FieldRef
}

func phase1Fields() integrationPhase1Fields {
	return integrationPhase1Fields{
		id:         query.NewFieldRef("id", "id", query.FieldInteger, false),
		authorName: query.NewFieldRef("name", "name", query.FieldString, false),
		title:      query.NewFieldRef("title", "title", query.FieldString, false),
		published:  query.NewFieldRef("published", "published", query.FieldBoolean, false),
		summary:    query.NewFieldRef("summary", "summary", query.FieldString, true),
		authorKey:  query.NewFieldRef("author", "author_id", query.FieldInteger, false),
		editorKey:  query.NewFieldRef("editor", "editor_id", query.FieldInteger, true),
	}
}

func phase1PostInsert(fields integrationPhase1Fields, title string, published bool, authorID int64, editorID *int64) query.InsertPlan {
	editor := query.Null()
	if editorID != nil {
		editor = query.Integer(*editorID)
	}
	return query.NewInsertPlanReturningKey(
		"blog_post",
		[]query.Assignment{
			query.NewAssignment(fields.title, query.String(title)),
			query.NewAssignment(fields.published, query.Boolean(published)),
			query.NewAssignment(fields.summary, query.Null()),
			query.NewAssignment(fields.authorKey, query.Integer(authorID)),
			query.NewAssignment(fields.editorKey, editor),
		},
		fields.id,
	)
}

func createPhase1Tables(t *testing.T, ctx context.Context, admin *pgx.Conn, schema string) {
	t.Helper()
	statements := []string{
		`CREATE TABLE ` + schema + `."authors_author" (
			"id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			"name" VARCHAR(100) NOT NULL
		)`,
		`CREATE TABLE ` + schema + `."blog_post" (
			"id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			"title" VARCHAR(100) NOT NULL,
			"published" BOOLEAN NOT NULL,
			"summary" VARCHAR(100) NULL,
			"author_id" BIGINT NOT NULL REFERENCES ` + schema + `."authors_author" ("id"),
			"editor_id" BIGINT NULL REFERENCES ` + schema + `."authors_author" ("id")
		)`,
	}
	for _, statement := range statements {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("create Phase 1 table: %v", err)
		}
	}
}

func integrationInsert(t *testing.T, ctx context.Context, mutator db.Mutator, plan query.InsertPlan) int64 {
	t.Helper()
	identifier, err := mutator.Insert(ctx, plan)
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if identifier <= 0 {
		t.Fatalf("Insert() identifier = %d", identifier)
	}
	return identifier
}

func collectForwardIDs(t *testing.T, rows db.Rows) []int64 {
	t.Helper()
	defer rows.Close()
	var identifiers []int64
	for rows.Next() {
		var identifier int64
		var text string
		var relationID int64
		if err := rows.Scan(&identifier, &text, &relationID); err != nil {
			t.Fatal(err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return identifiers
}

func collectTwoColumnIDs(t *testing.T, rows db.Rows) []int64 {
	t.Helper()
	defer rows.Close()
	var identifiers []int64
	for rows.Next() {
		var identifier int64
		var text string
		if err := rows.Scan(&identifier, &text); err != nil {
			t.Fatal(err)
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return identifiers
}

func integrationTitleCount(t *testing.T, ctx context.Context, backend *Backend, fields integrationPhase1Fields, title string) int {
	t.Helper()
	rows, err := backend.Query(ctx, query.NewPlan("blog_post", []query.FieldRef{fields.id, fields.title}).WithConditions(
		query.NewCondition(fields.title, query.LookupExact, query.String(title)),
	))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var storedTitle string
		if err := rows.Scan(&id, &storedTitle); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func postgresIntegrationURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("GODJ_TEST_POSTGRES_URL")
	if value != "" {
		return value
	}
	if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
		t.Fatal("GODJ_REQUIRE_POSTGRES=1 requires GODJ_TEST_POSTGRES_URL")
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; PostgreSQL integration was not run")
	return ""
}
