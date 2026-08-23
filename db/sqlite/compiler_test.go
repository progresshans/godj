package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCompilePredicatesOrderingAndLimit(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	plan := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(
			query.NewCondition(title, query.LookupIContains, query.String(`50%_Go\SQL`)),
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
		).
		WithOrderings(query.NewOrdering(title, query.Descending), query.NewOrdering(id, query.Ascending))
	plan, err := plan.WithLimit(2)
	if err != nil {
		t.Fatalf("WithLimit() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "id", "title", "published" FROM "news_article" WHERE "title" LIKE ? ESCAPE '\' AND "published" = ? ORDER BY "title" DESC, "id" ASC LIMIT ?`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{`%50\%\_Go\\SQL%`, true, int64(2)}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileScalarProjectionUsesExpressionOrderAndSourceMetadata(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	result, err := query.NewProjectionResult(title, id)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(published, query.Descending)).
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "title", "id" FROM "news_article" WHERE "published" = ? ORDER BY "published" DESC`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{true}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileDistinctProjectionLimitOffsetPreservesArgumentOrder(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	result, err := query.NewProjectionResult(title, id)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(
			query.NewCondition(title, query.LookupIContains, query.String("Go")),
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
		).
		WithOrderings(query.NewOrdering(title, query.Ascending), query.NewOrdering(id, query.Descending)).
		WithDistinct().
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}
	plan, err = plan.WithLimit(5)
	if err != nil {
		t.Fatalf("WithLimit() error = %v", err)
	}
	plan, err = plan.WithOffset(7)
	if err != nil {
		t.Fatalf("WithOffset() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT DISTINCT "title", "id" FROM "news_article" WHERE "title" LIKE ? ESCAPE '\' AND "published" = ? ORDER BY "title" ASC, "id" DESC LIMIT ? OFFSET ?`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{"%Go%", true, int64(5), int64(7)}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileOffsetOnlyUsesSQLiteUnlimitedLimit(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewPlan("news_article", []query.FieldRef{id}).WithOffset(9)
	if err != nil {
		t.Fatalf("WithOffset() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if want := `SELECT "id" FROM "news_article" LIMIT -1 OFFSET ?`; statement != want {
		t.Fatalf("SQL = %q, want %q", statement, want)
	}
	if want := []any{int64(9)}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileDistinctProjectionRejectsOrderingOutsideResult(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	result, err := query.NewProjectionResult(title)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title}).
		WithOrderings(query.NewOrdering(id, query.Ascending)).
		WithDistinct().
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}

	_, _, err = sqlite.Compile(plan)
	if !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Field:    "id",
	}) {
		t.Fatalf("Compile() error = %v, want backend unsupported_feature for id", err)
	}
}

func TestCompileAggregateWrapsTheSlicedLogicalSource(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	result, err := query.NewAggregateResult(
		query.CountAllResult(),
		query.MaxResult(id),
		query.MaxResult(summary),
	)
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, summary, published}).
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithDistinct().
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}
	plan, err = plan.WithLimit(4)
	if err != nil {
		t.Fatalf("WithLimit() error = %v", err)
	}
	plan, err = plan.WithOffset(2)
	if err != nil {
		t.Fatalf("WithOffset() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT COUNT(*), MAX("godj_aggregate_source"."id"), MAX("godj_aggregate_source"."summary") FROM (SELECT DISTINCT "id", "title", "summary", "published" FROM "news_article" WHERE "published" = ? ORDER BY "id" DESC LIMIT ? OFFSET ?) AS "godj_aggregate_source"`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{true, int64(4), int64(2)}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileSimpleAggregateUsesDirectSourceAndDropsOrdering(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	result, err := query.NewAggregateResult(
		query.CountAllResult(),
		query.MaxResult(id),
		query.MaxResult(summary),
	)
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, summary, published}).
		WithConditions(
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
			query.NewCondition(summary, query.LookupIContains, query.String("Go")),
		).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT COUNT(*), MAX("id"), MAX("summary") FROM "news_article" WHERE "published" = ? AND "summary" LIKE ? ESCAPE '\'`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{true, "%Go%"}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileDirectAggregateValidatesOmittedOrderings(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	outsideSource := query.NewFieldRef("created", "created_at", query.FieldString, false)
	result, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	tests := []struct {
		name     string
		ordering query.Ordering
		detail   string
	}{
		{
			name:     "field outside source metadata",
			ordering: query.NewOrdering(outsideSource, query.Ascending),
			detail:   `ordering field "created" is not selected model metadata`,
		},
		{
			name:     "unknown direction",
			ordering: query.NewOrdering(id, query.Direction("sideways")),
			detail:   "unknown ordering direction",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plan, planErr := query.NewPlan("news_article", []query.FieldRef{id}).
				WithOrderings(test.ordering).
				WithResultShape(result)
			if planErr != nil {
				t.Fatalf("WithResultShape() error = %v", planErr)
			}
			_, _, compileErr := sqlite.Compile(plan)
			if !errors.Is(compileErr, &query.Error{
				Category: query.CategoryQuery,
				Code:     query.CodeInvalidPlan,
			}) {
				t.Fatalf("Compile() error = %v, want query invalid_plan", compileErr)
			}
			if !strings.Contains(compileErr.Error(), test.detail) {
				t.Fatalf("Compile() error = %v, want detail %q", compileErr, test.detail)
			}
		})
	}
}

func TestCompileRejectsInvalidReadSourceMetadataForScalarAndRelation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fields []query.FieldRef
		detail string
	}{
		{
			name:   "empty logical name",
			fields: []query.FieldRef{query.NewFieldRef("", "id", query.FieldInteger, false)},
			detail: `field name "" is empty or contains NUL`,
		},
		{
			name:   "NUL logical name",
			fields: []query.FieldRef{query.NewFieldRef("i\x00d", "id", query.FieldInteger, false)},
			detail: `field name "i\x00d" is empty or contains NUL`,
		},
		{
			name:   "empty column",
			fields: []query.FieldRef{query.NewFieldRef("id", "", query.FieldInteger, false)},
			detail: `field column "" is empty or contains NUL`,
		},
		{
			name:   "NUL column",
			fields: []query.FieldRef{query.NewFieldRef("id", "i\x00d", query.FieldInteger, false)},
			detail: `field column "i\x00d" is empty or contains NUL`,
		},
		{
			name: "duplicate logical name",
			fields: []query.FieldRef{
				query.NewFieldRef("id", "id", query.FieldInteger, false),
				query.NewFieldRef("id", "other_id", query.FieldInteger, false),
			},
			detail: `field name "id" is duplicated`,
		},
		{
			name: "duplicate column",
			fields: []query.FieldRef{
				query.NewFieldRef("id", "id", query.FieldInteger, false),
				query.NewFieldRef("alias", "id", query.FieldInteger, false),
			},
			detail: `field column "id" is duplicated under SQLite identifier rules`,
		},
		{
			name: "ASCII case-folded duplicate column",
			fields: []query.FieldRef{
				query.NewFieldRef("title", "Title", query.FieldString, false),
				query.NewFieldRef("alias", "title", query.FieldString, false),
			},
			detail: `field column "title" is duplicated under SQLite identifier rules`,
		},
		{
			name:   "unsupported kind",
			fields: []query.FieldRef{query.NewFieldRef("score", "score", query.FieldKind("decimal"), false)},
			detail: `field "score" has unsupported kind "decimal"`,
		},
	}

	relatedPath := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	for _, test := range tests {
		test := test
		for _, compilerPath := range []struct {
			name string
			plan query.Plan
		}{
			{name: "scalar", plan: query.NewPlan("news_article", test.fields)},
			{
				name: "relation",
				plan: query.NewPlan("blog_post", test.fields).WithConditions(
					query.NewRelatedCondition(relatedPath, query.LookupExact, query.String("Ada")),
				),
			},
		} {
			compilerPath := compilerPath
			t.Run(test.name+"/"+compilerPath.name, func(t *testing.T) {
				t.Parallel()

				statement, arguments, err := sqlite.Compile(compilerPath.plan)
				if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
					t.Fatalf("Compile() error = %v, want query invalid_plan", err)
				}
				if !strings.Contains(err.Error(), test.detail) {
					t.Fatalf("Compile() error = %v, want detail %q", err, test.detail)
				}
				if statement != "" || arguments != nil {
					t.Fatalf("Compile() = (%q, %#v, %v), want empty SQL and nil arguments", statement, arguments, err)
				}
			})
		}
	}
}

func TestCompileReadSourceMetadataRetainsSQLiteIdentifierQuoting(t *testing.T) {
	t.Parallel()

	field := query.NewFieldRef(`Display "Name"`, `select "value"`, query.FieldString, false)
	scalarSQL, scalarArguments, err := sqlite.Compile(query.NewPlan(`odd "table"`, []query.FieldRef{field}))
	if err != nil {
		t.Fatalf("Compile(scalar) error = %v", err)
	}
	if want := `SELECT "select ""value""" FROM "odd ""table"""`; scalarSQL != want {
		t.Fatalf("scalar SQL = %q, want %q", scalarSQL, want)
	}
	if len(scalarArguments) != 0 {
		t.Fatalf("scalar arguments = %#v, want empty", scalarArguments)
	}

	relatedPath := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	relationPlan := query.NewPlan("blog_post", []query.FieldRef{field}).WithConditions(
		query.NewRelatedCondition(relatedPath, query.LookupExact, query.String("Ada")),
	)
	relationSQL, relationArguments, err := sqlite.Compile(relationPlan)
	if err != nil {
		t.Fatalf("Compile(relation) error = %v", err)
	}
	wantRelation := `SELECT "t0"."select ""value""" FROM "blog_post" AS "t0" ` +
		`INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = ?`
	if relationSQL != wantRelation {
		t.Fatalf("relation SQL = %q, want %q", relationSQL, wantRelation)
	}
	if want := []any{"Ada"}; !reflect.DeepEqual(relationArguments, want) {
		t.Fatalf("relation arguments = %#v, want %#v", relationArguments, want)
	}
}

func TestBackendRejectsInvalidReadSourceMetadataBeforeIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "invalid-read-source-metadata-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	relatedPath := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	plans := []query.Plan{
		query.NewPlan("missing_scalar_table", []query.FieldRef{
			query.NewFieldRef("", "id", query.FieldInteger, false),
		}),
		query.NewPlan("missing_relation_table", []query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.NewFieldRef("alias", "ID", query.FieldInteger, false),
		}).WithConditions(
			query.NewRelatedCondition(relatedPath, query.LookupExact, query.String("Ada")),
		),
	}
	for index, plan := range plans {
		rows, queryErr := backend.Query(ctx, plan)
		if rows != nil {
			t.Fatalf("Query(plan %d) rows = %#v, want nil", index, rows)
		}
		if !errors.Is(queryErr, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
			t.Fatalf("Query(plan %d) error = %v, want query invalid_plan", index, queryErr)
		}
		if got := backend.QueryCount(); got != 0 {
			t.Fatalf("QueryCount() after plan %d = %d, want zero pre-I/O rejection", index, got)
		}
	}
}

func TestCompileAggregateUsesDerivedSourceForDistinctOrExplicitSlice(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	result, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	base, err := query.NewPlan("news_article", []query.FieldRef{id, title}).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}
	limited, err := base.WithLimit(0)
	if err != nil {
		t.Fatalf("WithLimit(0) error = %v", err)
	}
	offset, err := base.WithOffset(0)
	if err != nil {
		t.Fatalf("WithOffset(0) error = %v", err)
	}

	tests := []struct {
		name          string
		plan          query.Plan
		wantSQL       string
		wantArguments []any
	}{
		{
			name: "distinct",
			plan: base.WithDistinct(),
			wantSQL: `SELECT COUNT(*), MAX("godj_aggregate_source"."id") FROM (` +
				`SELECT DISTINCT "id", "title" FROM "news_article" ORDER BY "id" DESC` +
				`) AS "godj_aggregate_source"`,
			wantArguments: []any{},
		},
		{
			name:          "zero limit is still an explicit slice",
			plan:          limited,
			wantSQL:       `SELECT COUNT(*), MAX("godj_aggregate_source"."id") FROM (SELECT "id", "title" FROM "news_article" ORDER BY "id" DESC LIMIT ?) AS "godj_aggregate_source"`,
			wantArguments: []any{int64(0)},
		},
		{
			name:          "zero offset is still an explicit slice",
			plan:          offset,
			wantSQL:       `SELECT COUNT(*), MAX("godj_aggregate_source"."id") FROM (SELECT "id", "title" FROM "news_article" ORDER BY "id" DESC LIMIT -1 OFFSET ?) AS "godj_aggregate_source"`,
			wantArguments: []any{int64(0)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			statement, arguments, compileErr := sqlite.Compile(test.plan)
			if compileErr != nil {
				t.Fatalf("Compile() error = %v", compileErr)
			}
			if statement != test.wantSQL {
				t.Fatalf("SQL = %q\nwant  %q", statement, test.wantSQL)
			}
			if !reflect.DeepEqual(arguments, test.wantArguments) {
				t.Fatalf("arguments = %#v, want %#v", arguments, test.wantArguments)
			}
		})
	}
}

func TestCompiledDirectAggregatePreservesEmptyNullableMax(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "compiler-direct-aggregate-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE "compiler_direct_aggregate" ("id" INTEGER NOT NULL PRIMARY KEY, "summary" TEXT NULL, "published" BOOLEAN NOT NULL)`,
		`INSERT INTO "compiler_direct_aggregate" ("id", "summary", "published") VALUES (1, NULL, TRUE), (2, 'Second', TRUE), (3, 'Third', FALSE)`,
	} {
		if _, execErr := backend.ExecContext(ctx, statement); execErr != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, execErr)
		}
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	result, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(summary))
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	base := query.NewPlan("compiler_direct_aggregate", []query.FieldRef{id, summary, published})
	filtered, err := base.
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape(filtered) error = %v", err)
	}

	rows, err := backend.Query(ctx, filtered)
	if err != nil {
		t.Fatalf("Query(filtered) error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("filtered aggregate returned no row: %v", rows.Err())
	}
	var count int64
	var maximum sql.NullString
	if scanErr := rows.Scan(&count, &maximum); scanErr != nil {
		t.Fatalf("Scan(filtered) error = %v", scanErr)
	}
	if count != 2 || maximum != (sql.NullString{String: "Second", Valid: true}) {
		t.Fatalf("filtered aggregate = (%d, %#v), want (2, Second)", count, maximum)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("Rows.Close(filtered) = %v", closeErr)
	}

	empty, err := base.
		WithConditions(query.NewCondition(summary, query.LookupExact, query.String("missing"))).
		WithResultShape(result)
	if err != nil {
		t.Fatalf("WithResultShape(empty) error = %v", err)
	}
	rows, err = backend.Query(ctx, empty)
	if err != nil {
		t.Fatalf("Query(empty) error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("empty aggregate returned no row: %v", rows.Err())
	}
	count = -1
	maximum = sql.NullString{String: "unexpected", Valid: true}
	if scanErr := rows.Scan(&count, &maximum); scanErr != nil {
		t.Fatalf("Scan(empty) error = %v", scanErr)
	}
	if count != 0 || maximum.Valid {
		t.Fatalf("empty aggregate = (%d, %#v), want (0, NULL)", count, maximum)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("Rows.Close(empty) = %v", closeErr)
	}
}

func TestCompileProjectionAndAggregateRejectRelationPaths(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	path := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	base := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).WithConditions(
		query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")),
	)
	projection, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}

	for _, result := range []query.ResultShape{projection, aggregate} {
		plan, shapeErr := base.WithResultShape(result)
		if shapeErr != nil {
			t.Fatalf("WithResultShape(%q) error = %v", result.Kind(), shapeErr)
		}
		_, _, compileErr := sqlite.Compile(plan)
		if !errors.Is(compileErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
			t.Fatalf("Compile(%q) error = %v, want backend unsupported_feature", result.Kind(), compileErr)
		}
	}
}

func TestCompileRelationModelSupportsDistinctAndOffset(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	path := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	plan, err := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).
		WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.String("Ada"))).
		WithOrderings(query.NewOrdering(id, query.Ascending)).
		WithDistinct().
		WithOffset(3)
	if err != nil {
		t.Fatalf("WithOffset() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT DISTINCT "t0"."id", "t0"."title", "t0"."author_id" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = ? ORDER BY "t0"."id" ASC LIMIT -1 OFFSET ?`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{"Ada", int64(3)}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompiledProjectionAndAggregateExecuteWithSQLiteSemantics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "compiler-result-shapes-"+t.Name())
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE "compiler_article" ("id" INTEGER NOT NULL PRIMARY KEY, "title" TEXT NOT NULL, "published" BOOLEAN NOT NULL)`,
		`INSERT INTO "compiler_article" ("id", "title", "published") VALUES (1, 'Alpha', TRUE), (2, 'Alpha', TRUE), (3, 'Beta', TRUE), (4, 'Gamma', FALSE)`,
	} {
		if _, execErr := backend.ExecContext(ctx, statement); execErr != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, execErr)
		}
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	projection, err := query.NewProjectionResult(title)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	projected, err := query.NewPlan("compiler_article", []query.FieldRef{id, title, published}).
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(title, query.Ascending)).
		WithDistinct().
		WithResultShape(projection)
	if err != nil {
		t.Fatalf("WithResultShape(projection) error = %v", err)
	}
	projected, err = projected.WithOffset(1)
	if err != nil {
		t.Fatalf("WithOffset(projection) error = %v", err)
	}
	rows, err := backend.Query(ctx, projected)
	if err != nil {
		t.Fatalf("Query(projection) error = %v", err)
	}
	var projectedTitles []string
	for rows.Next() {
		var got string
		if scanErr := rows.Scan(&got); scanErr != nil {
			t.Fatalf("Scan(projection) error = %v", scanErr)
		}
		projectedTitles = append(projectedTitles, got)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("Rows.Err(projection) = %v", rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("Rows.Close(projection) = %v", closeErr)
	}
	if want := []string{"Beta"}; !reflect.DeepEqual(projectedTitles, want) {
		t.Fatalf("projection titles = %#v, want %#v", projectedTitles, want)
	}

	aggregate, err := query.NewAggregateResult(
		query.CountAllResult(),
		query.MaxResult(id),
		query.MaxResult(title),
	)
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	aggregated, err := query.NewPlan("compiler_article", []query.FieldRef{id, title, published}).
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(id, query.Ascending)).
		WithDistinct().
		WithResultShape(aggregate)
	if err != nil {
		t.Fatalf("WithResultShape(aggregate) error = %v", err)
	}
	aggregated, err = aggregated.WithLimit(1)
	if err != nil {
		t.Fatalf("WithLimit(aggregate) error = %v", err)
	}
	aggregated, err = aggregated.WithOffset(1)
	if err != nil {
		t.Fatalf("WithOffset(aggregate) error = %v", err)
	}
	rows, err = backend.Query(ctx, aggregated)
	if err != nil {
		t.Fatalf("Query(aggregate) error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("aggregate returned no row: %v", rows.Err())
	}
	var count int64
	var maximumID sql.NullInt64
	var maximumTitle sql.NullString
	if scanErr := rows.Scan(&count, &maximumID, &maximumTitle); scanErr != nil {
		t.Fatalf("Scan(aggregate) error = %v", scanErr)
	}
	if count != 1 || maximumID != (sql.NullInt64{Int64: 2, Valid: true}) ||
		maximumTitle != (sql.NullString{String: "Alpha", Valid: true}) {
		t.Fatalf("aggregate = (%d, %#v, %#v), want (1, 2, Alpha)", count, maximumID, maximumTitle)
	}
	if rows.Next() {
		t.Fatal("aggregate returned more than one row")
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("Rows.Err(aggregate) = %v", rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("Rows.Close(aggregate) = %v", closeErr)
	}

	empty := query.NewPlan("compiler_article", []query.FieldRef{id, title, published}).WithConditions(
		query.NewCondition(title, query.LookupExact, query.String("missing")),
	)
	empty, err = empty.WithResultShape(aggregate)
	if err != nil {
		t.Fatalf("WithResultShape(empty aggregate) error = %v", err)
	}
	rows, err = backend.Query(ctx, empty)
	if err != nil {
		t.Fatalf("Query(empty aggregate) error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("empty aggregate returned no row: %v", rows.Err())
	}
	count = -1
	maximumID = sql.NullInt64{Int64: -1, Valid: true}
	maximumTitle = sql.NullString{String: "unexpected", Valid: true}
	if scanErr := rows.Scan(&count, &maximumID, &maximumTitle); scanErr != nil {
		t.Fatalf("Scan(empty aggregate) error = %v", scanErr)
	}
	if count != 0 || maximumID.Valid || maximumTitle.Valid {
		t.Fatalf("empty aggregate = (%d, %#v, %#v), want (0, NULL, NULL)", count, maximumID, maximumTitle)
	}
	if rows.Next() {
		t.Fatal("empty aggregate returned more than one row")
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("Rows.Err(empty aggregate) = %v", rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("Rows.Close(empty aggregate) = %v", closeErr)
	}
}

func TestCompileIsNullHasNoBoundArgument(t *testing.T) {
	t.Parallel()

	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	plan := query.NewPlan("news_article", []query.FieldRef{summary}).WithConditions(
		query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)),
	)
	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if statement != `SELECT "summary" FROM "news_article" WHERE "summary" IS NOT NULL` {
		t.Fatalf("SQL = %q", statement)
	}
	if len(arguments) != 0 {
		t.Fatalf("arguments = %#v, want empty", arguments)
	}
}

func TestCompileRootInConditionsPreserveValueOrder(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	idIn, err := query.NewInCondition(id, []query.Value{
		query.Integer(3),
		query.Integer(1),
		query.Integer(2),
	})
	if err != nil {
		t.Fatalf("NewInCondition(id) error = %v", err)
	}
	titleIn, err := query.NewInCondition(title, []query.Value{query.String("Beta"), query.String("Alpha")})
	if err != nil {
		t.Fatalf("NewInCondition(title) error = %v", err)
	}
	publishedIn, err := query.NewInCondition(published, []query.Value{query.Boolean(true), query.Boolean(false)})
	if err != nil {
		t.Fatalf("NewInCondition(published) error = %v", err)
	}
	plan := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(idIn, titleIn, publishedIn).
		WithOrderings(query.NewOrdering(id, query.Ascending))

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "id", "title", "published" FROM "news_article" WHERE "id" IN (?, ?, ?) AND "title" IN (?, ?) AND "published" IN (?, ?) ORDER BY "id" ASC`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	wantArguments := []any{int64(3), int64(1), int64(2), "Beta", "Alpha", true, false}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileRejectsScalarAndRelatedInConditions(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	_, _, err := sqlite.Compile(query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(
		query.NewCondition(id, query.LookupIn, query.Integer(1)),
	))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("scalar IN error = %v, want query invalid_plan", err)
	}

	authorNamePath := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(authorNamePath, query.LookupIn, query.String("Ada")),
	))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("related IN error = %v, want query invalid_plan", err)
	}
}

func TestCompileRejectsConditionFromOtherModel(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	otherID := query.NewFieldRef("id", "other_id", query.FieldInteger, false)
	plan := query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(
		query.NewCondition(otherID, query.LookupExact, query.Integer(1)),
	)
	_, _, err := sqlite.Compile(plan)
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("error = %v, want invalid_plan", err)
	}
}

func TestCompileRequiredForwardRelationQualifiesAndReusesJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorNamePath := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	authorIDPath := requiredAuthorPath(t, query.NewFieldRef("id", "id", query.FieldInteger, false))
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).WithConditions(
		query.NewRelatedCondition(authorNamePath, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(authorIDPath, query.LookupExact, query.Integer(1)),
	).WithOrderings(query.NewOrdering(id, query.Ascending))

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "t0"."id", "t0"."title", "t0"."author_id" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = ? AND "t1"."id" = ? ORDER BY "t0"."id" ASC`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{"Ada", int64(1)}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileReverseRelationInvertsRootAndReusesJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	titlePath := reversePostsPath(t, "authors_author", "author", "author_id", "posts", false,
		query.NewFieldRef("title", "title", query.FieldString, false))
	postIDPath := reversePostsPath(t, "authors_author", "author", "author_id", "posts", false,
		query.NewFieldRef("id", "id", query.FieldInteger, false))
	plan := query.NewPlan("authors_author", []query.FieldRef{id, name}).WithConditions(
		query.NewRelatedCondition(titlePath, query.LookupExact, query.String("Alpha")),
		query.NewRelatedCondition(postIDPath, query.LookupExact, query.Integer(10)),
	).WithOrderings(query.NewOrdering(id, query.Ascending))

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "t0"."id", "t0"."name" FROM "authors_author" AS "t0" INNER JOIN "blog_post" AS "t1" ON "t0"."id" = "t1"."author_id" WHERE "t1"."title" = ? AND "t1"."id" = ? ORDER BY "t0"."id" ASC`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{"Alpha", int64(10)}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileNullableReverseTargetPredicateStillUsesInnerJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	path := reversePostsPath(t, "authors_author", "reviewer", "reviewer_id", "reviewed_posts", true,
		query.NewFieldRef("title", "title", query.FieldString, false))
	plan := query.NewPlan("authors_author", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(path, query.LookupExact, query.String("Gamma")),
	)

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "t0"."id" FROM "authors_author" AS "t0" INNER JOIN "blog_post" AS "t1" ON "t0"."id" = "t1"."reviewer_id" WHERE "t1"."title" = ?`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{"Gamma"}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileForwardAndReverseSelfEdgesHaveDistinctJoinKeys(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	identity := ir.ModelIdentity{AppLabel: "people", ModelName: "person"}
	forward, err := query.NewForwardRelationPath(
		identity, "people_person", "manager", "manager_id",
		identity, "people_person", "id", false, name,
	)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := query.NewReverseRelationPath(
		identity, "people_person", "manager", "manager_id",
		identity, "people_person", "id", "reports", false, name,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("people_person", []query.FieldRef{id, name}).WithConditions(
		query.NewRelatedCondition(forward, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(reverse, query.LookupExact, query.String("Bob")),
	)

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantSQL := `SELECT "t0"."id", "t0"."name" FROM "people_person" AS "t0" INNER JOIN "people_person" AS "t1" ON "t0"."manager_id" = "t1"."id" INNER JOIN "people_person" AS "t2" ON "t0"."id" = "t2"."manager_id" WHERE "t1"."name" = ? AND "t2"."name" = ?`
	if statement != wantSQL {
		t.Fatalf("SQL = %q\nwant  %q", statement, wantSQL)
	}
	if want := []any{"Ada", "Bob"}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func TestCompileReverseRelationRejectsRootMismatchAndNonExact(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	terminal := query.NewFieldRef("title", "title", query.FieldString, false)
	wrongRoot := reversePostsPath(t, "other_author", "author", "author_id", "posts", false, terminal)
	_, _, err := sqlite.Compile(query.NewPlan("authors_author", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupExact, query.String("Alpha")),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)

	path := reversePostsPath(t, "authors_author", "author", "author_id", "posts", false, terminal)
	_, _, err = sqlite.Compile(query.NewPlan("authors_author", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(path, query.LookupIContains, query.String("Alpha")),
	))
	assertQueryCode(t, err, query.CodeUnsupported)
}

func TestCompileRelationJoinAliasesAreCanonicalRatherThanConditionOrdered(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	author := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	editor, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "editor", "editor_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false,
		query.NewFieldRef("name", "name", query.FieldString, false),
	)
	if err != nil {
		t.Fatal(err)
	}
	left := query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(editor, query.LookupExact, query.String("Bob")),
		query.NewRelatedCondition(author, query.LookupExact, query.String("Ada")),
	)
	right := query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(author, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(editor, query.LookupExact, query.String("Bob")),
	)
	leftSQL, _, err := sqlite.Compile(left)
	if err != nil {
		t.Fatal(err)
	}
	rightSQL, _, err := sqlite.Compile(right)
	if err != nil {
		t.Fatal(err)
	}
	wantJoins := `INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" INNER JOIN "authors_author" AS "t2" ON "t0"."editor_id" = "t2"."id"`
	if !strings.Contains(leftSQL, wantJoins) || !strings.Contains(rightSQL, wantJoins) {
		t.Fatalf("canonical joins missing:\nleft  %s\nright %s", leftSQL, rightSQL)
	}
}

func TestCompileRelationRejectsRootMismatchAndConflictingRepeatedEdge(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	terminal := query.NewFieldRef("name", "name", query.FieldString, false)
	wrongRoot, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"other_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupExact, query.String("Ada")),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)

	first := requiredAuthorPath(t, terminal)
	conflict, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "writer_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(first, query.LookupExact, query.String("Ada")),
		query.NewRelatedCondition(conflict, query.LookupExact, query.String("Ada")),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func TestCompileRelationRejectsNonExactAndWrongValueKind(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	path := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	_, _, err := sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(path, query.LookupIContains, query.String("Ada")),
	))
	assertQueryCode(t, err, query.CodeUnsupported)

	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id}).WithConditions(
		query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func TestCompileNullableForwardSourceKeyIsNullTrimsJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)

	for _, test := range []struct {
		name      string
		value     bool
		predicate string
	}{
		{name: "null", value: true, predicate: "IS NULL"},
		{name: "not null", value: false, predicate: "IS NOT NULL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := nullableReviewerPath(t, reviewerID)
			plan := query.NewPlan("blog_post", []query.FieldRef{id, title, reviewerID}).WithConditions(
				query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(test.value)),
			).WithOrderings(query.NewOrdering(id, query.Ascending))

			statement, arguments, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			want := `SELECT "t0"."id", "t0"."title", "t0"."reviewer_id" FROM "blog_post" AS "t0" WHERE "t0"."reviewer_id" ` + test.predicate + ` ORDER BY "t0"."id" ASC`
			if statement != want {
				t.Fatalf("SQL = %q\nwant  %q", statement, want)
			}
			if len(arguments) != 0 {
				t.Fatalf("arguments = %#v, want empty", arguments)
			}
			if strings.Contains(statement, " JOIN ") {
				t.Fatalf("source-key isnull SQL unexpectedly contains JOIN: %s", statement)
			}
		})
	}
}

func TestCompileNullableForwardSourceKeyCanCoexistWithRequiredJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	author := requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false))
	reviewer := nullableReviewerPath(t, reviewerID)
	plan := query.NewPlan("blog_post", []query.FieldRef{id, authorID, reviewerID}).WithConditions(
		query.NewRelatedCondition(reviewer, query.LookupIsNull, query.Boolean(true)),
		query.NewRelatedCondition(author, query.LookupExact, query.String("Ada")),
	)

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := `SELECT "t0"."id", "t0"."author_id", "t0"."reviewer_id" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t0"."reviewer_id" IS NULL AND "t1"."name" = ?`
	if statement != want {
		t.Fatalf("SQL = %q\nwant  %q", statement, want)
	}
	if wantArguments := []any{"Ada"}; !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileRequiredForwardProjectionSelectsRootThenTargetAndReusesPredicateJoin(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	projection := forwardProjection(t, authorID, targetID, []query.FieldRef{targetID, targetName})
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID, reviewerID}).WithConditions(
		query.NewRelatedCondition(requiredAuthorPath(t, targetName), query.LookupExact, query.String("Ada")),
	).WithOrderings(query.NewOrdering(id, query.Ascending))
	plan, err := plan.WithRelationProjection(projection)
	if err != nil {
		t.Fatalf("WithRelationProjection() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := `SELECT "t0"."id", "t0"."title", "t0"."author_id", "t0"."reviewer_id", "t1"."id", "t1"."name" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = ? ORDER BY "t0"."id" ASC`
	if statement != want {
		t.Fatalf("SQL = %q\nwant  %q", statement, want)
	}
	if strings.Count(statement, " INNER JOIN ") != 1 {
		t.Fatalf("required projection did not reuse predicate JOIN: %s", statement)
	}
	if wantArguments := []any{"Ada"}; !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileNullableForwardProjectionUsesLeftOuterJoinAndPreservesRootPlan(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	projection := forwardProjection(t, reviewerID, targetID, []query.FieldRef{targetID, targetName})
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID, reviewerID}).WithConditions(
		query.NewCondition(title, query.LookupIContains, query.String("a")),
	).WithOrderings(query.NewOrdering(id, query.Descending))
	plan, err := plan.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithRelationProjection(projection)
	if err != nil {
		t.Fatalf("WithRelationProjection() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := `SELECT "t0"."id", "t0"."title", "t0"."author_id", "t0"."reviewer_id", "t1"."id", "t1"."name" FROM "blog_post" AS "t0" LEFT OUTER JOIN "authors_author" AS "t1" ON "t0"."reviewer_id" = "t1"."id" WHERE "t0"."title" LIKE ? ESCAPE '\' ORDER BY "t0"."id" DESC LIMIT ?`
	if statement != want {
		t.Fatalf("SQL = %q\nwant  %q", statement, want)
	}
	if strings.Contains(statement, " INNER JOIN ") || strings.Count(statement, " LEFT OUTER JOIN ") != 1 {
		t.Fatalf("nullable projection JOIN shape = %s", statement)
	}
	if wantArguments := []any{"%a%", int64(2)}; !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileForwardProjectionRejectsUnrelatedRelationPredicate(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	projection := forwardProjection(t, authorID, targetID, []query.FieldRef{targetID, targetName})
	editorPath, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "editor", "editor_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, targetName,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("blog_post", []query.FieldRef{id, authorID}).WithConditions(
		query.NewRelatedCondition(editorPath, query.LookupExact, query.String("Ada")),
	)
	plan, err = plan.WithRelationProjection(projection)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(plan)
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func TestCompileForwardProjectionChecksMatchingSourceKeyProvenance(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	targetName := query.NewFieldRef("name", "name", query.FieldString, false)
	reviewerProjection := forwardProjection(t, reviewerID, targetID, []query.FieldRef{targetID, targetName})

	t.Run("same hop remains valid", func(t *testing.T) {
		path := nullableReviewerPath(t, reviewerID)
		plan := query.NewPlan("blog_post", []query.FieldRef{id, reviewerID}).WithConditions(
			query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(true)),
		)
		plan, err := plan.WithRelationProjection(reviewerProjection)
		if err != nil {
			t.Fatal(err)
		}
		statement, arguments, err := sqlite.Compile(plan)
		if err != nil {
			t.Fatalf("Compile() error = %v", err)
		}
		want := `SELECT "t0"."id", "t0"."reviewer_id", "t1"."id", "t1"."name" FROM "blog_post" AS "t0" LEFT OUTER JOIN "authors_author" AS "t1" ON "t0"."reviewer_id" = "t1"."id" WHERE "t0"."reviewer_id" IS NULL`
		if statement != want || len(arguments) != 0 {
			t.Fatalf("Compile() = (%q, %#v), want (%q, no arguments)", statement, arguments, want)
		}
	})

	validSource := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	validTarget := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	for _, test := range []struct {
		name        string
		source      ir.ModelIdentity
		target      ir.ModelIdentity
		targetTable string
		targetPK    string
	}{
		{name: "source identity mismatch", source: ir.ModelIdentity{AppLabel: "news", ModelName: "post"}, target: validTarget, targetTable: "authors_author", targetPK: "id"},
		{name: "target identity mismatch", source: validSource, target: ir.ModelIdentity{AppLabel: "people", ModelName: "person"}, targetTable: "authors_author", targetPK: "id"},
		{name: "target table mismatch", source: validSource, target: validTarget, targetTable: "people_person", targetPK: "id"},
		{name: "target primary key mismatch", source: validSource, target: validTarget, targetTable: "authors_author", targetPK: "uuid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, err := query.NewNullableForwardRelationIsNullPath(
				test.source,
				"blog_post",
				reviewerID,
				test.target,
				test.targetTable,
				test.targetPK,
			)
			if err != nil {
				t.Fatal(err)
			}
			plan := query.NewPlan("blog_post", []query.FieldRef{id, reviewerID}).WithConditions(
				query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(true)),
			)
			plan, err = plan.WithRelationProjection(reviewerProjection)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = sqlite.Compile(plan)
			assertQueryCode(t, err, query.CodeInvalidPlan)
		})
	}

	t.Run("unrelated nullable source key remains a root filter", func(t *testing.T) {
		path := nullableReviewerPath(t, reviewerID)
		authorProjection := forwardProjection(t, authorID, targetID, []query.FieldRef{targetID, targetName})
		plan := query.NewPlan("blog_post", []query.FieldRef{id, authorID, reviewerID}).WithConditions(
			query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(true)),
		)
		plan, err := plan.WithRelationProjection(authorProjection)
		if err != nil {
			t.Fatal(err)
		}
		statement, arguments, err := sqlite.Compile(plan)
		if err != nil {
			t.Fatalf("Compile() error = %v", err)
		}
		want := `SELECT "t0"."id", "t0"."author_id", "t0"."reviewer_id", "t1"."id", "t1"."name" FROM "blog_post" AS "t0" INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t0"."reviewer_id" IS NULL`
		if statement != want || len(arguments) != 0 {
			t.Fatalf("Compile() = (%q, %#v), want (%q, no arguments)", statement, arguments, want)
		}
	})
}

func forwardProjection(
	t *testing.T,
	sourceKey query.FieldRef,
	targetKey query.FieldRef,
	targetColumns []query.FieldRef,
) query.RelationProjection {
	t.Helper()
	projection, err := query.NewForwardRelationProjection(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		sourceKey,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		targetKey,
		targetColumns,
	)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func TestCompileNullableForwardSourceKeyRejectsMutationBeforeIO(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	reviewerID := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	path := nullableReviewerPath(t, reviewerID)

	tests := []struct {
		name    string
		columns []query.FieldRef
		path    query.RelationPath
		lookup  query.Lookup
		value   query.Value
		code    string
	}{
		{
			name:    "source key missing from columns",
			columns: []query.FieldRef{id},
			path:    path,
			lookup:  query.LookupIsNull,
			value:   query.Boolean(true),
			code:    query.CodeInvalidPlan,
		},
		{
			name:    "source key metadata differs",
			columns: []query.FieldRef{id, query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, false)},
			path:    path,
			lookup:  query.LookupIsNull,
			value:   query.Boolean(true),
			code:    query.CodeInvalidPlan,
		},
		{
			name:    "wrong lookup",
			columns: []query.FieldRef{id, reviewerID},
			path:    path,
			lookup:  query.LookupExact,
			value:   query.Boolean(true),
			code:    query.CodeUnsupported,
		},
		{
			name:    "wrong value kind",
			columns: []query.FieldRef{id, reviewerID},
			path:    path,
			lookup:  query.LookupIsNull,
			value:   query.String("true"),
			code:    query.CodeInvalidPlan,
		},
		{
			name:    "non canonical identity",
			columns: []query.FieldRef{id, reviewerID},
			path: nullableReviewerPathWithIdentity(t, reviewerID,
				ir.ModelIdentity{AppLabel: "Blog", ModelName: "post"}),
			lookup: query.LookupIsNull,
			value:  query.Boolean(true),
			code:   query.CodeInvalidPlan,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := sqlite.Compile(query.NewPlan("blog_post", test.columns).WithConditions(
				query.NewRelatedCondition(test.path, test.lookup, test.value),
			))
			assertQueryCode(t, err, test.code)
		})
	}

	wrongRoot, err := query.NewNullableForwardRelationIsNullPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"other_post", reviewerID,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = sqlite.Compile(query.NewPlan("blog_post", []query.FieldRef{id, reviewerID}).WithConditions(
		query.NewRelatedCondition(wrongRoot, query.LookupIsNull, query.Boolean(true)),
	))
	assertQueryCode(t, err, query.CodeInvalidPlan)
}

func requiredAuthorPath(t *testing.T, terminal query.FieldRef) query.RelationPath {
	t.Helper()
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func reversePostsPath(
	t *testing.T,
	targetTable, sourceField, sourceColumn, reverseName string,
	nullable bool,
	terminal query.FieldRef,
) query.RelationPath {
	t.Helper()
	path, err := query.NewReverseRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", sourceField, sourceColumn,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		targetTable, "id", reverseName, nullable, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func nullableReviewerPath(t *testing.T, sourceKey query.FieldRef) query.RelationPath {
	t.Helper()
	return nullableReviewerPathWithIdentity(t, sourceKey, ir.ModelIdentity{AppLabel: "blog", ModelName: "post"})
}

func nullableReviewerPathWithIdentity(
	t *testing.T,
	sourceKey query.FieldRef,
	source ir.ModelIdentity,
) query.RelationPath {
	t.Helper()
	path, err := query.NewNullableForwardRelationIsNullPath(
		source,
		"blog_post",
		sourceKey,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		"id",
	)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertQueryCode(t *testing.T, err error, code string) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != code {
		t.Fatalf("error = %v, want query error code %q", err, code)
	}
}
