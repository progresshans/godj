package query_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestResultShapeProjectionIsImmutableDetachedAndOrdered(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	fields := []query.FieldRef{title, id}

	shape, err := query.NewProjectionResult(fields...)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	fields[0] = published
	if shape.Kind() != query.ResultProjection {
		t.Fatalf("projection kind = %q, want %q", shape.Kind(), query.ResultProjection)
	}
	assertResultExpressionFields(t, shape.Expressions(), []query.FieldRef{title, id})

	returned := shape.Expressions()
	returned[0] = query.FieldResult(published)
	assertResultExpressionFields(t, shape.Expressions(), []query.FieldRef{title, id})

	equal, err := query.NewProjectionResult(title, id)
	if err != nil {
		t.Fatalf("equal NewProjectionResult() error = %v", err)
	}
	reordered, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatalf("reordered NewProjectionResult() error = %v", err)
	}
	if !shape.Equal(equal) || shape.Equal(reordered) || equal.Equal(reordered) {
		t.Fatal("projection equality did not preserve ordered expressions")
	}

	aggregate, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}
	if shape.Equal(aggregate) || shape.Equal(query.ResultShape{}) {
		t.Fatal("projection shape equals a different or zero result kind")
	}
}

func TestPlanResultShapeDerivationRetainsDetachedSourceMetadata(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	sources := []query.FieldRef{id, title, published}
	base := query.NewPlan("news_article", sources).
		WithConditions(query.NewCondition(published, query.LookupExact, query.Boolean(true))).
		WithOrderings(query.NewOrdering(id, query.Descending))
	sources[0] = query.NewFieldRef("mutated", "mutated", query.FieldString, false)

	if base.ResultShape().Kind() != query.ResultModel || len(base.ResultShape().Expressions()) != 0 {
		t.Fatalf("default result = %#v, want model with implicit source fields", base.ResultShape())
	}
	if got := base.SourceFields(); len(got) != 3 || !got[0].Equal(id) || !got[1].Equal(title) || !got[2].Equal(published) {
		t.Fatalf("base SourceFields() = %#v", got)
	}
	returnedSources := base.SourceFields()
	returnedSources[0] = query.NewFieldRef("changed", "changed", query.FieldBoolean, false)
	if !base.SourceFields()[0].Equal(id) {
		t.Fatal("SourceFields() exposed plan storage")
	}

	projection, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	projected, err := base.WithResultShape(projection)
	if err != nil {
		t.Fatalf("WithResultShape() error = %v", err)
	}
	if base.ResultShape().Kind() != query.ResultModel {
		t.Fatal("WithResultShape() mutated its source plan")
	}
	if got := projected.SourceFields(); len(got) != 3 || !got[0].Equal(id) || !got[1].Equal(title) || !got[2].Equal(published) {
		t.Fatalf("projected SourceFields() = %#v", got)
	}
	if len(projected.Conditions()) != 1 || len(projected.Orderings()) != 1 {
		t.Fatalf("projection derivation changed predicates/orderings: %#v", projected)
	}
	assertResultExpressionFields(t, projected.ResultShape().Expressions(), []query.FieldRef{id, title})

	returnedResult := projected.ResultShape()
	returnedExpressions := returnedResult.Expressions()
	returnedExpressions[0] = query.FieldResult(published)
	assertResultExpressionFields(t, projected.ResultShape().Expressions(), []query.FieldRef{id, title})

	identical, err := base.WithResultShape(projection)
	if err != nil {
		t.Fatalf("identical WithResultShape() error = %v", err)
	}
	if !projected.Equal(identical) || projected.Equal(base) {
		t.Fatal("plan equality omitted its result shape")
	}
}

func TestProjectionResultRejectsInvalidDuplicateAndForeignFields(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	tests := []struct {
		name   string
		fields []query.FieldRef
	}{
		{name: "empty"},
		{name: "zero field", fields: []query.FieldRef{{}}},
		{name: "missing name", fields: []query.FieldRef{query.NewFieldRef("", "title", query.FieldString, false)}},
		{name: "missing column", fields: []query.FieldRef{query.NewFieldRef("title", "", query.FieldString, false)}},
		{name: "unsupported kind", fields: []query.FieldRef{query.NewFieldRef("score", "score", query.FieldKind("float"), false)}},
		{name: "duplicate field", fields: []query.FieldRef{id, id}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			shape, err := query.NewProjectionResult(test.fields...)
			assertInvalidResultPlan(t, err)
			if !shape.Equal(query.ResultShape{}) {
				t.Fatalf("failed projection = %#v, want zero", shape)
			}
		})
	}

	foreign := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	shape, err := query.NewProjectionResult(foreign)
	if err != nil {
		t.Fatalf("foreign shape construction error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id}).WithResultShape(shape)
	assertInvalidResultPlan(t, err)
	if !plan.Equal(query.Plan{}) {
		t.Fatalf("WithResultShape(foreign) = %#v, want zero plan", plan)
	}

	zeroPlan, err := query.NewPlan("news_article", []query.FieldRef{id}).WithResultShape(query.ResultShape{})
	assertInvalidResultPlan(t, err)
	if !zeroPlan.Equal(query.Plan{}) {
		t.Fatalf("WithResultShape(zero) = %#v, want zero plan", zeroPlan)
	}
}

func TestAggregateResultLimitsTypesAccessorsAndEquality(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	count := query.CountAllResult()
	maxID := query.MaxResult(id)
	maxTitle := query.MaxResult(title)

	if count.Kind() != query.ResultCountAll {
		t.Fatalf("CountAllResult kind = %q", count.Kind())
	}
	if field, ok := count.Field(); ok || !field.Equal(query.FieldRef{}) {
		t.Fatalf("CountAllResult Field() = (%#v, %v), want zero/false", field, ok)
	}
	if field, ok := maxID.Field(); !ok || !field.Equal(id) || maxID.Kind() != query.ResultMax {
		t.Fatalf("MaxResult Field()/Kind() = (%#v, %v, %q)", field, ok, maxID.Kind())
	}
	if field, ok := query.FieldResult(title).Field(); !ok || !field.Equal(title) {
		t.Fatalf("FieldResult Field() = (%#v, %v)", field, ok)
	}

	for countExpressions := 1; countExpressions <= 4; countExpressions++ {
		expressions := make([]query.ResultExpression, countExpressions)
		for index := range expressions {
			expressions[index] = count
		}
		shape, err := query.NewAggregateResult(expressions...)
		if err != nil || shape.Kind() != query.ResultAggregate || len(shape.Expressions()) != countExpressions {
			t.Fatalf("NewAggregateResult(%d) = (%#v, %v)", countExpressions, shape, err)
		}
	}

	shape, err := query.NewAggregateResult(count, maxID, maxTitle)
	if err != nil {
		t.Fatalf("mixed NewAggregateResult() error = %v", err)
	}
	equal, err := query.NewAggregateResult(count, maxID, maxTitle)
	if err != nil {
		t.Fatalf("equal NewAggregateResult() error = %v", err)
	}
	reordered, err := query.NewAggregateResult(maxID, count, maxTitle)
	if err != nil {
		t.Fatalf("reordered NewAggregateResult() error = %v", err)
	}
	if !shape.Equal(equal) || shape.Equal(reordered) {
		t.Fatal("aggregate equality omitted ordered expressions")
	}
	returned := shape.Expressions()
	returned[0] = maxTitle
	if !shape.Expressions()[0].Equal(count) {
		t.Fatal("aggregate Expressions() exposed shape storage")
	}

	invalid := []struct {
		name        string
		expressions []query.ResultExpression
	}{
		{name: "empty"},
		{name: "five expressions", expressions: []query.ResultExpression{count, count, count, count, count}},
		{name: "zero expression", expressions: []query.ResultExpression{{}}},
		{name: "field projection expression", expressions: []query.ResultExpression{query.FieldResult(id)}},
		{name: "MAX zero field", expressions: []query.ResultExpression{query.MaxResult(query.FieldRef{})}},
		{name: "MAX boolean", expressions: []query.ResultExpression{query.MaxResult(published)}},
		{name: "MAX unsupported", expressions: []query.ResultExpression{query.MaxResult(query.NewFieldRef("score", "score", query.FieldKind("float"), false))}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, resultErr := query.NewAggregateResult(test.expressions...)
			assertInvalidResultPlan(t, resultErr)
			if !result.Equal(query.ResultShape{}) {
				t.Fatalf("failed aggregate = %#v, want zero", result)
			}
		})
	}

	foreignMax, err := query.NewAggregateResult(query.MaxResult(query.NewFieldRef("author", "author_id", query.FieldInteger, false)))
	if err != nil {
		t.Fatalf("foreign aggregate construction error = %v", err)
	}
	plan, err := query.NewPlan("news_article", []query.FieldRef{id, title}).WithResultShape(foreignMax)
	assertInvalidResultPlan(t, err)
	if !plan.Equal(query.Plan{}) {
		t.Fatalf("WithResultShape(foreign MAX) = %#v, want zero plan", plan)
	}
}

func TestPlanDistinctAndOffsetAreImmutableBoundedAndComparable(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	base := query.NewPlan("news_article", []query.FieldRef{id})
	distinct := base.WithDistinct()
	if base.Distinct() || !distinct.Distinct() {
		t.Fatalf("Distinct() source/derived = (%v, %v)", base.Distinct(), distinct.Distinct())
	}
	if !distinct.Equal(distinct.WithDistinct()) || distinct.Equal(base) {
		t.Fatal("distinct derivation is not idempotent or equality omits it")
	}

	offset, err := distinct.WithOffset(0)
	if err != nil {
		t.Fatalf("WithOffset(0) error = %v", err)
	}
	if _, ok := distinct.Offset(); ok {
		t.Fatal("WithOffset() mutated its source plan")
	}
	if got, ok := offset.Offset(); !ok || got != 0 {
		t.Fatalf("Offset() = (%d, %v), want (0, true)", got, ok)
	}
	if offset.Equal(distinct) {
		t.Fatal("plan equality omitted offset presence")
	}

	maximum, err := base.WithOffset(math.MaxInt32)
	if err != nil {
		t.Fatalf("WithOffset(MaxInt32) error = %v", err)
	}
	if got, ok := maximum.Offset(); !ok || int64(got) != math.MaxInt32 {
		t.Fatalf("maximum Offset() = (%d, %v)", got, ok)
	}

	for _, invalid := range []int{-1, -2} {
		plan, invalidErr := base.WithOffset(invalid)
		assertInvalidOffset(t, invalidErr)
		if !plan.Equal(query.Plan{}) {
			t.Fatalf("WithOffset(%d) = %#v, want zero plan", invalid, plan)
		}
	}
	if strconv.IntSize > 32 {
		tooLarge64 := int64(math.MaxInt32) + 1
		plan, invalidErr := base.WithOffset(int(tooLarge64))
		assertInvalidOffset(t, invalidErr)
		if !plan.Equal(query.Plan{}) {
			t.Fatalf("WithOffset(MaxInt32+1) = %#v, want zero plan", plan)
		}
	}
}

func TestResultShapeAndRelationProjectionAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	author := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	base := query.NewPlan("blog_post", []query.FieldRef{id, title, author})
	relation := resultShapeRelationProjection(t, author)

	projection, err := query.NewProjectionResult(id, title)
	if err != nil {
		t.Fatalf("NewProjectionResult() error = %v", err)
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult(), query.MaxResult(id))
	if err != nil {
		t.Fatalf("NewAggregateResult() error = %v", err)
	}

	for _, result := range []query.ResultShape{projection, aggregate} {
		shaped, shapeErr := base.WithResultShape(result)
		if shapeErr != nil {
			t.Fatalf("WithResultShape(%q) error = %v", result.Kind(), shapeErr)
		}
		selected, selectedErr := shaped.WithRelationProjection(relation)
		assertInvalidResultPlan(t, selectedErr)
		if !selected.Equal(query.Plan{}) {
			t.Fatalf("WithRelationProjection(%q result) = %#v, want zero", result.Kind(), selected)
		}
	}

	selected, err := base.WithRelationProjection(relation)
	if err != nil {
		t.Fatalf("WithRelationProjection() error = %v", err)
	}
	for _, result := range []query.ResultShape{projection, aggregate} {
		shaped, shapeErr := selected.WithResultShape(result)
		assertInvalidResultPlan(t, shapeErr)
		if !shaped.Equal(query.Plan{}) {
			t.Fatalf("selected.WithResultShape(%q) = %#v, want zero", result.Kind(), shaped)
		}
	}
	if got := selected.SourceFields(); len(got) != 3 || !got[0].Equal(id) || !got[1].Equal(title) || !got[2].Equal(author) {
		t.Fatalf("relation-selected SourceFields() = %#v", got)
	}
	if _, ok := base.RelationProjection(); ok {
		t.Fatal("WithRelationProjection() mutated its source")
	}
}

func resultShapeRelationProjection(t *testing.T, sourceKey query.FieldRef) query.RelationProjection {
	t.Helper()
	targetID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	projection, err := query.NewForwardRelationProjection(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		sourceKey,
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		targetID,
		[]query.FieldRef{targetID, query.NewFieldRef("name", "name", query.FieldString, false)},
	)
	if err != nil {
		t.Fatalf("NewForwardRelationProjection() error = %v", err)
	}
	return projection
}

func assertResultExpressionFields(t *testing.T, expressions []query.ResultExpression, want []query.FieldRef) {
	t.Helper()
	if len(expressions) != len(want) {
		t.Fatalf("len(expressions) = %d, want %d", len(expressions), len(want))
	}
	for index, expression := range expressions {
		field, ok := expression.Field()
		if expression.Kind() != query.ResultField || !ok || !field.Equal(want[index]) {
			t.Fatalf("expression[%d] = (%q, %#v, %v), want field %#v", index, expression.Kind(), field, ok, want[index])
		}
	}
}

func assertInvalidResultPlan(t *testing.T, err error) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != query.CategoryQuery || queryError.Code != query.CodeInvalidPlan {
		t.Fatalf("error = %T %v, want query_error/invalid_plan", err, err)
	}
}

func assertInvalidOffset(t *testing.T, err error) {
	t.Helper()
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Category != query.CategoryQuery || queryError.Code != query.CodeInvalidOffset {
		t.Fatalf("error = %T %v, want query_error/invalid_offset", err, err)
	}
}
