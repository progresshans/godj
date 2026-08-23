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

func TestServicePublishesAuditOnlyForConfirmedSemanticWrites(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-service")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "godj_conformance_article" (
		"id" INTEGER PRIMARY KEY AUTOINCREMENT,
		"title" TEXT NOT NULL,
		"published" INTEGER NOT NULL,
		"summary" TEXT NULL
	)`); err != nil {
		t.Fatalf("create Article table: %v", err)
	}
	audit, err := admin.NewAuditLog(32)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	service, err := adminapp.NewService(backend, audit)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	created, err := service.Create(ctx, "operator", adminapp.Input{Title: "Initial"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated, changed, err := service.Update(ctx, "operator", created.ID, adminapp.Input{Title: "Changed"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if fmt.Sprint(changed) != "[title]" {
		t.Fatalf("Update() changed = %v", changed)
	}
	if _, unchanged, err := service.Update(ctx, "operator", created.ID, adminapp.Input{Title: updated.Title}); err != nil || len(unchanged) != 0 {
		t.Fatalf("no-op Update() changed = %v, error = %v", unchanged, err)
	}
	result, err := service.Publish(ctx, "operator", []int64{created.ID})
	if err != nil || result.Matched() != 1 {
		t.Fatalf("Publish() = %#v, %v", result, err)
	}
	if _, err := service.Delete(ctx, "operator", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	history := service.History(created.ID)
	if len(history) != 4 {
		t.Fatalf("History() = %#v, want four events", history)
	}
	wantActions := []admin.Action{admin.ActionAdd, admin.ActionChange, admin.ActionPublish, admin.ActionDelete}
	for index, want := range wantActions {
		if history[index].Action != want || history[index].Sequence != uint64(index+1) || history[index].ActorID != "operator" {
			t.Fatalf("History()[%d] = %#v, want action %s", index, history[index], want)
		}
	}
	if len(history[0].ChangedFields) != 0 {
		t.Fatalf("add changed fields = %v, want none", history[0].ChangedFields)
	}
}

func TestNewServiceRejectsZeroAuditLogAtStartup(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-zero-audit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	if _, err := adminapp.NewService(backend, &admin.AuditLog{}); !adminapp.IsCode(err, adminapp.CodeInvalidInput) {
		t.Fatalf("NewService(zero audit) error = %v, want invalid_input", err)
	}
}

func TestDeleteAuditPreparationFailureRollsBackRow(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-delete-audit-rollback")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{
		`CREATE TABLE "godj_conformance_article" ("id" INTEGER PRIMARY KEY, "title" TEXT NOT NULL, "published" INTEGER NOT NULL, "summary" TEXT NULL)`,
		`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (1, 'bad' || char(1) || 'label', 0, NULL)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	audit, err := admin.NewAuditLog(4)
	if err != nil {
		t.Fatal(err)
	}
	service, err := adminapp.NewService(backend, audit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Delete(ctx, "operator", 1); err == nil {
		t.Fatal("Delete(invalid audit label) error = nil")
	}
	article, found, err := service.Get(ctx, 1)
	if err != nil || !found || article.ID != 1 {
		t.Fatalf("row after rejected delete = %#v, found=%t, error=%v", article, found, err)
	}
	if audit.Len() != 0 {
		t.Fatalf("audit length = %d, want 0", audit.Len())
	}
	if _, err := service.Delete(ctx, "operator", 999); !errors.Is(err, admin.ErrObjectNotFound) {
		t.Fatalf("Delete(missing) error = %v, want ErrObjectNotFound", err)
	}
}

func TestServiceRejectsInvalidActorBeforeArticleMutation(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-service-invalid-actor")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	audit, err := admin.NewAuditLog(4)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	service, err := adminapp.NewService(backend, audit)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Create(ctx, "bad\nactor", adminapp.Input{Title: "Never written"}); err == nil {
		t.Fatal("Create(invalid actor) error = nil")
	}
	if audit.Len() != 0 {
		t.Fatalf("audit length = %d, want 0", audit.Len())
	}
}
