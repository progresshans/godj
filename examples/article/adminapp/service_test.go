package adminapp_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/query"
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

func TestDurableServiceAuditSharesArticleTransactionAndPublishOrder(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-durable-audit")
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{
		`CREATE TABLE "godj_conformance_article" (
			"id" INTEGER PRIMARY KEY AUTOINCREMENT,
			"title" TEXT NOT NULL,
			"published" INTEGER NOT NULL,
			"summary" TEXT NULL
		)`,
		`CREATE TABLE "test_audit" (
			"id" INTEGER PRIMARY KEY AUTOINCREMENT,
			"object_id" INTEGER NOT NULL,
			"action" TEXT NOT NULL
		)`,
		`CREATE TRIGGER "require_published_article_before_audit"
			BEFORE INSERT ON "test_audit"
			WHEN NEW."action" = 'publish' AND COALESCE((
				SELECT "published" FROM "godj_conformance_article" WHERE "id" = NEW."object_id"
			), 0) <> 1
			BEGIN
				SELECT RAISE(FAIL, 'audit executed before Article DML');
			END`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatalf("schema statement error = %v", err)
		}
	}
	repository, err := adminapp.NewRepository(backend)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	first, err := repository.Create(ctx, adminapp.Input{Title: "First"})
	if err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	second, err := repository.Create(ctx, adminapp.Input{Title: "Second"})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	writerFailure := errors.New("forced durable audit failure")
	store := &transactionalAuditProbe{failAt: 2, failure: writerFailure}
	service, err := adminapp.NewDurableService(backend, store)
	if err != nil {
		t.Fatalf("NewDurableService() error = %v", err)
	}
	if _, err := service.Publish(ctx, "operator", []int64{second.ID, first.ID, second.ID}); !errors.Is(err, writerFailure) {
		t.Fatalf("Publish(audit failure) error = %v", err)
	}
	if fmt.Sprint(store.objectIDs) != fmt.Sprintf("[%d %d]", first.ID, second.ID) {
		t.Fatalf("failed publish audit order = %v, want ascending canonical IDs", store.objectIDs)
	}
	for _, id := range []int64{first.ID, second.ID} {
		article, found, err := repository.Get(ctx, id)
		if err != nil || !found || article.Published {
			t.Fatalf("Article %d after audit rollback = %#v, found=%t, error=%v", id, article, found, err)
		}
	}
	if rows := readAuditRows(t, ctx, backend); len(rows) != 0 {
		t.Fatalf("audit rows after rollback = %#v", rows)
	}
	if store.borrowed == nil {
		t.Fatal("durable writer did not receive a transaction session")
	}
	if _, err := store.borrowed.Insert(ctx, testAuditInsertPlan(999, admin.ActionAdd)); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("borrowed session after callback error = %v, want inactive session", err)
	}

	store.reset()
	result, err := service.Publish(ctx, "operator", []int64{second.ID, first.ID, second.ID})
	if err != nil || fmt.Sprint(result.MatchedIDs) != fmt.Sprintf("[%d %d]", first.ID, second.ID) {
		t.Fatalf("Publish(success) = %#v, %v", result, err)
	}
	rows := readAuditRows(t, ctx, backend)
	if len(rows) != 2 || rows[0].objectID != first.ID || rows[1].objectID != second.ID ||
		rows[0].action != admin.ActionPublish || rows[1].action != admin.ActionPublish {
		t.Fatalf("committed audit rows = %#v", rows)
	}
	store.reset()
	unchanged, changed, err := service.Update(ctx, "operator", first.ID, adminapp.Input{Title: "First", Published: true})
	if err != nil || len(changed) != 0 || !unchanged.Published {
		t.Fatalf("Update(no-op) = %#v, changed=%v, error=%v", unchanged, changed, err)
	}
	if store.calls != 0 || len(readAuditRows(t, ctx, backend)) != 2 {
		t.Fatalf("no-op update audit calls=%d rows=%#v", store.calls, readAuditRows(t, ctx, backend))
	}

	store.history = []admin.AuditEntry{{
		Sequence: 9,
		ActorID:  "operator",
		Model:    "godj_conformance.article",
		ObjectID: first.ID,
		Action:   admin.ActionPublish,
	}}
	history, err := service.HistoryLimited(ctx, first.ID, 7)
	if err != nil || len(history) != 1 || history[0].Sequence != 9 ||
		store.historyModel != "godj_conformance.article" || store.historyObjectID != first.ID || store.historyLimit != 7 {
		t.Fatalf("HistoryLimited() = %#v, %v; request=%q/%d/%d", history, err,
			store.historyModel, store.historyObjectID, store.historyLimit)
	}
	if got := service.History(first.ID); got != nil {
		t.Fatalf("History(durable) = %#v, want context-required nil convenience result", got)
	}
}

func TestMemoryServiceCommitUnknownDoesNotRetryOrPublishSyntheticAudit(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-commit-unknown")
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
	unknown := &commitUnknownBackend{Backend: backend}
	audit, err := admin.NewAuditLog(4)
	if err != nil {
		t.Fatalf("NewAuditLog() error = %v", err)
	}
	service, err := adminapp.NewService(unknown, audit)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Create(ctx, "operator", adminapp.Input{Title: "Outcome unknown"}); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}) {
		t.Fatalf("Create(commit unknown) error = %v", err)
	}
	if unknown.atomicCalls != 1 {
		t.Fatalf("Atomic() calls = %d, want one with no automatic retry", unknown.atomicCalls)
	}
	if audit.Len() != 0 {
		t.Fatalf("memory audit length = %d, want no synthetic post-unknown append", audit.Len())
	}
	repository, err := adminapp.NewRepository(backend)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	page, err := repository.List(ctx, adminapp.ListOptions{})
	if err != nil || page.Total != 1 || len(page.Articles) != 1 || page.Articles[0].Title != "Outcome unknown" {
		t.Fatalf("underlying committed outcome probe = %#v, %v", page, err)
	}
}

func TestNewDurableServiceRejectsTypedNilStore(t *testing.T) {
	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "article-admin-nil-durable-audit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	var store *transactionalAuditProbe
	if _, err := adminapp.NewDurableService(backend, store); !adminapp.IsCode(err, adminapp.CodeInvalidInput) {
		t.Fatalf("NewDurableService(typed nil) error = %v, want invalid_input", err)
	}
}

type transactionalAuditProbe struct {
	failAt          int
	failure         error
	calls           int
	objectIDs       []int64
	borrowed        db.Session
	history         []admin.AuditEntry
	historyModel    string
	historyObjectID int64
	historyLimit    int
}

func (store *transactionalAuditProbe) AppendAudit(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
	store.calls++
	store.borrowed = session
	store.objectIDs = append(store.objectIDs, event.ObjectID())
	if _, err := session.Insert(ctx, testAuditInsertPlan(event.ObjectID(), event.Action())); err != nil {
		return err
	}
	if store.failAt > 0 && store.calls == store.failAt {
		return store.failure
	}
	return nil
}

func (store *transactionalAuditProbe) AuditHistory(_ context.Context, model string, objectID int64, limit int) ([]admin.AuditEntry, error) {
	store.historyModel = model
	store.historyObjectID = objectID
	store.historyLimit = limit
	result := make([]admin.AuditEntry, len(store.history))
	for index := range store.history {
		result[index] = store.history[index].Clone()
	}
	return result, nil
}

func (store *transactionalAuditProbe) reset() {
	store.failAt = 0
	store.calls = 0
	store.objectIDs = nil
	store.borrowed = nil
}

type commitUnknownBackend struct {
	*sqlite.Backend
	atomicCalls int
}

func (backend *commitUnknownBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls++
	if err := backend.Backend.Atomic(ctx, callback); err != nil {
		return err
	}
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "test backend reports unknown commit outcome",
	}
}

var (
	testAuditIDField       = query.NewFieldRef("id", "id", query.FieldInteger, false)
	testAuditObjectIDField = query.NewFieldRef("object_id", "object_id", query.FieldInteger, false)
	testAuditActionField   = query.NewFieldRef("action", "action", query.FieldString, false)
)

func testAuditInsertPlan(objectID int64, action admin.Action) query.InsertPlan {
	return query.NewInsertPlan("test_audit", []query.Assignment{
		query.NewAssignment(testAuditObjectIDField, query.Integer(objectID)),
		query.NewAssignment(testAuditActionField, query.String(string(action))),
	})
}

type testAuditRow struct {
	id       int64
	objectID int64
	action   admin.Action
}

func readAuditRows(t *testing.T, ctx context.Context, backend db.Queryer) []testAuditRow {
	t.Helper()
	plan := query.NewPlan("test_audit", []query.FieldRef{
		testAuditIDField,
		testAuditObjectIDField,
		testAuditActionField,
	}).WithOrderings(query.NewOrdering(testAuditIDField, query.Ascending))
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatalf("query audit rows: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close audit rows: %v", err)
		}
	}()
	var result []testAuditRow
	for rows.Next() {
		var row testAuditRow
		var action string
		if err := rows.Scan(&row.id, &row.objectID, &action); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		row.action = admin.Action(action)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit rows: %v", err)
	}
	return result
}
