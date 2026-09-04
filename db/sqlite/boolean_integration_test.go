package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestSQLiteBooleanPredicatesExecuteNullableNegationAndReuseResultShapes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "boolean-expression-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "news_article" (
		"id" INTEGER PRIMARY KEY,
		"title" TEXT NOT NULL,
		"summary" TEXT NULL,
		"published" INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO "news_article" VALUES (1, 'Go Intro', NULL, 1)`,
		`INSERT INTO "news_article" VALUES (2, 'Other', 'ORM and go', 1)`,
		`INSERT INTO "news_article" VALUES (3, 'Draft Go', 'go', 1)`,
		`INSERT INTO "news_article" VALUES (4, 'Go hidden', 'go', 0)`,
		`INSERT INTO "news_article" VALUES (5, 'Other', NULL, 1)`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	source := []query.FieldRef{id, title, summary, published}

	notORM := sqliteTestNot(t, sqliteTestExpression(t,
		query.NewCondition(summary, query.LookupIContains, query.String("orm")),
	))
	negatedPlan, err := query.NewPlan("news_article", source).
		WithOrderings(query.NewOrdering(id, query.Ascending)).
		WithWhere(notORM)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := backend.Query(ctx, negatedPlan)
	if err != nil {
		t.Fatal(err)
	}
	var negatedIDs []int64
	for rows.Next() {
		var identifier int64
		var rowTitle string
		var rowSummary sql.NullString
		var rowPublished bool
		if err := rows.Scan(&identifier, &rowTitle, &rowSummary, &rowPublished); err != nil {
			t.Fatal(err)
		}
		negatedIDs = append(negatedIDs, identifier)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 3, 4, 5}; !reflect.DeepEqual(negatedIDs, want) {
		t.Fatalf("nullable NOT ids = %v, want %v", negatedIDs, want)
	}

	search := sqliteTestOr(t,
		sqliteTestExpression(t, query.NewCondition(title, query.LookupIContains, query.String("go"))),
		sqliteTestExpression(t, query.NewCondition(summary, query.LookupIContains, query.String("go"))),
	)
	where := sqliteTestAnd(t,
		search,
		sqliteTestExpression(t, query.NewCondition(published, query.LookupExact, query.Boolean(true))),
		sqliteTestNot(t, sqliteTestExpression(t,
			query.NewCondition(title, query.LookupIContains, query.String("draft")),
		)),
	)
	base, err := query.NewPlan("news_article", source).
		WithOrderings(query.NewOrdering(id, query.Ascending)).
		WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := query.NewProjectionResult(id)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := base.WithResultShape(projection)
	if err != nil {
		t.Fatal(err)
	}
	projectedRows, err := backend.Query(ctx, projected)
	if err != nil {
		t.Fatal(err)
	}
	var projectedIDs []int64
	for projectedRows.Next() {
		var identifier int64
		if err := projectedRows.Scan(&identifier); err != nil {
			t.Fatal(err)
		}
		projectedIDs = append(projectedIDs, identifier)
	}
	if err := projectedRows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := projectedRows.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2}; !reflect.DeepEqual(projectedIDs, want) {
		t.Fatalf("projected ids = %v, want %v", projectedIDs, want)
	}

	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatal(err)
	}
	report, err := base.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	reportRows, err := backend.Query(ctx, report)
	if err != nil {
		t.Fatal(err)
	}
	if !reportRows.Next() {
		t.Fatal("aggregate query returned no row")
	}
	var count int64
	var maximum sql.NullInt64
	if err := reportRows.Scan(&count, &maximum); err != nil {
		t.Fatal(err)
	}
	if reportRows.Next() {
		t.Fatal("aggregate query returned more than one row")
	}
	if err := reportRows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := reportRows.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 2 || !maximum.Valid || maximum.Int64 != 2 {
		t.Fatalf("aggregate = count:%d max:%+v, want count:2 max:2", count, maximum)
	}
	if backend.QueryCount() != 3 {
		t.Fatalf("query count = %d, want 3", backend.QueryCount())
	}
}

func TestSQLiteNullableNegationParityExecutesAcrossLookups(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "nullable-negation-parity-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "nullable_article" (
		"id" INTEGER PRIMARY KEY,
		"summary" TEXT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "nullable_article" VALUES
		(1, NULL),
		(2, 'ORM'),
		(3, 'go'),
		(4, 'other')`); err != nil {
		t.Fatal(err)
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	inCondition, err := query.NewInCondition(summary, []query.Value{query.String("ORM"), query.String("go")})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := query.NewProjectionResult(id)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		condition query.Condition
		negations int
		want      []int64
	}{
		{name: "exact odd", condition: query.NewCondition(summary, query.LookupExact, query.String("ORM")), negations: 1, want: []int64{1, 3, 4}},
		{name: "exact even", condition: query.NewCondition(summary, query.LookupExact, query.String("ORM")), negations: 2, want: []int64{2}},
		{name: "exact triple", condition: query.NewCondition(summary, query.LookupExact, query.String("ORM")), negations: 3, want: []int64{1, 3, 4}},
		{name: "icontains odd", condition: query.NewCondition(summary, query.LookupIContains, query.String("orm")), negations: 1, want: []int64{1, 3, 4}},
		{name: "icontains even", condition: query.NewCondition(summary, query.LookupIContains, query.String("orm")), negations: 2, want: []int64{2}},
		{name: "in odd", condition: inCondition, negations: 1, want: []int64{1, 4}},
		{name: "in even", condition: inCondition, negations: 2, want: []int64{2, 3}},
		{name: "isnull true odd", condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(true)), negations: 1, want: []int64{2, 3, 4}},
		{name: "isnull true even", condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(true)), negations: 2, want: []int64{1}},
		{name: "isnull false odd", condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)), negations: 1, want: []int64{1}},
		{name: "isnull false even", condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)), negations: 2, want: []int64{2, 3, 4}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression := sqliteTestExpression(t, test.condition)
			for index := 0; index < test.negations; index++ {
				expression = sqliteTestNot(t, expression)
			}
			plan, err := query.NewPlan("nullable_article", []query.FieldRef{id, summary}).
				WithOrderings(query.NewOrdering(id, query.Ascending)).
				WithWhere(expression)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = plan.WithResultShape(projection)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := backend.Query(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			for rows.Next() {
				var identifier int64
				if err := rows.Scan(&identifier); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				got = append(got, identifier)
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("IDs = %v, want %v", got, test.want)
			}
		})
	}
	if got := backend.QueryCount(); got != uint64(len(tests)) {
		t.Fatalf("query count = %d, want %d", got, len(tests))
	}
}

func TestSQLiteBooleanInvalidTreesAndRelationCompositionRemainPreIO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := sqlite.OpenMemory(ctx, "boolean-pre-io-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	condition := query.NewCondition(id, query.LookupExact, query.Integer(1))
	conditions := make([]query.Condition, 1024)
	for index := range conditions {
		conditions[index] = condition
	}
	if _, err := backend.Query(ctx, query.NewPlan("missing_table", []query.FieldRef{id}).WithConditions(conditions...)); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Query(over-limit) error = %v, want query invalid_plan", err)
	}
	if backend.QueryCount() != 0 {
		t.Fatalf("over-limit query count = %d, want 0", backend.QueryCount())
	}

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	related := sqliteTestExpression(t, query.NewRelatedCondition(
		requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false)),
		query.LookupExact,
		query.String("Ada"),
	))
	scalar := sqliteTestExpression(t, query.NewCondition(title, query.LookupExact, query.String("Alpha")))
	relationOr := sqliteTestOr(t, related, scalar)
	if _, err := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).WithWhere(relationOr); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported}) {
		t.Fatalf("WithWhere(relation OR) error = %v, want query unsupported", err)
	}
	relationNot := sqliteTestNot(t, related)
	if _, err := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).WithWhere(relationNot); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported}) {
		t.Fatalf("WithWhere(relation NOT) error = %v, want query unsupported", err)
	}
	if backend.QueryCount() != 0 {
		t.Fatalf("relation OR/NOT query count = %d, want 0", backend.QueryCount())
	}
}
