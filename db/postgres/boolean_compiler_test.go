package postgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCompileBooleanExpressionPreservesPrecedenceAndDFSArguments(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)

	titleMatch := mustPostgresExpression(t, query.NewCondition(title, query.LookupIContains, query.String(`50%_Go\SQL`)))
	summaryMatch := mustPostgresExpression(t, query.NewCondition(summary, query.LookupExact, query.String("ORM")))
	either, err := query.OrExpressions(titleMatch, summaryMatch)
	if err != nil {
		t.Fatal(err)
	}
	publishedMatch := mustPostgresExpression(t, query.NewCondition(published, query.LookupExact, query.Boolean(true)))
	excluded := mustPostgresExpression(t, query.NewCondition(title, query.LookupIContains, query.String("draft")))
	excluded, err = query.NotExpression(excluded)
	if err != nil {
		t.Fatal(err)
	}
	where, err := query.AndExpressions(either, publishedMatch, excluded)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, summary, published}).WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "title", "summary", "published" FROM "godj_app"."news_article" WHERE ((("title" ILIKE $1 ESCAPE '\') OR ("summary" = $2)) AND ("published" = $3) AND (NOT ("title" ILIKE $4 ESCAPE '\')))`
	if statement != want {
		t.Fatalf("SQL = %q\nwant  %q", statement, want)
	}
	wantArguments := []any{`%50\%\_Go\\SQL%`, "ORM", true, "%draft%"}
	if !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", arguments, wantArguments)
	}
}

func TestCompileNullableNegationUsesDjangoNullGuards(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	in, err := query.NewInCondition(summary, []query.Value{query.String("ORM"), query.String("Go")})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		condition query.Condition
		wantWhere string
		wantArgs  []any
	}{
		{
			name:      "exact",
			condition: query.NewCondition(summary, query.LookupExact, query.String("ORM")),
			wantWhere: `(NOT ("summary" = $1 AND "summary" IS NOT NULL))`,
			wantArgs:  []any{"ORM"},
		},
		{
			name:      "icontains",
			condition: query.NewCondition(summary, query.LookupIContains, query.String("orm")),
			wantWhere: `(NOT ("summary" ILIKE $1 ESCAPE '\' AND "summary" IS NOT NULL))`,
			wantArgs:  []any{"%orm%"},
		},
		{
			name:      "in",
			condition: in,
			wantWhere: `(NOT ("summary" IN ($1, $2) AND "summary" IS NOT NULL))`,
			wantArgs:  []any{"ORM", "Go"},
		},
		{
			name:      "isnull retains its predicate",
			condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(true)),
			wantWhere: `(NOT ("summary" IS NULL))`,
			wantArgs:  []any{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expression, expressionErr := query.NotExpression(mustPostgresExpression(t, test.condition))
			if expressionErr != nil {
				t.Fatal(expressionErr)
			}
			plan, err := query.NewPlan("news_article", []query.FieldRef{id, summary}).WithWhere(expression)
			if err != nil {
				t.Fatal(err)
			}
			statement, arguments, err := compilePlan("godj_app", plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(statement, " WHERE "+test.wantWhere) {
				t.Fatalf("SQL = %q, want WHERE %q", statement, test.wantWhere)
			}
			if !reflect.DeepEqual(arguments, test.wantArgs) {
				t.Fatalf("arguments = %#v, want %#v", arguments, test.wantArgs)
			}
		})
	}
}

func TestCompileNullableNegationTracksEvenAndTripleParity(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	inCondition, err := query.NewInCondition(summary, []query.Value{query.String("ORM"), query.String("Go")})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		condition query.Condition
		negations int
		wantWhere string
		wantArgs  []any
	}{
		{
			name:      "exact even",
			condition: query.NewCondition(summary, query.LookupExact, query.String("ORM")),
			negations: 2,
			wantWhere: `(NOT (NOT ("summary" = $1)))`,
			wantArgs:  []any{"ORM"},
		},
		{
			name:      "exact triple",
			condition: query.NewCondition(summary, query.LookupExact, query.String("ORM")),
			negations: 3,
			wantWhere: `(NOT (NOT (NOT ("summary" = $1 AND "summary" IS NOT NULL))))`,
			wantArgs:  []any{"ORM"},
		},
		{
			name:      "IN even",
			condition: inCondition,
			negations: 2,
			wantWhere: `(NOT (NOT ("summary" IN ($1, $2))))`,
			wantArgs:  []any{"ORM", "Go"},
		},
		{
			name:      "IN triple",
			condition: inCondition,
			negations: 3,
			wantWhere: `(NOT (NOT (NOT ("summary" IN ($1, $2) AND "summary" IS NOT NULL))))`,
			wantArgs:  []any{"ORM", "Go"},
		},
		{
			name:      "isnull false odd",
			condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)),
			negations: 1,
			wantWhere: `(NOT ("summary" IS NOT NULL))`,
			wantArgs:  []any{},
		},
		{
			name:      "isnull false even",
			condition: query.NewCondition(summary, query.LookupIsNull, query.Boolean(false)),
			negations: 2,
			wantWhere: `(NOT (NOT ("summary" IS NOT NULL)))`,
			wantArgs:  []any{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			expression := mustPostgresExpression(t, test.condition)
			for index := 0; index < test.negations; index++ {
				var expressionErr error
				expression, expressionErr = query.NotExpression(expression)
				if expressionErr != nil {
					t.Fatal(expressionErr)
				}
			}
			plan, err := query.NewPlan("news_article", []query.FieldRef{id, summary}).WithWhere(expression)
			if err != nil {
				t.Fatal(err)
			}
			statement, arguments, err := compilePlan("godj_app", plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(statement, " WHERE "+test.wantWhere) {
				t.Fatalf("SQL = %q, want WHERE %q", statement, test.wantWhere)
			}
			if !reflect.DeepEqual(arguments, test.wantArgs) {
				t.Fatalf("arguments = %#v, want %#v", arguments, test.wantArgs)
			}
		})
	}
}

func TestCompileBooleanWhereIsSharedByEveryResultShape(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	left := mustPostgresExpression(t, query.NewCondition(title, query.LookupIContains, query.String("django")))
	right := mustPostgresExpression(t, query.NewCondition(title, query.LookupExact, query.String("Other")))
	where, err := query.OrExpressions(left, right)
	if err != nil {
		t.Fatal(err)
	}
	base, err := query.NewPlan("news_article", []query.FieldRef{id, title, published}).WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}
	wantWhere := ` WHERE (("title" ILIKE $1 ESCAPE '\') OR ("title" = $2))`
	wantArguments := []any{"%django%", "Other"}

	projection, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := base.WithResultShape(projection)
	if err != nil {
		t.Fatal(err)
	}
	directAggregate, err := base.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	derivedAggregate, err := directAggregate.WithLimit(4)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		plan query.Plan
	}{
		{name: "model", plan: base},
		{name: "projection", plan: projected},
		{name: "direct aggregate", plan: directAggregate},
		{name: "derived aggregate", plan: derivedAggregate},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statement, arguments, compileErr := compilePlan("godj_app", test.plan)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			if !strings.Contains(statement, wantWhere) {
				t.Fatalf("SQL = %q, want shared predicate %q", statement, wantWhere)
			}
			if len(arguments) < len(wantArguments) || !reflect.DeepEqual(arguments[:len(wantArguments)], wantArguments) {
				t.Fatalf("arguments = %#v, want prefix %#v", arguments, wantArguments)
			}
		})
	}
}

func TestPostgresWhereAnalysisFailsClosedOnUncheckedAndBoundedTrees(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	base := query.NewPlan("news_article", []query.FieldRef{id})
	for name, plan := range map[string]query.Plan{
		"zero condition":         base.WithConditions(query.Condition{}),
		"field outside metadata": base.WithConditions(query.NewCondition(query.NewFieldRef("other", "other", query.FieldInteger, false), query.LookupExact, query.Integer(1))),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := compilePlan("godj_app", plan)
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("compilePlan() error = %v, want query invalid_plan", err)
			}
		})
	}

	conditions := make([]query.Condition, 1024)
	for index := range conditions {
		conditions[index] = query.NewCondition(id, query.LookupExact, query.Integer(int64(index)))
	}
	_, _, err := compilePlan("godj_app", base.WithConditions(conditions...))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) ||
		!strings.Contains(err.Error(), "maximum node count of 1024") {
		t.Fatalf("oversized unchecked tree error = %v, want node-cap invalid_plan", err)
	}

	analyzer := whereAnalyzer{plan: base, sourceFields: []query.FieldRef{id}}
	if _, err := analyzer.walk(query.Expression{}, 1, true); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero expression error = %v, want query invalid_plan", err)
	}
}

func TestPostgresBooleanRelationBoundaryAndAliasOrder(t *testing.T) {
	t.Parallel()

	post := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	category := ir.ModelIdentity{AppLabel: "catalog", ModelName: "category"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	categoryKey := query.NewFieldRef("category", "category_id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	categoryName := query.NewFieldRef("name", "name", query.FieldString, false)
	authorPath, err := query.NewForwardRelationPath(
		post, "blog_post", "author", "author_id", author, "authors_author", "id", false, authorName,
	)
	if err != nil {
		t.Fatal(err)
	}
	categoryPath, err := query.NewForwardRelationPath(
		post, "blog_post", "category", "category_id", category, "catalog_category", "id", false, categoryName,
	)
	if err != nil {
		t.Fatal(err)
	}
	authorLeaf := mustPostgresExpression(t, query.NewRelatedCondition(authorPath, query.LookupExact, query.String("Ada")))
	categoryLeaf := mustPostgresExpression(t, query.NewRelatedCondition(categoryPath, query.LookupExact, query.String("Go")))
	where, err := query.AndExpressions(categoryLeaf, authorLeaf)
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("blog_post", []query.FieldRef{id, title, authorKey, categoryKey})
	plan, err := base.WithWhere(where)
	if err != nil {
		t.Fatal(err)
	}
	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	// Alias allocation is sorted by symbolic edge (author before category),
	// while the arguments retain expression DFS order (category before author).
	want := `SELECT "t0"."id", "t0"."title", "t0"."author_id", "t0"."category_id" FROM "godj_app"."blog_post" AS "t0" INNER JOIN "godj_app"."authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" INNER JOIN "godj_app"."catalog_category" AS "t2" ON "t0"."category_id" = "t2"."id" WHERE (("t2"."name" = $1) AND ("t1"."name" = $2))`
	if statement != want || !reflect.DeepEqual(arguments, []any{"Go", "Ada"}) {
		t.Fatalf("relation = %q %#v, want %q [Go Ada]", statement, arguments, want)
	}

	scalarLeaf := mustPostgresExpression(t, query.NewCondition(title, query.LookupExact, query.String("Other")))
	for name, makeExpression := range map[string]func() (query.Expression, error){
		"OR":  func() (query.Expression, error) { return query.OrExpressions(authorLeaf, scalarLeaf) },
		"NOT": func() (query.Expression, error) { return query.NotExpression(authorLeaf) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expression, expressionErr := makeExpression()
			if expressionErr != nil {
				t.Fatal(expressionErr)
			}
			analyzer := whereAnalyzer{plan: base, sourceFields: base.SourceFields()}
			_, analysisErr := analyzer.walk(expression, 1, true)
			if !errors.Is(analysisErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Field: "name", Lookup: string(query.LookupExact)}) {
				t.Fatalf("analysis error = %v, want backend unsupported", analysisErr)
			}
		})
	}
}

func mustPostgresExpression(t *testing.T, condition query.Condition) query.Expression {
	t.Helper()
	expression, err := query.NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	return expression
}
