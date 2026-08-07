package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
)

func TestCompileWritePlansBindZeroEmptyAndNullValues(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	assignments := []query.Assignment{
		query.NewAssignment(title, query.String("")),
		query.NewAssignment(published, query.Boolean(false)),
		query.NewAssignment(summary, query.Null()),
	}

	insertSQL, insertArguments, err := sqlite.CompileInsert(query.NewInsertPlan("news_article", assignments))
	if err != nil {
		t.Fatalf("CompileInsert() error = %v", err)
	}
	if insertSQL != `INSERT INTO "news_article" ("title", "published", "summary") VALUES (?, ?, ?)` {
		t.Fatalf("insert SQL = %q", insertSQL)
	}
	if want := []any{"", false, nil}; !reflect.DeepEqual(insertArguments, want) {
		t.Fatalf("insert arguments = %#v, want %#v", insertArguments, want)
	}

	updateSQL, updateArguments, err := sqlite.CompileUpdate(query.NewUpdatePlan("news_article", assignments[1:], id, query.Integer(9)))
	if err != nil {
		t.Fatalf("CompileUpdate() error = %v", err)
	}
	if updateSQL != `UPDATE "news_article" SET "published" = ?, "summary" = ? WHERE "id" = ?` {
		t.Fatalf("update SQL = %q", updateSQL)
	}
	if want := []any{false, nil, int64(9)}; !reflect.DeepEqual(updateArguments, want) {
		t.Fatalf("update arguments = %#v, want %#v", updateArguments, want)
	}

	deleteSQL, deleteArguments, err := sqlite.CompileDelete(query.NewDeletePlan("news_article", id, query.Integer(9)))
	if err != nil {
		t.Fatalf("CompileDelete() error = %v", err)
	}
	if deleteSQL != `DELETE FROM "news_article" WHERE "id" = ?` || !reflect.DeepEqual(deleteArguments, []any{int64(9)}) {
		t.Fatalf("delete = %q %#v", deleteSQL, deleteArguments)
	}
}

func TestCompileWriteRejectsInvalidFieldValues(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	_, _, err := sqlite.CompileInsert(query.NewInsertPlan("news_article", []query.Assignment{
		query.NewAssignment(title, query.Null()),
	}))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("NULL non-null insert error = %v, want invalid_plan", err)
	}
	_, _, err = sqlite.CompileInsert(query.NewInsertPlan("news_article", []query.Assignment{
		query.NewAssignment(title, query.Boolean(false)),
	}))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("wrong scalar insert error = %v, want invalid_plan", err)
	}
}

func TestCompileWriteRejectsSQLiteCaseFoldedColumnCollisions(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	titleUpper := query.NewFieldRef("title_upper", "TITLE", query.FieldString, false)
	_, _, err := sqlite.CompileInsert(query.NewInsertPlan("news_article", []query.Assignment{
		query.NewAssignment(title, query.String("first")),
		query.NewAssignment(titleUpper, query.String("second")),
	}))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("case-folded duplicate insert error = %v, want invalid_plan", err)
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	aliasID := query.NewFieldRef("alias_id", "ID", query.FieldInteger, false)
	_, _, err = sqlite.CompileUpdate(query.NewUpdatePlan(
		"news_article",
		[]query.Assignment{query.NewAssignment(aliasID, query.Integer(10))},
		id,
		query.Integer(9),
	))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("case-folded key update error = %v, want invalid_plan", err)
	}
}

func TestSQLiteInsertDefaultValuesSupportsAutoOnlyModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "auto-only-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "only_id" ("id" INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatalf("create auto-only table: %v", err)
	}
	statement, arguments, err := sqlite.CompileInsert(query.NewInsertPlan("only_id", nil))
	if err != nil {
		t.Fatalf("CompileInsert(default values) error = %v", err)
	}
	if statement != `INSERT INTO "only_id" DEFAULT VALUES` || len(arguments) != 0 {
		t.Fatalf("default insert = %q %#v", statement, arguments)
	}
	identifier, err := backend.Insert(ctx, query.NewInsertPlan("only_id", nil))
	if err != nil {
		t.Fatalf("Insert(default values) error = %v", err)
	}
	if identifier != 1 {
		t.Fatalf("default insert ID = %d, want 1", identifier)
	}
}

func TestArticleManagerWriteVerticalSlice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabase(t, ctx)
	created, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Created"))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != 5 || created.Published || created.Summary != nil {
		t.Fatalf("created Article = %#v", created)
	}

	updated, err := models.ArticleObjects.Update(
		ctx,
		backend,
		created,
		models.ArticlePatch{}.WithTitle("").WithPublished(false).WithSummary(""),
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Title != "" || updated.Published || updated.Summary == nil || *updated.Summary != "" {
		t.Fatalf("updated Article = %#v", updated)
	}

	nulled, err := models.ArticleObjects.Update(ctx, backend, updated, models.ArticlePatch{}.WithSummaryNull())
	if err != nil {
		t.Fatalf("explicit NULL Update() error = %v", err)
	}
	if nulled.Summary != nil {
		t.Fatalf("explicit NULL summary = %#v", nulled.Summary)
	}
	stored, err := models.ArticleObjects.Using(backend).Filter(models.ArticleFields.ID.Exact(nulled.ID)).All(ctx)
	if err != nil || len(stored) != 1 || stored[0].Summary != nil || stored[0].Title != "" || stored[0].Published {
		t.Fatalf("stored after update = %#v, error = %v", stored, err)
	}

	rowsAffected, err := models.ArticleObjects.Delete(ctx, backend, &nulled)
	if err != nil || rowsAffected != 1 {
		t.Fatalf("Delete() = (%d, %v)", rowsAffected, err)
	}
	if nulled.ID != 0 {
		t.Fatalf("deleted Article ID = %d, want cleared", nulled.ID)
	}
	if _, present := (models.ArticleDescriptor{}).PrimaryKey(nulled); present {
		t.Fatal("deleted Article retained explicit primary-key presence")
	}
	remaining, err := models.ArticleObjects.Using(backend).Filter(models.ArticleFields.ID.Exact(created.ID)).All(ctx)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("rows after delete = %#v, error = %v", remaining, err)
	}
}

func TestSQLiteWritePropagatesCanceledContextWithoutExecuting(t *testing.T) {
	t.Parallel()

	background := context.Background()
	backend := openArticleDatabase(t, background)
	canceled, cancel := context.WithCancel(background)
	cancel()
	_, err := models.ArticleObjects.Create(canceled, backend, models.NewArticleCreate("Canceled"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context.Canceled", err)
	}
	rows, queryErr := models.ArticleObjects.Using(backend).Filter(models.ArticleFields.Title.Exact("Canceled")).All(background)
	if queryErr != nil || len(rows) != 0 {
		t.Fatalf("canceled create rows = %#v, error = %v", rows, queryErr)
	}
}

func TestSQLiteInsertRejectsTriggerIgnoredRow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabase(t, ctx)
	if _, err := backend.ExecContext(ctx, `CREATE TRIGGER "ignore_article_insert" BEFORE INSERT ON "godj_conformance_article"
		WHEN NEW."title" = 'Ignored' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatalf("create ignore trigger: %v", err)
	}
	_, err := models.ArticleObjects.Create(ctx, backend, models.NewArticleCreate("Ignored"))
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows}) {
		t.Fatalf("Create(ignored) error = %v, want unexpected rows", err)
	}
	rows, queryErr := models.ArticleObjects.Using(backend).
		Filter(models.ArticleFields.Title.Exact("Ignored")).
		All(ctx)
	if queryErr != nil || len(rows) != 0 {
		t.Fatalf("ignored insert rows = %#v, error = %v", rows, queryErr)
	}
}

func TestSQLiteInsertClassifiesPrimaryKeyConstraintAndPreservesCause(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabase(t, ctx)
	metadata := (models.ArticleDescriptor{}).Metadata()
	plan := query.NewInsertPlan(metadata.DBTable, []query.Assignment{
		query.NewAssignment(
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.Integer(1),
		),
		query.NewAssignment(
			query.NewFieldRef("title", "title", query.FieldString, false),
			query.String("Duplicate primary key"),
		),
		query.NewAssignment(
			query.NewFieldRef("published", "published", query.FieldBoolean, false),
			query.Boolean(false),
		),
		query.NewAssignment(
			query.NewFieldRef("summary", "summary", query.FieldString, true),
			query.Null(),
		),
	})

	_, err := backend.Insert(ctx, plan)
	if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey}) {
		t.Fatalf("Insert(duplicate primary key) error = %v, want integrity_error/unique_primary_key", err)
	}
	var driverError *modernsqlite.Error
	if !errors.As(err, &driverError) {
		t.Fatalf("Insert(duplicate primary key) error = %v, want preserved *sqlite.Error cause", err)
	}
}

func TestSQLiteInsertDoesNotMisclassifyAnotherUniqueConstraintAsPrimaryKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend := openArticleDatabase(t, ctx)
	if _, err := backend.ExecContext(ctx, `CREATE UNIQUE INDEX "unique_article_title" ON "godj_conformance_article" ("title")`); err != nil {
		t.Fatalf("create title unique index: %v", err)
	}
	plan := query.NewInsertPlan("godj_conformance_article", []query.Assignment{
		query.NewAssignment(query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(99)),
		query.NewAssignment(query.NewFieldRef("title", "title", query.FieldString, false), query.String("Alpine Guide")),
		query.NewAssignment(query.NewFieldRef("published", "published", query.FieldBoolean, false), query.Boolean(false)),
		query.NewAssignment(query.NewFieldRef("summary", "summary", query.FieldString, true), query.Null()),
	})

	_, err := backend.Insert(ctx, plan)
	if err == nil {
		t.Fatal("Insert(duplicate title) unexpectedly succeeded")
	}
	if errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniquePrimaryKey}) {
		t.Fatalf("Insert(duplicate title) error = %v, must not be classified as a primary-key conflict", err)
	}
	var driverError *modernsqlite.Error
	if !errors.As(err, &driverError) {
		t.Fatalf("Insert(duplicate title) error = %v, want preserved *sqlite.Error cause", err)
	}
}
