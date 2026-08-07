package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
)

func TestAtomicCommitRollbackAndExpiredSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabase(t, ctx)
	var committed models.Article
	var retained db.Session
	if err := backend.Atomic(ctx, func(session db.Session) error {
		retained = session
		created, err := models.ArticleObjects.Create(ctx, session, models.NewArticleCreate("Committed"))
		if err != nil {
			return err
		}
		committed = created
		return nil
	}); err != nil {
		t.Fatalf("Atomic(commit) error = %v", err)
	}
	assertArticleTitleCount(t, ctx, backend, "Committed", 1)
	if _, err := models.ArticleObjects.Create(ctx, retained, models.NewArticleCreate("Expired")); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("write through expired Session error = %v, want backend invalid_plan", err)
	}

	rollbackSignal := errors.New("rollback requested")
	err := backend.Atomic(ctx, func(session db.Session) error {
		if _, err := models.ArticleObjects.Update(ctx, session, committed, models.ArticlePatch{}.WithTitle("Rolled Back Update")); err != nil {
			return err
		}
		if _, err := models.ArticleObjects.Create(ctx, session, models.NewArticleCreate("Rolled Back Create")); err != nil {
			return err
		}
		return rollbackSignal
	})
	if !errors.Is(err, rollbackSignal) {
		t.Fatalf("Atomic(rollback) error = %v, want rollback signal", err)
	}
	assertArticleTitleCount(t, ctx, backend, "Committed", 1)
	assertArticleTitleCount(t, ctx, backend, "Rolled Back Update", 0)
	assertArticleTitleCount(t, ctx, backend, "Rolled Back Create", 0)
}

func TestAtomicNilCallbackCancellationAndPanicRollback(t *testing.T) {
	t.Parallel()

	background := context.Background()
	backend := openArticleDatabase(t, background)
	if err := backend.Atomic(background, nil); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Atomic(nil) error = %v, want query invalid_plan", err)
	}

	canceled, cancel := context.WithCancel(background)
	err := backend.Atomic(canceled, func(session db.Session) error {
		if _, err := models.ArticleObjects.Create(canceled, session, models.NewArticleCreate("Canceled Transaction")); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Atomic(canceled) error = %v, want context.Canceled", err)
	}
	assertArticleTitleCount(t, background, backend, "Canceled Transaction", 0)

	panicValue := "transaction panic"
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("recovered panic = %#v, want %#v", recovered, panicValue)
			}
		}()
		_ = backend.Atomic(background, func(session db.Session) error {
			if _, err := models.ArticleObjects.Create(background, session, models.NewArticleCreate("Panicked Transaction")); err != nil {
				t.Fatalf("Create() before panic error = %v", err)
			}
			panic(panicValue)
		})
	}()
	assertArticleTitleCount(t, background, backend, "Panicked Transaction", 0)
}

func assertArticleTitleCount(t *testing.T, ctx context.Context, backend *sqlite.Backend, title string, want int) {
	t.Helper()
	rows, err := models.ArticleObjects.Using(backend).Filter(models.ArticleFields.Title.Exact(title)).All(ctx)
	if err != nil {
		t.Fatalf("query title %q error = %v", title, err)
	}
	if len(rows) != want {
		t.Fatalf("title %q count = %d, want %d", title, len(rows), want)
	}
}
