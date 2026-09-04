package sqlite_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func TestSQLiteBooleanCompilerPreservesPrecedenceAndDFSArgumentsAcrossResultShapes(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)

	titleSearch := sqliteTestExpression(t, query.NewCondition(title, query.LookupIContains, query.String(`50%_Go`)))
	summarySearch := sqliteTestExpression(t, query.NewCondition(summary, query.LookupIContains, query.String("orm")))
	search := sqliteTestOr(t, titleSearch, summarySearch)
	publishedOnly := sqliteTestExpression(t, query.NewCondition(published, query.LookupExact, query.Boolean(true)))
	excludedTitle := sqliteTestNot(t, sqliteTestExpression(t,
		query.NewCondition(title, query.LookupIContains, query.String("draft")),
	))
	where := sqliteTestAnd(t, search, publishedOnly, excludedTitle)

	base, err := query.NewPlan("news_article", []query.FieldRef{id, title, summary, published}).
		WithWhere(where)
	if err != nil {
		t.Fatalf("WithWhere() error = %v", err)
	}
	wantWhere := `WHERE (("title" LIKE ? ESCAPE '\' OR "summary" LIKE ? ESCAPE '\') AND "published" = ? AND NOT ("title" LIKE ? ESCAPE '\'))`
	wantArguments := []any{`%50\%\_Go%`, "%orm%", true, "%draft%"}

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
	derivedAggregate, err := base.WithDistinct().WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	derivedAggregate, err = derivedAggregate.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name          string
		plan          query.Plan
		wantArguments []any
	}{
		{name: "model", plan: base, wantArguments: wantArguments},
		{name: "projection", plan: projected, wantArguments: wantArguments},
		{name: "direct aggregate", plan: directAggregate, wantArguments: wantArguments},
		{name: "derived aggregate", plan: derivedAggregate, wantArguments: append(append([]any(nil), wantArguments...), int64(2))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statement, arguments, compileErr := sqlite.Compile(test.plan)
			if compileErr != nil {
				t.Fatalf("Compile() error = %v", compileErr)
			}
			if !strings.Contains(statement, wantWhere) {
				t.Fatalf("SQL = %q, want shared predicate %q", statement, wantWhere)
			}
			if !reflect.DeepEqual(arguments, test.wantArguments) {
				t.Fatalf("arguments = %#v, want %#v", arguments, test.wantArguments)
			}
		})
	}
}

func TestSQLiteBooleanCompilerGuardsNullableLeavesAtOddNegationParity(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	inCondition, err := query.NewInCondition(summary, []query.Value{query.String("ORM"), query.String("Go")})
	if err != nil {
		t.Fatal(err)
	}
	exact := sqliteTestExpression(t, query.NewCondition(summary, query.LookupExact, query.String("ORM")))

	tests := []struct {
		name      string
		where     query.Expression
		wantWhere string
		wantArgs  []any
	}{
		{
			name:      "exact",
			where:     sqliteTestNot(t, exact),
			wantWhere: `WHERE NOT ("summary" = ? AND "summary" IS NOT NULL)`,
			wantArgs:  []any{"ORM"},
		},
		{
			name: "icontains",
			where: sqliteTestNot(t, sqliteTestExpression(t,
				query.NewCondition(summary, query.LookupIContains, query.String("orm")),
			)),
			wantWhere: `WHERE NOT ("summary" LIKE ? ESCAPE '\' AND "summary" IS NOT NULL)`,
			wantArgs:  []any{"%orm%"},
		},
		{
			name:      "in",
			where:     sqliteTestNot(t, sqliteTestExpression(t, inCondition)),
			wantWhere: `WHERE NOT ("summary" IN (?, ?) AND "summary" IS NOT NULL)`,
			wantArgs:  []any{"ORM", "Go"},
		},
		{
			name: "isnull",
			where: sqliteTestNot(t, sqliteTestExpression(t,
				query.NewCondition(summary, query.LookupIsNull, query.Boolean(true)),
			)),
			wantWhere: `WHERE NOT ("summary" IS NULL)`,
			wantArgs:  []any{},
		},
		{
			name:      "double not",
			where:     sqliteTestNot(t, sqliteTestNot(t, exact)),
			wantWhere: `WHERE NOT (NOT ("summary" = ?))`,
			wantArgs:  []any{"ORM"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, withWhereErr := query.NewPlan("news_article", []query.FieldRef{id, summary}).WithWhere(test.where)
			if withWhereErr != nil {
				t.Fatalf("WithWhere() error = %v", withWhereErr)
			}
			statement, arguments, compileErr := sqlite.Compile(plan)
			if compileErr != nil {
				t.Fatalf("Compile() error = %v", compileErr)
			}
			if !strings.Contains(statement, test.wantWhere) {
				t.Fatalf("SQL = %q, want predicate %q", statement, test.wantWhere)
			}
			if !reflect.DeepEqual(arguments, test.wantArgs) {
				t.Fatalf("arguments = %#v, want %#v", arguments, test.wantArgs)
			}
		})
	}
}

func TestSQLiteBooleanCompilerEnforcesBackendTreeBoundsAndUncheckedLeaves(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	condition := query.NewCondition(id, query.LookupExact, query.Integer(1))
	withinLimit := make([]query.Condition, 1023)
	for index := range withinLimit {
		withinLimit[index] = condition
	}
	if _, _, err := sqlite.Compile(query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(withinLimit...)); err != nil {
		t.Fatalf("Compile(1024-node unchecked tree) error = %v", err)
	}

	overLimit := append(append([]query.Condition(nil), withinLimit...), condition)
	for _, test := range []struct {
		name     string
		plan     query.Plan
		category string
		code     string
	}{
		{name: "node cap", plan: query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(overLimit...), category: query.CategoryQuery, code: query.CodeInvalidPlan},
		{name: "zero leaf", plan: query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(query.Condition{}), category: query.CategoryQuery, code: query.CodeInvalidPlan},
		{name: "foreign field", plan: query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(
			query.NewCondition(query.NewFieldRef("id", "other_id", query.FieldInteger, false), query.LookupExact, query.Integer(1)),
		), category: query.CategoryQuery, code: query.CodeInvalidPlan},
		{name: "unknown lookup", plan: query.NewPlan("news_article", []query.FieldRef{id}).WithConditions(
			query.NewCondition(id, query.Lookup("unknown"), query.Integer(1)),
		), category: query.CategoryBackend, code: query.CodeUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			statement, arguments, err := sqlite.Compile(test.plan)
			if !errors.Is(err, &query.Error{Category: test.category, Code: test.code}) {
				t.Fatalf("Compile() error = %v, want %s/%s", err, test.category, test.code)
			}
			if statement != "" || arguments != nil {
				t.Fatalf("Compile() = (%q, %#v), want empty output on error", statement, arguments)
			}
		})
	}
}

func TestSQLiteBooleanCompilerKeepsRelationJoinsRootConjunctive(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorID := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	related := sqliteTestExpression(t, query.NewRelatedCondition(
		requiredAuthorPath(t, query.NewFieldRef("name", "name", query.FieldString, false)),
		query.LookupExact,
		query.String("Ada"),
	))
	titleChoice := sqliteTestOr(t,
		sqliteTestExpression(t, query.NewCondition(title, query.LookupExact, query.String("Alpha"))),
		sqliteTestExpression(t, query.NewCondition(title, query.LookupExact, query.String("Beta"))),
	)
	where := sqliteTestAnd(t, related, titleChoice)
	plan, err := query.NewPlan("blog_post", []query.FieldRef{id, title, authorID}).WithWhere(where)
	if err != nil {
		t.Fatalf("WithWhere() error = %v", err)
	}

	statement, arguments, err := sqlite.Compile(plan)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	wantFragment := `INNER JOIN "authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE ("t1"."name" = ? AND ("t0"."title" = ? OR "t0"."title" = ?))`
	if !strings.Contains(statement, wantFragment) {
		t.Fatalf("SQL = %q, want root-conjunctive relation fragment %q", statement, wantFragment)
	}
	if want := []any{"Ada", "Alpha", "Beta"}; !reflect.DeepEqual(arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
}

func sqliteTestExpression(t *testing.T, condition query.Condition) query.Expression {
	t.Helper()
	expression, err := query.NewExpression(condition)
	if err != nil {
		t.Fatalf("NewExpression() error = %v", err)
	}
	return expression
}

func sqliteTestAnd(t *testing.T, left, right query.Expression, rest ...query.Expression) query.Expression {
	t.Helper()
	expression, err := query.AndExpressions(left, right, rest...)
	if err != nil {
		t.Fatalf("AndExpressions() error = %v", err)
	}
	return expression
}

func sqliteTestOr(t *testing.T, left, right query.Expression, rest ...query.Expression) query.Expression {
	t.Helper()
	expression, err := query.OrExpressions(left, right, rest...)
	if err != nil {
		t.Fatalf("OrExpressions() error = %v", err)
	}
	return expression
}

func sqliteTestNot(t *testing.T, expression query.Expression) query.Expression {
	t.Helper()
	negated, err := query.NotExpression(expression)
	if err != nil {
		t.Fatalf("NotExpression() error = %v", err)
	}
	return negated
}
