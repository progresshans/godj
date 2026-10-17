package article_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

// Called by the existing SQLite and PostgreSQL Bearer HTTP owners. All fault
// modes wrap the real transaction and compare durable state after the response.
func verifyArticleTypedUpdate(t *testing.T, native articleapp.Backend) {
	t.Helper()
	repository, err := articleapp.NewRepository(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "PATCH"} {
		modes := []string{"success", "no_op", "malformed_existing", "malformed_missing", "invalid_id", "permission", "missing_callback", "swallowed_miss", "joined_miss", "confirmed_miss", "commit_unknown"}
		// A partial write can retain an invalid legacy field. Full replacement
		// validates/replaces every scalar, so its encode budget is probed below.
		if method == "PATCH" {
			modes = append(modes, "output_failure", "no_op_output_failure")
		}
		for _, mode := range modes {
			t.Run(method+"/"+mode, func(t *testing.T) {
				before, err := repository.Create(t.Context(), articleapp.Input{Title: "Typed update baseline"})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := repository.Delete(context.Background(), before.ID); err != nil && !errors.Is(err, articleapp.ErrNotFound) {
						t.Error(err)
					}
				}()
				backend := &typedArticleBackend{Backend: native, id: before.ID, mode: mode}
				fixture := newArticleAPIBearerFixture(t, backend)
				body, path, authorization := `{"published":true}`, fmt.Sprintf("/api/articles/%d/", before.ID), "Bearer "+articleAPIFullBearer
				if method == "PUT" {
					body = `{"title":"Typed update baseline","published":true}`
				}
				status := 500
				switch mode {
				case "success":
					status = 200
				case "no_op", "no_op_output_failure":
					body = `{}`
					if method == "PUT" {
						body = `{"title":"Typed update baseline","published":false}`
					}
					if mode == "no_op" {
						status = 200
					}
				case "malformed_existing":
					body, status = `{`, 400
				case "malformed_missing":
					path, body, status = "/api/articles/9223372036854775807/", `{`, 404
				case "invalid_id":
					path, body, status = "/api/articles/0/", `{`, 404
				case "permission":
					body, authorization, status = `{`, "Bearer "+articleAPIViewerBearer, 403
				case "confirmed_miss":
					status = 404
				}
				response := fixture.request(t, articleAPIBearerRequest{method: method, target: path, contentType: api.JSONContentType, body: body, authorization: authorization})
				if response.status != status {
					t.Fatal("typed update HTTP outcome", response.status, response.body)
				}
				if strings.Contains(response.body, "private") {
					t.Fatal("private failure escaped")
				}
				if mode == "invalid_id" || mode == "permission" {
					if backend.queries.Load()+backend.transactions.Load() != 0 {
						t.Fatal("path/admission failure reached storage")
					}
				}
				if strings.HasPrefix(mode, "malformed") && backend.transactions.Load() != 0 {
					t.Fatal("input failure opened a transaction")
				}
				stored, found, err := repository.Get(t.Context(), before.ID)
				if err != nil || !found {
					t.Fatal("failed update lost original row", err)
				}
				wantPublished := mode == "success" || mode == "commit_unknown"
				if stored.Title != before.Title || stored.Published != wantPublished || stored.Summary != nil || stored.Slug != nil {
					t.Fatal("prepare/transaction failure changed durable state", stored)
				}
				if mode == "success" || mode == "no_op" {
					var decoded articleapp.Article
					if err := json.Unmarshal([]byte(response.body), &decoded); err != nil || decoded != stored {
						t.Fatal("prepared response differs from committed row", decoded, err)
					}
				}
				if mode == "output_failure" && backend.writes.Load() != 1 {
					t.Fatal("output failure was not after DML", backend.writes.Load())
				}
				if mode == "no_op" || mode == "no_op_output_failure" {
					if backend.writes.Load() != 0 {
						t.Fatal("no-op performed DML")
					}
				}
				if mode == "commit_unknown" && (backend.writes.Load() != 1 || backend.transactions.Load() != 1) {
					t.Fatal("unknown commit retried a write")
				}
			})
		}
		t.Run(method+"/prepare_budget_rollback", func(t *testing.T) {
			before, err := repository.Create(t.Context(), articleapp.Input{Title: "Budget baseline"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := repository.Delete(context.Background(), before.ID); err != nil {
					t.Error(err)
				}
			}()
			response, err := output.New(output.String(), serializers.Limits{MaxDocumentBytes: 4})
			if err != nil {
				t.Fatal(err)
			}
			prepares, hooks := 0, 0
			hooked := repository.WithMutationHook(func(context.Context, db.Session, articleapp.MutationResult) error { hooks++; return nil })
			prepare := func(ctx context.Context, value articleapp.Article, fields []string) (output.Prepared[string], error) {
				prepares++
				if value.Title != "Budget must roll back" || fmt.Sprint(fields) != "[title]" {
					t.Error("preparation did not receive DML result")
				}
				return response.Prepare(ctx, 200, value.Title)
			}
			var prepared output.Prepared[string]
			if method == "PUT" {
				prepared, err = articleapp.UpdateAndPrepare(t.Context(), hooked, before.ID, articleapp.Input{Title: "Budget must roll back"}, prepare)
			} else {
				prepared, err = articleapp.PatchAndPrepare(t.Context(), hooked, before.ID, (articleapp.Patch{}).WithTitle("Budget must roll back"), prepare)
			}
			if err == nil || prepares != 1 || hooks != 0 {
				t.Fatal("encoding limit did not abort before hook", err, prepares, hooks)
			}
			if _, err := response.Response(prepared); err == nil {
				t.Fatal("failed preparation returned a response")
			}
			stored, found, err := repository.Get(t.Context(), before.ID)
			if err != nil || !found || stored != before {
				t.Fatal("output limit committed DML", stored, err)
			}
		})
	}
}

type typedArticleBackend struct {
	articleapp.Backend
	id                            int64
	mode                          string
	queries, transactions, writes atomic.Int64
}

func (backend *typedArticleBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	backend.queries.Add(1)
	return backend.Backend.Query(ctx, plan)
}
func (backend *typedArticleBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.transactions.Add(1)
	if backend.mode == "missing_callback" {
		return nil
	}
	err := backend.Backend.Atomic(ctx, func(session db.Session) error {
		if backend.mode == "swallowed_miss" || backend.mode == "joined_miss" || backend.mode == "wrapped_miss" || backend.mode == "confirmed_miss" {
			row := articlemodels.NewArticleWithID(backend.id)
			if _, err := articlemodels.ArticleObjects.Delete(ctx, session, &row); err != nil {
				return err
			}
		}
		return callback(typedArticleSession{Session: session, owner: backend})
	})
	if backend.mode == "swallowed_miss" {
		return nil
	}
	if backend.mode == "joined_miss" {
		return errors.Join(err, errors.New("private rollback boundary"))
	}
	if backend.mode == "wrapped_miss" {
		return fmt.Errorf("private rollback boundary: %w", err)
	}
	if err != nil && backend.mode == "delete_rollback_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	if err == nil && backend.mode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	return err
}

type typedArticleSession struct {
	db.Session
	owner *typedArticleBackend
}

func (session typedArticleSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	session.owner.queries.Add(1)
	rows, err := session.Session.Query(ctx, plan)
	if err == nil && plan.Table() == "godj_conformance_article" && plan.ResultShape().Kind() != query.ResultProjection && (session.owner.mode == "output_failure" || session.owner.mode == "no_op_output_failure") {
		return typedArticleInvalidOutputRows{rows}, nil
	}
	return rows, err
}
func (session typedArticleSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.owner.writes.Add(1)
	return session.Session.Update(ctx, plan)
}

func (session typedArticleSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session.owner.writes.Add(1)
	switch session.owner.mode {
	case "delete_error", "delete_rollback_unknown":
		return 0, errors.New("private delete failure")
	case "delete_cancel":
		return 0, context.Canceled
	case "delete_rejection":
		return 0, validation.Reject(validation.NewErrors(validation.New(validation.NonField, "protected")), nil)
	}
	return session.Session.Delete(ctx, plan)
}

type typedArticleInvalidOutputRows struct{ db.Rows }

func (rows typedArticleInvalidOutputRows) Scan(values ...any) error {
	if err := rows.Rows.Scan(values...); err != nil {
		return err
	}
	if len(values) != 5 {
		return errors.New("unexpected Article row shape")
	}
	title, ok := values[1].(*string)
	if !ok {
		return errors.New("unexpected Article title scanner")
	}
	*title = strings.Repeat("x", 201)
	return nil
}
