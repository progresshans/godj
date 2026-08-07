package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

func TestBackendQueryClassifiesOnlyThePlanMissingTable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "missing-query-table-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	rows, err := backend.Query(ctx, query.NewPlan("missing_article", []query.FieldRef{id}))
	if rows != nil {
		t.Fatalf("Query() rows = %T, want nil", rows)
	}
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeMissingTable}) {
		t.Fatalf("Query() error = %v, want backend_error/missing_table", err)
	}
	var driverError *modernsqlite.Error
	if !errors.As(err, &driverError) {
		t.Fatalf("Query() error = %v, want preserved *sqlite.Error cause", err)
	}

	if _, err := backend.ExecContext(ctx, `CREATE TABLE "present_article" ("id" INTEGER)`); err != nil {
		t.Fatalf("create present table: %v", err)
	}
	missingColumn := query.NewFieldRef("missing", "missing", query.FieldInteger, false)
	_, err = backend.Query(ctx, query.NewPlan("present_article", []query.FieldRef{missingColumn}))
	if errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeMissingTable}) {
		t.Fatalf("missing-column error was classified as missing table: %v", err)
	}
}

func TestMissingTableClassifierRejectsLookalikes(t *testing.T) {
	t.Parallel()

	if isMissingTableMessage("SQL logic error: no such table: article_extra (1)", "article") {
		t.Fatal("table-name prefix was accepted")
	}
	if isMissingTableMessage("SQL logic error: no such column: article (1)", "article") {
		t.Fatal("missing-column message was accepted")
	}
	lookalike := errors.New("SQL logic error: no such table: article (1)")
	if errors.Is(classifyQueryError(lookalike, "article"), &query.Error{Code: query.CodeMissingTable}) {
		t.Fatal("unstructured lookalike error was classified as missing table")
	}
}
