package postgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestCompileScalarProjectionUsesResultOrderAndSourceValidation(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	shape, err := query.NewProjectionResult(title, id)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, published, summary}).
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(summary, query.Descending)).
		WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "title", "id" FROM "godj_app"."news_article" WHERE "published" = $1 ORDER BY "summary" DESC`
	if statement != want || !reflect.DeepEqual(arguments, []any{true}) {
		t.Fatalf("projection = %q %#v, want %q %#v", statement, arguments, want, []any{true})
	}
}

func TestCompileDistinctProjectionAndPaginationPreservePlaceholderOrder(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	shape, err := query.NewProjectionResult(title, id)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(
			query.NewCondition(title, query.LookupIContains, query.String(`50%_Go\SQL`)),
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
		).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	plan = plan.WithDistinct()
	plan, err = plan.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithOffset(3)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT DISTINCT "title", "id" FROM "godj_app"."news_article" WHERE "title" ILIKE $1 ESCAPE '\' AND "published" = $2 ORDER BY "id" DESC LIMIT $3 OFFSET $4`
	wantArguments := []any{`%50\%\_Go\\SQL%`, true, int64(2), int64(3)}
	if statement != want || !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("distinct projection = %q %#v, want %q %#v", statement, arguments, want, wantArguments)
	}
}

func TestCompileOffsetOnlyAndDistinctModel(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	base := query.NewPlan("news_article", []query.FieldRef{id, title}).
		WithConditions(query.NewCondition(title, query.LookupExact, query.String("hello")))
	offset, err := base.WithOffset(9)
	if err != nil {
		t.Fatal(err)
	}
	statement, arguments, err := compilePlan("godj_app", offset)
	if err != nil {
		t.Fatal(err)
	}
	wantOffset := `SELECT "id", "title" FROM "godj_app"."news_article" WHERE "title" = $1 OFFSET $2`
	if statement != wantOffset || !reflect.DeepEqual(arguments, []any{"hello", int64(9)}) {
		t.Fatalf("offset-only = %q %#v", statement, arguments)
	}

	statement, arguments, err = compilePlan("godj_app", base.WithDistinct())
	if err != nil {
		t.Fatal(err)
	}
	wantDistinct := `SELECT DISTINCT "id", "title" FROM "godj_app"."news_article" WHERE "title" = $1`
	if statement != wantDistinct || !reflect.DeepEqual(arguments, []any{"hello"}) {
		t.Fatalf("distinct model = %q %#v", statement, arguments)
	}
}

func TestCompileAggregateWrapsTheSlicedLogicalSource(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	shape, err := query.NewAggregateResult(
		query.MaxResult(title),
		query.CountAllResult(),
		query.MaxResult(id),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title, published}).
		WithConditions(
			query.NewCondition(title, query.LookupIContains, query.String(`50%_Go\SQL`)),
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
		).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	plan = plan.WithDistinct()
	plan, err = plan.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithOffset(3)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT MAX("godj_source"."title"), COUNT(*), MAX("godj_source"."id") FROM (SELECT DISTINCT "id", "title", "published" FROM "godj_app"."news_article" WHERE "title" ILIKE $1 ESCAPE '\' AND "published" = $2 ORDER BY "id" DESC LIMIT $3 OFFSET $4) AS "godj_source"`
	wantArguments := []any{`%50\%\_Go\\SQL%`, true, int64(2), int64(3)}
	if statement != want || !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("aggregate = %q %#v, want %q %#v", statement, arguments, want, wantArguments)
	}
}

func TestCompileSimpleAggregateUsesDirectSourceAndDropsOrdering(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	shape, err := query.NewAggregateResult(
		query.CountAllResult(),
		query.MaxResult(id),
		query.MaxResult(summary),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, summary, published}).
		WithConditions(
			query.NewCondition(published, query.LookupExact, query.Boolean(true)),
			query.NewCondition(summary, query.LookupIContains, query.String("Go")),
		).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT COUNT(*), MAX("id"), MAX("summary") FROM "godj_app"."news_article" WHERE "published" = $1 AND "summary" ILIKE $2 ESCAPE '\'`
	wantArguments := []any{true, "%Go%"}
	if statement != want || !reflect.DeepEqual(arguments, wantArguments) {
		t.Fatalf("direct aggregate = %q %#v, want %q %#v", statement, arguments, want, wantArguments)
	}
}

func TestCompileDirectAggregateValidatesOmittedOrderings(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	outsideSource := query.NewFieldRef("created", "created_at", query.FieldString, false)
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
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
				WithResultShape(shape)
			if planErr != nil {
				t.Fatal(planErr)
			}
			_, _, compileErr := compilePlan("godj_app", plan)
			if !errors.Is(compileErr, &query.Error{
				Category: query.CategoryQuery,
				Code:     query.CodeInvalidPlan,
			}) {
				t.Fatalf("compilePlan() error = %v, want query invalid_plan", compileErr)
			}
			if !strings.Contains(compileErr.Error(), test.detail) {
				t.Fatalf("compilePlan() error = %v, want detail %q", compileErr, test.detail)
			}
		})
	}
}

func TestCompileAggregateUsesDerivedSourceForDistinctOrExplicitSlice(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	shape, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatal(err)
	}
	base, err := query.NewPlan("news_article", []query.FieldRef{id, title}).
		WithOrderings(query.NewOrdering(id, query.Descending)).
		WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := base.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := base.WithOffset(0)
	if err != nil {
		t.Fatal(err)
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
			wantSQL: `SELECT COUNT(*), MAX("godj_source"."id") FROM (` +
				`SELECT DISTINCT "id", "title" FROM "godj_app"."news_article" ORDER BY "id" DESC` +
				`) AS "godj_source"`,
			wantArguments: []any{},
		},
		{
			name:          "zero limit is still an explicit slice",
			plan:          limited,
			wantSQL:       `SELECT COUNT(*), MAX("godj_source"."id") FROM (SELECT "id", "title" FROM "godj_app"."news_article" ORDER BY "id" DESC LIMIT $1) AS "godj_source"`,
			wantArguments: []any{int64(0)},
		},
		{
			name:          "zero offset is still an explicit slice",
			plan:          offset,
			wantSQL:       `SELECT COUNT(*), MAX("godj_source"."id") FROM (SELECT "id", "title" FROM "godj_app"."news_article" ORDER BY "id" DESC OFFSET $1) AS "godj_source"`,
			wantArguments: []any{int64(0)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			statement, arguments, compileErr := compilePlan("godj_app", test.plan)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			if statement != test.wantSQL || !reflect.DeepEqual(arguments, test.wantArguments) {
				t.Fatalf("aggregate = %q %#v, want %q %#v", statement, arguments, test.wantSQL, test.wantArguments)
			}
		})
	}
}

func TestCompileCountAndNullableMaxOverEmptySlice(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, true)
	shape, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(title))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = plan.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}

	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT COUNT(*), MAX("godj_source"."title") FROM (SELECT "id", "title" FROM "godj_app"."news_article" LIMIT $1) AS "godj_source"`
	if statement != want || !reflect.DeepEqual(arguments, []any{int64(0)}) {
		t.Fatalf("empty aggregate = %q %#v, want %q [0]", statement, arguments, want)
	}
}

func TestCompilerRejectsDistinctProjectionOrderingOutsideResult(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	shape, err := query.NewProjectionResult(title)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title}).
		WithOrderings(query.NewOrdering(id, query.Ascending)).
		WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = compilePlan("godj_app", plan.WithDistinct())
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Field: "id"}) {
		t.Fatalf("error = %v, want backend unsupported_feature", err)
	}
}

func TestCompilerRejectsNonModelRelationResults(t *testing.T) {
	t.Parallel()

	post := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(
		post, "blog_post", "author", "author_id",
		author, "authors_author", "id", false, authorName,
	)
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("blog_post", []query.FieldRef{id, title, authorKey}).
		WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")))
	projection, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []query.ResultShape{projection, aggregate} {
		plan, shapeErr := base.WithResultShape(shape)
		if shapeErr != nil {
			t.Fatal(shapeErr)
		}
		_, _, compileErr := compilePlan("godj_app", plan)
		if !errors.Is(compileErr, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
			t.Fatalf("result %q error = %v, want backend unsupported_feature", shape.Kind(), compileErr)
		}
	}
}

func TestCompileRelationModelSupportsDistinctAndOffset(t *testing.T) {
	t.Parallel()

	post := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	author := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(
		post, "blog_post", "author", "author_id",
		author, "authors_author", "id", false, authorName,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("blog_post", []query.FieldRef{id, authorKey}).
		WithConditions(query.NewRelatedCondition(path, query.LookupExact, query.String("Ada"))).
		WithDistinct()
	plan, err = plan.WithOffset(4)
	if err != nil {
		t.Fatal(err)
	}
	statement, arguments, err := compilePlan("godj_app", plan)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT DISTINCT "t0"."id", "t0"."author_id" FROM "godj_app"."blog_post" AS "t0" INNER JOIN "godj_app"."authors_author" AS "t1" ON "t0"."author_id" = "t1"."id" WHERE "t1"."name" = $1 OFFSET $2`
	if statement != want || !reflect.DeepEqual(arguments, []any{"Ada", int64(4)}) {
		t.Fatalf("relation distinct offset = %q %#v, want %q", statement, arguments, want)
	}
}

func TestCompilerRejectsInvalidReadFieldMetadata(t *testing.T) {
	t.Parallel()

	tests := []query.Plan{
		query.NewPlan("news_article", []query.FieldRef{
			query.NewFieldRef("DisplayName", "title", query.FieldString, false),
		}),
		query.NewPlan("news_article", []query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.NewFieldRef("other", "id", query.FieldInteger, false),
		}),
		query.NewPlan("news_article", []query.FieldRef{
			query.NewFieldRef("value", "value", query.FieldKind("decimal"), false),
		}),
	}
	for _, plan := range tests {
		_, _, err := compilePlan("godj_app", plan)
		if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
			t.Fatalf("plan %#v error = %v, want query invalid_plan", plan, err)
		}
	}
}
