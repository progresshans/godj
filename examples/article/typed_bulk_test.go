package article_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

// The existing PostgreSQL and SQLite HTTP owners both call these helpers.
// Every failure observes durable rows through the original backend afterwards.
func verifyArticleTypedBulk(t *testing.T, native articleapp.Backend) {
	t.Helper()
	repository, err := articleapp.NewRepository(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"created", "invalid_last", "duplicate_batch", "duplicate_existing", "native_later_conflict", "permission", "missing_callback", "second_batch_error", "reload_error", "output_failure", "swallowed_failure", "commit_unknown"} {
		t.Run(mode, func(t *testing.T) {
			original, err := repository.List(t.Context(), articleapp.ListOptions{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			old := map[int64]bool{}
			for _, row := range original.Articles {
				old[row.ID] = true
			}
			t.Cleanup(func() {
				page, err := repository.List(context.Background(), articleapp.ListOptions{Limit: 100})
				if err != nil {
					t.Error(err)
					return
				}
				for _, row := range page.Articles {
					if !old[row.ID] {
						if _, err := repository.Delete(context.Background(), row.ID); err != nil {
							t.Error(err)
						}
					}
				}
			})
			rows := make([]map[string]any, 40)
			for i := range rows {
				rows[i] = map[string]any{"title": fmt.Sprintf("  bulk %02d  ", i), "slug": fmt.Sprintf("bulk-%02d", i)}
			}
			delete(rows[0], "slug")
			rows[1]["slug"] = nil
			rows[0]["summary"], rows[0]["published"] = "summary", true
			if mode == "invalid_last" {
				rows[39]["title"] = false
			}
			if mode == "duplicate_batch" {
				rows[39]["slug"] = rows[2]["slug"]
			}
			seed := int64(0)
			if mode == "duplicate_existing" || mode == "native_later_conflict" {
				slug := "bulk-39"
				if _, err := repository.Create(t.Context(), articleapp.Input{Title: "existing bulk slug", Slug: &slug}); err != nil {
					t.Fatal(err)
				}
				seed = 1
			}
			wire, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			backend := &articleBulkHTTPBackend{Backend: native, mode: mode}
			fixture := newArticleAPIBearerFixture(t, backend)
			authorization := "Bearer " + articleAPIFullBearer
			status := 500
			switch mode {
			case "created":
				status = 201
			case "invalid_last", "duplicate_batch", "duplicate_existing", "native_later_conflict":
				status = 400
			case "permission":
				status = 403
				authorization = "Bearer " + articleAPIViewerBearer
				wire = []byte("{")
			}
			response := fixture.request(t, articleAPIBearerRequest{method: http.MethodPost, target: apiapp.BulkCreatePath, contentType: api.JSONContentType, body: string(wire), authorization: authorization})
			if response.status != status || strings.Contains(response.body, "private") {
				t.Fatal("bulk HTTP outcome", response.status, response.body)
			}
			page, err := repository.List(t.Context(), articleapp.ListOptions{Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			want := original.Total + seed
			if mode == "created" || mode == "commit_unknown" {
				want += 40
			}
			if page.Total != want {
				t.Fatal("failed bulk committed partial rows", page.Total, want)
			}
			if mode == "created" {
				verifyArticleBulkRows(t, repository, response.body, 40)
			}
			if mode == "invalid_last" || mode == "permission" {
				if backend.transactions.Load() != 0 || backend.reads.Load() != 0 || backend.bulks.Load() != 0 {
					t.Fatal("denied/invalid bulk reached I/O")
				}
			}
			if mode == "duplicate_batch" || mode == "duplicate_existing" {
				if backend.bulks.Load() != 0 || !strings.Contains(response.body, `"key":"index","value":"39"`) {
					t.Fatal("complete unique preflight or index lost", response.body)
				}
			}
			if mode == "created" || mode == "commit_unknown" || mode == "native_later_conflict" || mode == "second_batch_error" || mode == "reload_error" || mode == "output_failure" || mode == "swallowed_failure" {
				if backend.transactions.Load() != 1 || backend.bulks.Load() != 2 || backend.insertedRows.Load() != 40 || backend.savepoints.Load() != 1 {
					t.Fatal("bulk did not retain both native batches in one owner", backend.transactions.Load(), backend.bulks.Load(), backend.insertedRows.Load(), backend.savepoints.Load())
				}
			}
			fixture.requireSecretsAbsent(t)
		})
	}
	for _, mode := range []string{"prepare_budget", "hook_failure"} {
		t.Run(mode, func(t *testing.T) {
			before := articleAPIArticleCount(t, repository)
			limits := serializers.Limits{}
			if mode == "prepare_budget" {
				limits.MaxDocumentBytes = 4
			}
			response, err := output.New(output.Array(output.String(), 1, 40), limits)
			if err != nil {
				t.Fatal(err)
			}
			hooks, prepares := 0, 0
			private := errors.New("private bulk hook failure")
			hooked := repository.WithMutationHook(func(context.Context, db.Session, articleapp.MutationResult) error { hooks++; return private })
			values := make([]articleapp.Input, 40)
			for i := range values {
				values[i].Title = fmt.Sprintf("prepared bulk %02d", i)
			}
			result, err := articleapp.BulkCreateAndPrepare(t.Context(), hooked, values, func(ctx context.Context, rows []articleapp.Article) (output.Prepared[[]string], error) {
				prepares++
				if len(rows) != 40 {
					t.Fatal("partial rows reached prepare")
				}
				titles := make([]string, len(rows))
				for i, row := range rows {
					titles[i] = row.Title
				}
				return response.Prepare(ctx, 201, titles)
			})
			if err == nil || prepares != 1 || mode == "prepare_budget" && hooks != 0 || mode == "hook_failure" && (hooks != 1 || !errors.Is(err, private)) {
				t.Fatal("preparation/hook failure boundary", err, prepares, hooks)
			}
			if _, err := response.Response(result); err == nil {
				t.Fatal("failed write returned a prepared result")
			}
			if after := articleAPIArticleCount(t, repository); after != before {
				t.Fatal("failed preparation/hook committed rows", before, after)
			}
		})
	}
}

func verifyArticleBulkSession(t *testing.T, fixture articleAPIAdminSessionFixture, viewer *http.Client, csrf, viewerCSRF string) {
	t.Helper()
	before := articleAPIArticleCount(t, fixture.repository)
	for _, mode := range []string{"csrf", "permission", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			client, token := fixture.client, csrf
			switch mode {
			case "csrf":
				token = ""
			case "permission":
				client, token = viewer, viewerCSRF
			case "anonymous":
				client = fixture.newClient(t)
				token = ""
			}
			result := fixture.request(t, client, http.MethodPost, apiapp.BulkCreatePath, api.JSONContentType, "{", token)
			if result.status != 403 || articleAPIArticleCount(t, fixture.repository) != before {
				t.Fatal("session bulk admission failed", result.status, result.body)
			}
		})
	}
	t.Run("created", func(t *testing.T) {
		rows := make([]string, 40)
		for i := range rows {
			rows[i] = fmt.Sprintf(`{"title":"  bulk %02d  "}`, i)
		}
		rows[0] = `{"title":"bulk 00","summary":"summary","published":true}`
		result := fixture.request(t, fixture.client, http.MethodPost, apiapp.BulkCreatePath, api.JSONContentType, "["+strings.Join(rows, ",")+"]", csrf)
		if result.status != 201 {
			t.Fatal("session bulk creation", result.status, result.body)
		}
		ids := verifyArticleBulkRows(t, fixture.repository, result.body, 40)
		if articleAPIArticleCount(t, fixture.repository) != before+40 {
			t.Fatal("session bulk partial persistence")
		}
		for _, id := range ids {
			if _, err := fixture.repository.Delete(t.Context(), id); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("invalid_last", func(t *testing.T) {
		result := fixture.request(t, fixture.client, http.MethodPost, apiapp.BulkCreatePath, api.JSONContentType, `[{"title":"must roll back"},{}]`, csrf)
		if result.status != 400 || !strings.Contains(result.body, `"key":"index","value":"1"`) || articleAPIArticleCount(t, fixture.repository) != before {
			t.Fatal("session bulk row validation", result.status, result.body)
		}
	})
}

func verifyArticleBulkRows(t *testing.T, repository articleapp.Repository, body string, count int) []int64 {
	t.Helper()
	var rows []articleapp.Article
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != count {
		t.Fatal("bulk result count", len(rows))
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		stored, found, err := repository.Get(t.Context(), row.ID)
		if err != nil || !found {
			t.Fatal("response row missing", row.ID, err)
		}
		if row.Title != fmt.Sprintf("bulk %02d", i) || stored.Title != row.Title || row.Published != (i == 0) || stored.Published != row.Published || i > 0 && rows[i-1].ID >= row.ID {
			t.Fatal("bulk cleaning/default/order or committed row differs", i)
		}
		if i == 0 {
			if row.Summary == nil || stored.Summary == nil || *row.Summary != "summary" || *stored.Summary != "summary" {
				t.Fatal("summary did not round-trip")
			}
		} else if row.Summary != nil || stored.Summary != nil {
			t.Fatal("missing summary did not become null")
		}
		if i < 2 && (row.Slug != nil || stored.Slug != nil) {
			t.Fatal("omitted/null slug did not remain null")
		}
		if (row.Slug == nil) != (stored.Slug == nil) || row.Slug != nil && *row.Slug != *stored.Slug {
			t.Fatal("slug output differs from stored row")
		}
		ids[i] = row.ID
	}
	return ids
}

type articleBulkHTTPBackend struct {
	articleapp.Backend
	mode                                                 string
	transactions, reads, bulks, insertedRows, savepoints atomic.Int64
}

func (backend *articleBulkHTTPBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.reads.Add(1)
	return backend.Backend.Query(ctx, plan)
}
func (backend *articleBulkHTTPBackend) Atomic(ctx context.Context, run func(db.Session) error) error {
	backend.transactions.Add(1)
	if backend.mode == "missing_callback" {
		return nil
	}
	err := backend.Backend.Atomic(ctx, func(session db.Session) error { return run(articleBulkHTTPSession{Session: session, owner: backend}) })
	if backend.mode == "swallowed_failure" {
		return nil
	}
	if err == nil && backend.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	return err
}

type articleBulkHTTPSession struct {
	db.Session
	owner *articleBulkHTTPBackend
}

func (session articleBulkHTTPSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session articleBulkHTTPSession) Savepoint(ctx context.Context, run func(db.Session) error) error {
	session.owner.savepoints.Add(1)
	return db.WithSavepoint(ctx, session.Session, func(child db.Session) error { return run(articleBulkHTTPSession{Session: child, owner: session.owner}) })
}
func (session articleBulkHTTPSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	return session.Session.(db.BulkInserter).BulkInsertLimits(ctx)
}
func (session articleBulkHTTPSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	owner := session.owner
	batch := owner.bulks.Add(1)
	owner.insertedRows.Add(int64(len(plan.Rows())))
	if batch == 2 && owner.mode == "second_batch_error" {
		return db.BulkInsertResult{}, errors.New("private later Article batch failure")
	}
	return session.Session.(db.BulkInserter).BulkInsert(ctx, plan)
}
func (session articleBulkHTTPSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	owner := session.owner
	owner.reads.Add(1)
	if owner.bulks.Load() == 0 && owner.mode == "native_later_conflict" {
		return articleBulkHTTPEmptyRows{}, nil
	}
	if owner.bulks.Load() > 0 && (owner.mode == "reload_error" || owner.mode == "swallowed_failure") {
		return nil, errors.New("private bulk reload failure")
	}
	rows, err := session.Session.Query(ctx, plan)
	if err == nil && owner.bulks.Load() > 0 && owner.mode == "output_failure" && plan.ResultShape().Kind() != query.ResultProjection {
		return typedArticleInvalidOutputRows{rows}, nil
	}
	return rows, err
}

type articleBulkHTTPEmptyRows struct{}

func (articleBulkHTTPEmptyRows) Next() bool        { return false }
func (articleBulkHTTPEmptyRows) Scan(...any) error { return errors.New("empty bulk rows") }
func (articleBulkHTTPEmptyRows) Err() error        { return nil }
func (articleBulkHTTPEmptyRows) Close() error      { return nil }
