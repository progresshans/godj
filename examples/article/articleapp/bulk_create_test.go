package articleapp_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

var bulkArticlePrivateFailure = errors.New("private Article bulk boundary failure")

type bulkArticleBackend struct {
	articleapp.Backend
	mode                                string
	atomics, savepoints, reads, inserts int
	batches                             []int
	cancel                              context.CancelFunc
}

func (backend *bulkArticleBackend) Atomic(ctx context.Context, run func(db.Session) error) error {
	backend.atomics++
	if backend.mode == "skipped callback" {
		return nil
	}
	err := backend.Backend.Atomic(ctx, func(session db.Session) error {
		wrapped := &bulkArticleSession{Session: session, owner: backend}
		err := run(wrapped)
		if err == nil && backend.mode == "repeated callback" {
			return run(wrapped)
		}
		return err
	})
	if backend.mode == "swallowed callback" {
		return nil
	}
	if err == nil && backend.mode == "unknown commit" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Cause: bulkArticlePrivateFailure}
	}
	if err == nil && backend.mode == "confirmed late cancel" {
		backend.cancel()
	}
	return err
}

type bulkArticleSession struct {
	db.Session
	owner *bulkArticleBackend
}

func (session *bulkArticleSession) ValidateSession(ctx context.Context) error {
	return session.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (session *bulkArticleSession) Savepoint(ctx context.Context, run func(db.Session) error) error {
	session.owner.savepoints++
	return db.WithSavepoint(ctx, session.Session, func(child db.Session) error { return run(&bulkArticleSession{Session: child, owner: session.owner}) })
}
func (session *bulkArticleSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	return session.Session.(db.BulkInserter).BulkInsertLimits(ctx)
}
func (session *bulkArticleSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	owner := session.owner
	owner.batches = append(owner.batches, len(plan.Rows()))
	if owner.mode == "second batch error" && len(owner.batches) == 2 {
		return db.BulkInsertResult{}, bulkArticlePrivateFailure
	}
	result, err := session.Session.(db.BulkInserter).BulkInsert(ctx, plan)
	if err == nil {
		if owner.mode == "repeated returned key" && len(result.Keys) > 1 {
			result.Keys[1] = result.Keys[0]
		}
		if owner.mode == "missing returned key" {
			result.Keys = result.Keys[:len(result.Keys)-1]
		}
		if owner.mode == "cancel bulk" {
			owner.cancel()
		}
	}
	return result, err
}
func (session *bulkArticleSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	owner := session.owner
	owner.reads++
	if len(owner.batches) == 0 && owner.mode == "native later conflict" {
		return bulkArticleEmptyRows{}, nil
	}
	if len(owner.batches) > 0 && owner.mode == "reload error" {
		return nil, bulkArticlePrivateFailure
	}
	return session.Session.Query(ctx, plan)
}
func (session *bulkArticleSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.owner.inserts++
	return session.Session.Insert(ctx, plan)
}

type bulkArticleEmptyRows struct{}

func (bulkArticleEmptyRows) Next() bool        { return false }
func (bulkArticleEmptyRows) Scan(...any) error { return errors.New("empty Article rows cannot scan") }
func (bulkArticleEmptyRows) Err() error        { return nil }
func (bulkArticleEmptyRows) Close() error      { return nil }

func TestPreparedArticleBulkCreateOwnsAllBatchesReloadOutputAndHook(t *testing.T) {
	for _, mode := range []string{"created", "stored values", "duplicate batch", "duplicate empty slug", "duplicate existing", "invalid last", "native later conflict", "second batch error", "repeated returned key", "missing returned key", "reload error", "prepare error", "output limit", "hook error", "cancel prepare", "cancel hook", "cancel bulk", "nil prepare", "skipped callback", "repeated callback", "swallowed callback", "unknown commit", "confirmed late cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, native, plain := openArticleRepository(t, t.Name())
			if mode == "stored values" {
				if _, err := native.ExecContext(ctx, `CREATE TRIGGER article_bulk_stored AFTER INSERT ON godj_conformance_article BEGIN UPDATE godj_conformance_article SET title='stored ' || NEW.title WHERE id=NEW.id; END`); err != nil {
					t.Fatal(err)
				}
			}
			inputs := make([]articleapp.Input, 40)
			for index := range inputs {
				summary, slug := "original summary", fmt.Sprintf("slug-%02d", index)
				inputs[index] = articleapp.Input{Title: fmt.Sprintf("row %02d", index), Summary: &summary, Slug: &slug}
			}
			// Distinct SQL NULLs must survive the whole-candidate uniqueness check.
			inputs[0].Slug, inputs[1].Slug = nil, nil
			seedCount := int64(0)
			if mode == "duplicate existing" || mode == "native later conflict" {
				if _, err := plain.Create(ctx, inputs[39]); err != nil {
					t.Fatal(err)
				}
				seedCount = 1
			}
			if mode == "duplicate batch" {
				inputs[39].Slug = inputs[2].Slug
			}
			if mode == "duplicate empty slug" {
				empty := ""
				inputs[2].Slug, inputs[39].Slug = &empty, &empty
			}
			if mode == "invalid last" {
				inputs[39].Title = ""
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			backend := &bulkArticleBackend{Backend: native, mode: mode, cancel: cancel}
			repository, err := articleapp.NewRepository(backend)
			if err != nil {
				t.Fatal(err)
			}
			var stages []string
			repository = repository.WithMutationHook(func(work context.Context, session db.Session, mutation articleapp.MutationResult) error {
				stages = append(stages, "hook")
				if mutation.Operation != articleapp.MutationBulkCreate || len(mutation.Items) != 40 {
					t.Fatal("incomplete bulk hook", mutation.Operation, len(mutation.Items))
				}
				for index, item := range mutation.Items {
					want := fmt.Sprintf("row %02d", index)
					if mode == "stored values" {
						want = "stored " + want
					}
					if item.Article.Title != want || item.Article.Summary == nil || *item.Article.Summary != "original summary" || len(item.ChangedFields) != 0 {
						t.Fatal("prepare changed the hook's detached stored snapshot", index, item)
					}
					*item.Article.Summary = "hook changed its own snapshot"
				}
				if err := session.(db.SessionValidator).ValidateSession(work); err != nil {
					t.Fatal("hook escaped the write transaction", err)
				}
				if mode == "hook error" {
					return bulkArticlePrivateFailure
				}
				if mode == "cancel hook" {
					cancel()
				}
				return nil
			})
			limits := serializers.Limits{}
			if mode == "output limit" {
				limits.MaxDocumentBytes = 4
			}
			response, err := output.New(output.Array(output.Int64(), 1, 40), limits)
			if err != nil {
				t.Fatal(err)
			}
			prepare := func(work context.Context, rows []articleapp.Article) (output.Prepared[[]int64], error) {
				stages = append(stages, "prepare")
				if len(rows) != 40 {
					t.Fatal("partial preparation", len(rows))
				}
				ids := make([]int64, len(rows))
				for index, row := range rows {
					want := fmt.Sprintf("row %02d", index)
					if mode == "stored values" {
						want = "stored " + want
					}
					if row.Title != want || row.ID <= 0 || index > 0 && row.ID <= rows[index-1].ID {
						t.Fatal("stored reload or input order lost", index, row)
					}
					if (row.Slug == nil) != (index < 2) {
						t.Fatal("SQL null input changed", index)
					}
					ids[index] = row.ID
					*row.Summary = "prepare changed its own snapshot"
					rows[index].Title = "prepare snapshot"
				}
				prepared, err := response.Prepare(work, 201, ids)
				if mode == "prepare error" || mode == "swallowed callback" {
					return prepared, bulkArticlePrivateFailure
				}
				if mode == "cancel prepare" {
					cancel()
				}
				return prepared, err
			}
			if mode == "nil prepare" {
				prepare = nil
			}
			prepared, err := articleapp.BulkCreateAndPrepare(ctx, repository, inputs, prepare)
			ok := mode == "created" || mode == "stored values" || mode == "confirmed late cancel"
			if (err == nil) != ok {
				t.Fatal("bulk outcome", err)
			}
			encoded, encodeErr := response.Response(prepared)
			if (encodeErr == nil) != ok || !ok && encoded.Status() != 0 {
				t.Fatal("unconfirmed bulk result escaped", encodeErr)
			}
			if backend.inserts != 0 || backend.atomics > 1 || backend.savepoints > 1 {
				t.Fatal("bulk became repeated writes/transactions", backend)
			}
			if ok || mode == "prepare error" || mode == "output limit" || mode == "hook error" || mode == "unknown commit" || mode == "native later conflict" || mode == "second batch error" {
				if backend.atomics != 1 || backend.savepoints != 1 || !slices.Equal(backend.batches, []int{20, 20}) {
					t.Fatal("native batches do not share one owner", backend)
				}
			}
			if mode == "invalid last" || mode == "nil prepare" {
				if backend.atomics != 0 || backend.reads != 0 {
					t.Fatal("invalid input reached I/O")
				}
			}
			if mode == "duplicate batch" || mode == "duplicate empty slug" || mode == "duplicate existing" {
				if len(backend.batches) != 0 {
					t.Fatal("unique preflight inserted an earlier row")
				}
				diagnostics, rejected := validation.Rejected(err)
				if !rejected || diagnostics.Empty() {
					t.Fatal("preflight lost validation result", err)
				}
				found := false
				for _, failure := range diagnostics.All() {
					for _, param := range failure.Params() {
						if param.Key() == "index" && param.Value() == "39" {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("unique failure lost original row index")
				}
			}
			if mode == "native later conflict" {
				if _, rejected := validation.Rejected(err); !rejected {
					t.Fatal("native conflict lost whole-request rejection", err)
				}
			}
			if mode == "swallowed callback" || mode == "skipped callback" || mode == "repeated callback" {
				if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
					t.Fatal("bad owner completion lost unknown marker", err)
				}
			}
			if mode == "unknown commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("unknown commit was translated to validation", err)
			}
			if mode == "cancel prepare" || mode == "cancel hook" || mode == "cancel bulk" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation was hidden", err)
				}
			}
			if mode == "confirmed late cancel" && ctx.Err() == nil {
				t.Fatal("late-cancel case did not cancel")
			}
			wantStages := []string(nil)
			switch mode {
			case "created", "stored values", "hook error", "cancel hook", "unknown commit", "confirmed late cancel", "repeated callback":
				wantStages = []string{"prepare", "hook"}
			case "prepare error", "output limit", "cancel prepare", "swallowed callback":
				wantStages = []string{"prepare"}
			}
			if !slices.Equal(stages, wantStages) {
				t.Fatal("preparation/hook order", stages, wantStages)
			}
			page, readErr := plain.List(context.Background(), articleapp.ListOptions{Limit: 100})
			if readErr != nil {
				t.Fatal(readErr)
			}
			wantCount := seedCount
			if ok || mode == "unknown commit" {
				wantCount += 40
			}
			if page.Total != wantCount {
				t.Fatal("failed bulk committed partial rows", page.Total, wantCount)
			}
			for _, row := range page.Articles {
				if row.Summary == nil || *row.Summary != "original summary" {
					t.Fatal("callback mutated stored values", row)
				}
			}
			for _, input := range inputs {
				if input.Summary == nil || *input.Summary != "original summary" {
					t.Fatal("callback retained caller-owned pointers")
				}
			}
		})
	}
}

func TestPreparedArticleBulkRejectsCountAndContextBeforeIO(t *testing.T) {
	_, native, _ := openArticleRepository(t, t.Name())
	backend := &bulkArticleBackend{Backend: native}
	repository, err := articleapp.NewRepository(backend)
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(context.Context, []articleapp.Article) (string, error) {
		t.Fatal("invalid bulk reached preparation")
		return "", nil
	}
	for _, count := range []int{0, 41} {
		value, err := articleapp.BulkCreateAndPrepare(context.Background(), repository, make([]articleapp.Input, count), prepare)
		if _, rejected := validation.Rejected(err); !rejected || value != "" {
			t.Fatal("invalid count accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if value, err := articleapp.BulkCreateAndPrepare(ctx, repository, []articleapp.Input{{Title: "valid"}}, prepare); err == nil || value != "" {
			t.Fatal("invalid context accepted")
		}
	}
	if value, err := articleapp.BulkCreateAndPrepare(context.Background(), articleapp.Repository{}, []articleapp.Input{{Title: "valid"}}, prepare); err == nil || value != "" {
		t.Fatal("zero repository accepted")
	}
	if backend.atomics != 0 || backend.reads != 0 || len(backend.batches) != 0 {
		t.Fatal("invalid bulk reached I/O")
	}
}
