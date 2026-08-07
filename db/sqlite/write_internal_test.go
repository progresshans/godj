package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

func TestClassifyInsertErrorUsesStructuredSQLiteExtendedCode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "structured-insert-error-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "widget" ("widget_pk" INTEGER NOT NULL PRIMARY KEY)`); err != nil {
		t.Fatalf("create widget table: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "widget" ("widget_pk") VALUES (1)`); err != nil {
		t.Fatalf("seed widget row: %v", err)
	}
	plan := query.NewInsertPlan("widget", []query.Assignment{
		query.NewAssignment(
			query.NewFieldRef("widget_pk", "widget_pk", query.FieldInteger, false),
			query.Integer(1),
		),
	})
	_, classified := backend.Insert(ctx, plan)
	if classified == nil {
		t.Fatal("duplicate primary-key insert unexpectedly succeeded")
	}
	var sqliteErr *modernsqlite.Error
	if !errors.Is(classified, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey}) {
		t.Fatalf("widget.widget_pk error classified as %v, want integrity_error/unique_primary_key", classified)
	}
	if !errors.As(classified, &sqliteErr) {
		t.Fatalf("classified error = %v, want preserved structured SQLite cause", classified)
	}
}

func TestClassifyInsertErrorRejectsPrimaryKeyLookingTextWithoutStructuredCode(t *testing.T) {
	t.Parallel()

	lookalike := fmt.Errorf("execute: UNIQUE constraint failed: widget.widget_pk (1555)")
	classified := classifyInsertError(lookalike)
	if errors.Is(classified, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey}) {
		t.Fatalf("plain-text lookalike classified as primary-key conflict: %v", classified)
	}
	if !errors.Is(classified, lookalike) {
		t.Fatalf("classified error = %v, want preserved lookalike cause", classified)
	}
}
