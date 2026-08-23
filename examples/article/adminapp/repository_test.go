package adminapp_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
)

func TestRepositorySQLiteCRUDSearchAndAtomicPublish(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-repository")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"title" TEXT NOT NULL,
		"published" INTEGER NOT NULL,
		"summary" TEXT NULL
	)`); err != nil {
		t.Fatalf("create Article table: %v", err)
	}

	repository, err := adminapp.NewRepository(backend)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	goSummary := "Go systems"
	first, err := repository.Create(ctx, adminapp.Input{Title: "Alpha", Summary: &goSummary})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := repository.Create(ctx, adminapp.Input{Title: "Go Beta", Published: false})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	third, err := repository.Create(ctx, adminapp.Input{Title: "Gamma", Published: false})
	if err != nil {
		t.Fatalf("Create(third) error = %v", err)
	}

	page, err := repository.List(ctx, adminapp.ListOptions{Search: "go", Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.Total != 2 || len(page.Articles) != 1 || page.Articles[0].ID != first.ID {
		t.Fatalf("List() = %#v, want total 2 and first ID %d", page, first.ID)
	}

	emptySummary := ""
	updated, changed, err := repository.Update(ctx, first.ID, adminapp.Input{
		Title:     "Alpha Updated",
		Published: true,
		Summary:   &emptySummary,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if fmt.Sprint(changed) != "[title published summary]" || updated.Title != "Alpha Updated" ||
		!updated.Published || updated.Summary == nil || *updated.Summary != "" {
		t.Fatalf("Update() = %#v changed %v", updated, changed)
	}

	result, err := repository.Publish(ctx, []int64{third.ID, second.ID, third.ID, 999999})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if fmt.Sprint(result.MatchedIDs) != fmt.Sprintf("[%d %d]", second.ID, third.ID) {
		t.Fatalf("Publish().MatchedIDs = %v", result.MatchedIDs)
	}
	for _, id := range []int64{second.ID, third.ID} {
		article, found, err := repository.Get(ctx, id)
		if err != nil || !found || !article.Published {
			t.Fatalf("Get(%d) = %#v, %v, %v", id, article, found, err)
		}
	}

	deleted, err := repository.Delete(ctx, second.ID)
	if err != nil || deleted.ID != second.ID {
		t.Fatalf("Delete() = %#v, %v", deleted, err)
	}
	if _, found, err := repository.Get(ctx, second.ID); err != nil || found {
		t.Fatalf("Get(deleted) found = %v, error = %v", found, err)
	}
	if _, err := repository.Delete(ctx, second.ID); !adminapp.IsCode(err, adminapp.CodeNotFound) {
		t.Fatalf("Delete(missing) error = %v, want not_found", err)
	} else if !errors.Is(err, admin.ErrObjectNotFound) {
		t.Fatalf("Delete(missing) error = %v, want admin.ErrObjectNotFound", err)
	}
}

func TestRepositoryRejectsInvalidInputBeforeBackendWork(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-invalid")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	repository, err := adminapp.NewRepository(backend)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}

	for name, run := range map[string]func() error{
		"empty title": func() error {
			_, err := repository.Create(ctx, adminapp.Input{Title: "  "})
			return err
		},
		"negative offset": func() error {
			_, err := repository.List(ctx, adminapp.ListOptions{Offset: -1})
			return err
		},
		"empty action": func() error {
			_, err := repository.Publish(ctx, nil)
			return err
		},
		"canceled context": func() error {
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err := repository.List(canceled, adminapp.ListOptions{})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil {
				t.Fatal("operation error = nil")
			}
			if name != "canceled context" && !adminapp.IsCode(err, adminapp.CodeInvalidInput) {
				t.Fatalf("operation error = %v, want invalid_input", err)
			}
			if name == "canceled context" && !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
}
