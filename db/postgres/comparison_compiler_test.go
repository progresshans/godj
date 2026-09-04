package postgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCompileOrderedLiteralAndFieldComparisonsPreservesPlaceholderOrder(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	titleEqualsSummary, err := query.NewFieldCondition(title, query.LookupExact, summary)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("news_article", []query.FieldRef{id, title, summary}).WithConditions(
		query.NewCondition(id, query.LookupGreaterThan, query.Integer(1)),
		titleEqualsSummary,
		query.NewCondition(title, query.LookupGreaterThanOrEqual, query.String("A")),
		query.NewCondition(id, query.LookupLessThanOrEqual, query.Integer(4)),
		query.NewCondition(title, query.LookupLessThan, query.String("Z")),
	)
	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id", "title", "summary" FROM "godj_app"."news_article" WHERE (("id" > $1) AND ("title" = "summary") AND ("title" >= $2) AND ("id" <= $3) AND ("title" < $4))`
	wantArguments := []any{int64(1), "A", int64(4), "Z"}
	if statement != want || !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("comparison = %q %#v, want %q %#v", statement, arguments, want, wantArguments)
	}
}

func TestCompileFieldComparisonNullableNegationUsesBothOperandsAndParity(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	other := query.NewFieldRef("other", "other", query.FieldString, true)

	conditions := []struct {
		name       string
		left       query.FieldRef
		lookup     query.Lookup
		right      query.FieldRef
		negations  int
		wantSuffix string
	}{
		{name: "nullable RHS odd", left: title, lookup: query.LookupExact, right: summary, negations: 1, wantSuffix: ` WHERE (NOT ("title" = "summary" AND "summary" IS NOT NULL))`},
		{name: "nullable LHS odd", left: summary, lookup: query.LookupGreaterThan, right: title, negations: 1, wantSuffix: ` WHERE (NOT ("summary" > "title" AND "summary" IS NOT NULL))`},
		{name: "two nullable operands odd", left: summary, lookup: query.LookupLessThanOrEqual, right: other, negations: 1, wantSuffix: ` WHERE (NOT ("summary" <= "other" AND "summary" IS NOT NULL AND "other" IS NOT NULL))`},
		{name: "same nullable field deduplicated", left: summary, lookup: query.LookupGreaterThanOrEqual, right: summary, negations: 1, wantSuffix: ` WHERE (NOT ("summary" >= "summary" AND "summary" IS NOT NULL))`},
		{name: "nullable RHS even", left: title, lookup: query.LookupExact, right: summary, negations: 2, wantSuffix: ` WHERE (NOT (NOT ("title" = "summary")))`},
		{name: "nullable RHS triple", left: title, lookup: query.LookupLessThan, right: summary, negations: 3, wantSuffix: ` WHERE (NOT (NOT (NOT ("title" < "summary" AND "summary" IS NOT NULL))))`},
	}
	for _, test := range conditions {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			condition, err := query.NewFieldCondition(test.left, test.lookup, test.right)
			if err != nil {
				t.Fatal(err)
			}
			expression := mustPostgresExpression(t, condition)
			for index := 0; index < test.negations; index++ {
				expression, err = query.NotExpression(expression)
				if err != nil {
					t.Fatal(err)
				}
			}
			plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, summary, other}).WithWhere(expression)
			if err != nil {
				t.Fatal(err)
			}
			statement, arguments, err := compilePlan("godj_app", plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(statement, test.wantSuffix) || len(arguments) != 0 {
				t.Fatalf("field NOT = %q %#v, want suffix %q and zero arguments", statement, arguments, test.wantSuffix)
			}
		})
	}
}

func TestCompileFieldComparisonIsSharedByResultShapes(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	condition, err := query.NewFieldCondition(title, query.LookupExact, summary)
	if err != nil {
		t.Fatal(err)
	}
	base, err := query.NewPlan("news_article", []query.FieldRef{id, title, summary}).
		WithWhere(mustPostgresExpression(t, condition))
	if err != nil {
		t.Fatal(err)
	}
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
	direct, err := base.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := direct.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		plan     query.Plan
		wantArgs []any
	}{
		{name: "model", plan: base, wantArgs: []any{}},
		{name: "projection", plan: projected, wantArgs: []any{}},
		{name: "direct aggregate", plan: direct, wantArgs: []any{}},
		{name: "derived aggregate", plan: derived, wantArgs: []any{int64(2)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, arguments, err := compilePlan("godj_app", test.plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(statement, ` WHERE ("title" = "summary")`) || !reflect.DeepEqual(arguments, test.wantArgs) {
				t.Fatalf("result shape = %q %#v, want shared field predicate and %#v", statement, arguments, test.wantArgs)
			}
		})
	}
}

func TestCompileFieldComparisonRejectsRHSOutsideSourceInventory(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	condition, err := query.NewFieldCondition(title, query.LookupExact, summary)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = compilePlan("godj_app", query.NewPlan("news_article", []query.FieldRef{id, title}).WithConditions(condition))
	if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) ||
		!strings.Contains(err.Error(), "right-hand-side field") {
		t.Fatalf("compilePlan() error = %v, want RHS source invalid_plan", err)
	}
}

func TestCompileRelationAndRootFieldComparisonUseRootAlias(t *testing.T) {
	t.Parallel()

	post := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(
		post, "blog_post", "author", "author_id", author, "authors_author", "id", false, authorName,
	)
	if err != nil {
		t.Fatal(err)
	}
	fieldCondition, err := query.NewFieldCondition(title, query.LookupExact, summary)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("blog_post", []query.FieldRef{id, title, summary, authorKey}).WithConditions(
		fieldCondition,
		query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")),
	)
	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, `("t0"."title" = "t0"."summary")`) ||
		!strings.Contains(statement, `("t1"."name" = $1)`) || !reflect.DeepEqual(arguments, []any{"Ada"}) {
		t.Fatalf("relation/root field comparison = %q %#v", statement, arguments)
	}
}
