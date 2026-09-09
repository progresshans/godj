package query_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestPlanDerivationDoesNotMutateSource(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	base := query.NewPlan("news_article", []query.FieldRef{title})
	filtered := querytest.Conditions(t, base, query.NewCondition(title, query.LookupExact, query.String("Django")))
	ordered := filtered.WithOrderings(query.NewOrdering(title, query.Descending))

	if len(base.Conditions()) != 0 || len(base.Orderings()) != 0 {
		t.Fatalf("base plan mutated: conditions=%v orderings=%v", base.Conditions(), base.Orderings())
	}
	if len(filtered.Conditions()) != 1 || len(filtered.Orderings()) != 0 {
		t.Fatalf("filtered plan changed unexpectedly: conditions=%v orderings=%v", filtered.Conditions(), filtered.Orderings())
	}
	if len(ordered.Conditions()) != 1 || len(ordered.Orderings()) != 1 {
		t.Fatalf("ordered plan = conditions=%v orderings=%v", ordered.Conditions(), ordered.Orderings())
	}

	copyOfConditions := filtered.Conditions()
	copyOfConditions[0] = query.NewCondition(title, query.LookupIContains, query.String("mutated"))
	if filtered.Conditions()[0].Lookup() != query.LookupExact {
		t.Fatal("Conditions returned mutable plan storage")
	}
}

func TestPlanDerivationsPreserveCompositeOwnership(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	for _, kind := range []string{"model", "projection", "relation"} {
		t.Run(kind, func(t *testing.T) {
			fields := []query.FieldRef{id, title}
			orderings := []query.Ordering{query.NewOrdering(id, query.Ascending)}
			base := query.NewPlan("blog_post", fields).WithOrderings(orderings...)
			var err error
			switch kind {
			case "projection":
				shape, shapeErr := query.NewProjectionResult(id, title)
				if shapeErr != nil {
					t.Fatal(shapeErr)
				}
				base, err = base.WithResultShape(shape)
			case "relation":
				base, err = base.WithRelationProjection(newTestRelationProjection(t, false))
			}
			if err != nil {
				t.Fatal(err)
			}
			limited, err := base.WithLimit(7)
			if err != nil {
				t.Fatal(err)
			}
			offset, err := limited.WithOffset(3)
			if err != nil {
				t.Fatal(err)
			}
			derived := offset.WithDistinct()
			filtered, err := derived.WithWhere(expressionLeaf(t, query.NewCondition(id, query.LookupExact, query.Integer(7))))
			if err != nil {
				t.Fatal(err)
			}
			freshOrderings := []query.Ordering{query.NewOrdering(title, query.Descending)}
			reordered := filtered.WithOrderings(freshOrderings...)
			freshOrderings[0] = query.NewOrdering(id, query.Descending)
			if !reordered.Orderings()[0].Equal(query.NewOrdering(title, query.Descending)) {
				t.Fatal("ordering derivation retained the caller's input slice")
			}
			reshaped, err := filtered.WithResultShape(base.ResultShape())
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := derived.Where(); exists {
				t.Fatal("filter changed the original plan predicate")
			}
			if _, set := base.Limit(); set {
				t.Fatal("WithLimit mutated its source")
			}
			if _, set := limited.Offset(); set || offset.Distinct() {
				t.Fatal("Offset or Distinct mutated its source")
			}
			if limit, set := derived.Limit(); !set || limit != 7 {
				t.Fatalf("derived limit = %d, %v", limit, set)
			}
			if value, set := derived.Offset(); !set || value != 3 || !derived.Distinct() {
				t.Fatalf("derived offset/distinct = %d, %v, %v", value, set, derived.Distinct())
			}
			fields[0] = title
			orderings[0] = query.NewOrdering(title, query.Descending)
			derived.SourceFields()[0] = title
			derived.Orderings()[0] = orderings[0]
			if kind == "projection" {
				derived.ResultShape().Expressions()[0] = query.CountAllResult()
			}
			if projection, ok := derived.RelationProjection(); ok {
				projection.TargetColumns()[0] = title
			}
			for _, plan := range []query.Plan{base, limited, offset, derived, filtered, reshaped} {
				if !plan.SourceFields()[0].Equal(id) || !plan.Orderings()[0].Equal(query.NewOrdering(id, query.Ascending)) {
					t.Fatal("caller slice mutation reached shared plan metadata")
				}
				if !plan.ResultShape().Equal(base.ResultShape()) {
					t.Fatal("scalar derivation changed result shape")
				}
				if kind == "projection" && plan.ResultShape().Expressions()[0].Kind() != query.ResultField {
					t.Fatal("result accessor exposed shared projection storage")
				}
				if kind == "relation" {
					projection, ok := plan.RelationProjection()
					if !ok || !projection.Equal(newTestRelationProjection(t, false)) {
						t.Fatal("scalar derivation changed relation projection")
					}
				}
			}
		})
	}
}

func TestPlanWhereIsAuthoritativeAndConditionsAreComputedDFS(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	titleLeaf := expressionLeaf(t, query.NewCondition(title, query.LookupIContains, query.String("go")))
	summaryLeaf := expressionLeaf(t, query.NewCondition(summary, query.LookupIContains, query.String("go")))
	publishedLeaf := expressionLeaf(t, query.NewCondition(published, query.LookupExact, query.Boolean(false)))
	search, err := query.OrExpressions(titleLeaf, summaryLeaf)
	if err != nil {
		t.Fatalf("OrExpressions() error = %v", err)
	}
	excluded, err := query.NotExpression(publishedLeaf)
	if err != nil {
		t.Fatalf("NotExpression() error = %v", err)
	}
	where, err := query.AndExpressions(search, excluded)
	if err != nil {
		t.Fatalf("AndExpressions() error = %v", err)
	}

	base := query.NewPlan("news_article", []query.FieldRef{title, summary, published})
	if zero, ok := base.Where(); ok || zero != (query.Expression{}) {
		t.Fatalf("base Where() = (%#v, %v), want zero/false", zero, ok)
	}
	filtered, err := base.WithWhere(where)
	if err != nil {
		t.Fatalf("WithWhere() error = %v", err)
	}
	stored, ok := filtered.Where()
	if !ok || !stored.Equal(where) {
		t.Fatalf("filtered Where() = (%#v, %v), want authoritative input", stored, ok)
	}
	conditions := filtered.Conditions()
	if len(conditions) != 3 {
		t.Fatalf("diagnostic condition count = %d, want 3", len(conditions))
	}
	if conditions[0].Field().Name() != "title" || conditions[1].Field().Name() != "summary" ||
		conditions[2].Field().Name() != "published" {
		t.Fatalf("diagnostic DFS order = %q, %q, %q", conditions[0].Field().Name(), conditions[1].Field().Name(), conditions[2].Field().Name())
	}
	conditions[0] = query.NewCondition(title, query.LookupExact, query.String("mutated"))
	children := stored.Children()
	children[0] = publishedLeaf
	again, ok := filtered.Where()
	if !ok || !again.Equal(where) || filtered.Conditions()[0].Lookup() != query.LookupIContains {
		t.Fatal("Where or Conditions accessor exposed authoritative plan storage")
	}
	if len(base.Conditions()) != 0 {
		t.Fatal("WithWhere mutated its source plan")
	}
}

func TestPlanImplicitAndConvergesAcrossConditionAndExpressionPaths(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	titleCondition := query.NewCondition(title, query.LookupExact, query.String("GoDj"))
	publishedCondition := query.NewCondition(published, query.LookupExact, query.Boolean(true))
	base := query.NewPlan("news_article", []query.FieldRef{title, published})

	variadicConditions := querytest.Conditions(t, base, titleCondition, publishedCondition)
	repeatedConditions := querytest.Conditions(t, querytest.Conditions(t, base, titleCondition), publishedCondition)
	if !variadicConditions.Equal(repeatedConditions) {
		t.Fatal("variadic and repeated WithConditions produced different trees")
	}
	titleLeaf := expressionLeaf(t, titleCondition)
	publishedLeaf := expressionLeaf(t, publishedCondition)
	combined, err := query.AndExpressions(titleLeaf, publishedLeaf)
	if err != nil {
		t.Fatalf("AndExpressions() error = %v", err)
	}
	variadicWhere, err := base.WithWhere(combined)
	if err != nil {
		t.Fatalf("WithWhere(combined) error = %v", err)
	}
	repeatedWhere, err := base.WithWhere(titleLeaf)
	if err != nil {
		t.Fatalf("WithWhere(title) error = %v", err)
	}
	repeatedWhere, err = repeatedWhere.WithWhere(publishedLeaf)
	if err != nil {
		t.Fatalf("WithWhere(published) error = %v", err)
	}
	if !variadicConditions.Equal(variadicWhere) || !variadicWhere.Equal(repeatedWhere) {
		t.Fatal("condition and expression implicit-AND paths did not converge")
	}
	where, ok := repeatedWhere.Where()
	if !ok || where.Kind() != query.ExpressionAnd || len(where.Children()) != 2 {
		t.Fatalf("canonical repeated where = (%#v, %v)", where, ok)
	}
}

func TestPlanConditionConstructionRejectsInvalidInputWithoutPartialPlan(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("title", "title", query.FieldString, false)
	base := query.NewPlan("news_article", []query.FieldRef{field})
	valid := query.NewCondition(field, query.LookupExact, query.String("valid"))
	oversized := make([]query.Condition, 1024)
	for index := range oversized {
		oversized[index] = valid
	}
	for name, conditions := range map[string][]query.Condition{
		"zero":           {query.Condition{}},
		"foreign field":  {query.NewCondition(query.NewFieldRef("other", "other", query.FieldString, false), query.LookupExact, query.String("x"))},
		"unknown lookup": {query.NewCondition(field, query.Lookup("unknown"), query.String("x"))},
		"partial input":  {valid, query.Condition{}},
		"node cap":       oversized,
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := base.WithConditions(conditions...)
			if !isInvalidPlan(err) || !plan.Equal(query.Plan{}) {
				t.Fatalf("invalid construction = (%#v, %v), want zero plan and invalid_plan", plan, err)
			}
			if _, exists := base.Where(); exists {
				t.Fatal("failed construction changed source")
			}
		})
	}
	if _, err := base.WithWhere(query.Expression{}); !isInvalidPlan(err) {
		t.Fatalf("zero expression = %v", err)
	}
	if empty, err := base.WithConditions(); err != nil || !empty.Equal(base) {
		t.Fatalf("empty conjunction = (%#v,%v)", empty, err)
	}
	bounded := querytest.Conditions(t, base, oversized[:1023]...)
	if _, err := bounded.WithConditions(valid); !isInvalidPlan(err) {
		t.Fatalf("combined node cap = %v", err)
	}
}

func TestPlanWithWhereBindsSourceFieldsAndKeepsRelationsRootConjunctive(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	authorKey := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	authorName := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post",
		"author",
		"author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author",
		"id",
		false,
		authorName,
	)
	if err != nil {
		t.Fatalf("NewForwardRelationPath() error = %v", err)
	}
	related := expressionLeaf(t, query.NewRelatedCondition(path, query.LookupExact, query.String("Ada")))
	scalar := expressionLeaf(t, query.NewCondition(title, query.LookupIContains, query.String("go")))
	base := query.NewPlan("blog_post", []query.FieldRef{id, title, authorKey})
	if _, err := base.WithWhere(related); err != nil {
		t.Fatalf("WithWhere(root related terminal outside SourceFields) error = %v", err)
	}
	conjunctive, err := query.AndExpressions(scalar, related)
	if err != nil {
		t.Fatalf("AndExpressions(scalar, related) error = %v", err)
	}
	if _, err := base.WithWhere(conjunctive); err != nil {
		t.Fatalf("WithWhere(root-conjunctive relation) error = %v", err)
	}

	disjunctive, err := query.OrExpressions(scalar, related)
	if err != nil {
		t.Fatalf("OrExpressions(scalar, related) error = %v", err)
	}
	if _, err := base.WithWhere(disjunctive); !errors.Is(err, &query.Error{
		Category: query.CategoryQuery,
		Code:     query.CodeUnsupported,
		Field:    "name",
		Lookup:   string(query.LookupExact),
	}) {
		t.Fatalf("WithWhere(relation under OR) error = %v, want structured unsupported", err)
	}
	negated, err := query.NotExpression(related)
	if err != nil {
		t.Fatalf("NotExpression(related) error = %v", err)
	}
	if _, err := base.WithWhere(negated); !errors.Is(err, &query.Error{
		Category: query.CategoryQuery,
		Code:     query.CodeUnsupported,
		Field:    "name",
		Lookup:   string(query.LookupExact),
	}) {
		t.Fatalf("WithWhere(relation under NOT) error = %v, want structured unsupported", err)
	}

	foreignScalar := expressionLeaf(t, query.NewCondition(authorName, query.LookupExact, query.String("Ada")))
	if _, err := base.WithWhere(foreignScalar); !isInvalidPlan(err) {
		t.Fatalf("WithWhere(foreign scalar) error = %v, want invalid_plan", err)
	}
	missingSourceKey := query.NewPlan("blog_post", []query.FieldRef{id, title})
	if _, err := missingSourceKey.WithWhere(related); !isInvalidPlan(err) {
		t.Fatalf("WithWhere(relation missing source key) error = %v, want invalid_plan", err)
	}
}

func TestPlanLimitValidation(t *testing.T) {
	t.Parallel()

	_, err := query.NewPlan("news_article", nil).WithLimit(-1)
	var queryError *query.Error
	if !errors.As(err, &queryError) || queryError.Code != query.CodeInvalidLimit {
		t.Fatalf("error = %v, want invalid_limit", err)
	}
}

func TestProjectionErrorCodesAreStableAndDistinct(t *testing.T) {
	t.Parallel()

	if query.CodeInvalidRelatedPath != "invalid_related_path" {
		t.Fatalf("CodeInvalidRelatedPath = %q", query.CodeInvalidRelatedPath)
	}
	if query.CodeRelatedObjectProjection != "related_object_projection" {
		t.Fatalf("CodeRelatedObjectProjection = %q", query.CodeRelatedObjectProjection)
	}
	if query.CodeInvalidRelatedPath == query.CodeRelatedObjectProjection {
		t.Fatal("projection taxonomy codes collide")
	}
}

func TestInConditionClonesValuesAndPlanEqualityUsesListContents(t *testing.T) {
	t.Parallel()

	author := query.NewFieldRef("author", "author_id", query.FieldInteger, true)
	input := []query.Value{query.Integer(3), query.Integer(1), query.Integer(2)}
	condition, err := query.NewInCondition(author, input)
	if err != nil {
		t.Fatalf("NewInCondition() error = %v", err)
	}
	assertQueryConditionComparable(condition)
	input[0] = query.Integer(999)

	values, ok := condition.Values()
	if !ok || len(values) != 3 {
		t.Fatalf("Values() = (%#v, %v)", values, ok)
	}
	first, firstOK := values[0].Integer()
	if !firstOK || first != 3 {
		t.Fatalf("first IN value = (%d, %v), want (3, true)", first, firstOK)
	}
	values[0] = query.Integer(888)
	again, ok := condition.Values()
	if !ok {
		t.Fatal("second Values() reports invalid")
	}
	first, firstOK = again[0].Integer()
	if !firstOK || first != 3 {
		t.Fatalf("Values() exposed condition storage: (%d, %v)", first, firstOK)
	}
	if condition.Value().Kind() != "" {
		t.Fatalf("IN Value() kind = %q, want zero", condition.Value().Kind())
	}

	base := query.NewPlan("blog_post", []query.FieldRef{author})
	plan := querytest.Conditions(t, base, condition)
	equalCondition, err := query.NewInCondition(author, []query.Value{
		query.Integer(3), query.Integer(1), query.Integer(2),
	})
	if err != nil {
		t.Fatalf("second NewInCondition() error = %v", err)
	}
	if !plan.Equal(querytest.Conditions(t, base, equalCondition)) {
		t.Fatal("plans with equal IN contents differ")
	}
	differentCondition, err := query.NewInCondition(author, []query.Value{
		query.Integer(1), query.Integer(2), query.Integer(3),
	})
	if err != nil {
		t.Fatalf("different NewInCondition() error = %v", err)
	}
	if plan.Equal(querytest.Conditions(t, base, differentCondition)) {
		t.Fatal("plans with differently ordered IN contents compare equal")
	}

	returned := plan.Conditions()
	returnedValues, ok := returned[0].Values()
	if !ok {
		t.Fatal("cloned plan condition lost IN values")
	}
	returnedValues[0] = query.Integer(777)
	canonical, ok := plan.Conditions()[0].Values()
	if !ok {
		t.Fatal("canonical plan condition lost IN values")
	}
	first, firstOK = canonical[0].Integer()
	if !firstOK || first != 3 {
		t.Fatalf("Conditions() exposed IN storage: (%d, %v)", first, firstOK)
	}
}

func TestInConditionValidationAndScalarMisuse(t *testing.T) {
	t.Parallel()

	validInteger := query.NewFieldRef("author", "author_id", query.FieldInteger, false)
	tests := []struct {
		name   string
		field  query.FieldRef
		values []query.Value
	}{
		{name: "zero field", values: []query.Value{query.Integer(1)}},
		{name: "missing name", field: query.NewFieldRef("", "author_id", query.FieldInteger, false), values: []query.Value{query.Integer(1)}},
		{name: "missing column", field: query.NewFieldRef("author", "", query.FieldInteger, false), values: []query.Value{query.Integer(1)}},
		{name: "NUL name", field: query.NewFieldRef("author\x00", "author_id", query.FieldInteger, false), values: []query.Value{query.Integer(1)}},
		{name: "NUL column", field: query.NewFieldRef("author", "author_id\x00", query.FieldInteger, false), values: []query.Value{query.Integer(1)}},
		{name: "unsupported kind", field: query.NewFieldRef("score", "score", query.FieldKind("float"), false), values: []query.Value{query.Integer(1)}},
		{name: "empty", field: validInteger},
		{name: "NULL", field: validInteger, values: []query.Value{query.Null()}},
		{name: "wrong kind", field: validInteger, values: []query.Value{query.String("1")}},
		{name: "zero value", field: validInteger, values: []query.Value{{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			condition, err := query.NewInCondition(test.field, test.values)
			var queryError *query.Error
			if !errors.As(err, &queryError) || queryError.Category != query.CategoryQuery ||
				queryError.Code != query.CodeInvalidPlan {
				t.Fatalf("NewInCondition() error = %v, want query_error/invalid_plan", err)
			}
			if condition != (query.Condition{}) {
				t.Fatalf("failed NewInCondition() = %#v, want zero", condition)
			}
		})
	}

	for _, test := range []struct {
		field query.FieldRef
		value query.Value
	}{
		{query.NewFieldRef("title", "title", query.FieldString, true), query.String("Alpha")},
		{query.NewFieldRef("published", "published", query.FieldBoolean, false), query.Boolean(true)},
	} {
		condition, err := query.NewInCondition(test.field, []query.Value{test.value})
		if err != nil {
			t.Fatalf("supported NewInCondition(%q) error = %v", test.field.Kind(), err)
		}
		if values, ok := condition.Values(); !ok || len(values) != 1 || !values[0].Equal(test.value) {
			t.Fatalf("supported Values(%q) = (%#v, %v)", test.field.Kind(), values, ok)
		}
	}

	scalarMisuse := query.NewCondition(validInteger, query.LookupIn, query.Integer(1))
	if values, ok := scalarMisuse.Values(); ok || values != nil {
		t.Fatalf("scalar IN Values() = (%#v, %v), want (nil, false)", values, ok)
	}
	if scalarMisuse.Value().Kind() != "" {
		t.Fatalf("scalar IN Value() kind = %q, want zero", scalarMisuse.Value().Kind())
	}
	normal := query.NewCondition(validInteger, query.LookupExact, query.Integer(1))
	if values, ok := normal.Values(); ok || values != nil {
		t.Fatalf("scalar exact Values() = (%#v, %v), want (nil, false)", values, ok)
	}
	if value, ok := normal.Value().Integer(); !ok || value != 1 {
		t.Fatalf("scalar exact Value() = (%d, %v)", value, ok)
	}
}

func assertQueryConditionComparable[T comparable](T) {}

func TestMutationPlansDoNotExposeMutableAssignmentStorage(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	assignments := []query.Assignment{
		query.NewAssignment(title, query.String("before")),
		query.NewAssignment(published, query.Boolean(false)),
	}
	insert := query.NewInsertPlan("news_article", assignments)
	returningInsert := query.NewInsertPlanReturningKey("news_article", assignments, id)
	update := query.NewUpdatePlan("news_article", assignments, id, query.Integer(7))
	assignments[0] = query.NewAssignment(title, query.String("source-mutated"))

	returned := insert.Assignments()
	returned[0] = query.NewAssignment(title, query.String("getter-mutated"))
	if got, _ := insert.Assignments()[0].Value().String(); got != "before" {
		t.Fatalf("insert plan was mutated through external slice: %q", got)
	}
	if got, _ := returningInsert.Assignments()[0].Value().String(); got != "before" {
		t.Fatalf("returning insert plan shared source assignments: %q", got)
	}
	if got, _ := update.Assignments()[0].Value().String(); got != "before" {
		t.Fatalf("update plan shared source assignments: %q", got)
	}
	if !query.NewDeletePlan("news_article", id, query.Integer(7)).Equal(
		query.NewDeletePlan("news_article", id, query.Integer(7)),
	) {
		t.Fatal("equal delete plans differ")
	}
}

func TestInsertPlanReturningKeyPresenceAndEquality(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	otherID := query.NewFieldRef("article_id", "article_id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	assignments := []query.Assignment{query.NewAssignment(title, query.String("GoDj"))}

	legacy := query.NewInsertPlan("news_article", assignments)
	if key, present := legacy.ReturningKey(); present || !key.Equal(query.FieldRef{}) {
		t.Fatalf("legacy ReturningKey() = (%#v, %v), want zero/false", key, present)
	}

	returning := query.NewInsertPlanReturningKey("news_article", assignments, id)
	if key, present := returning.ReturningKey(); !present || !key.Equal(id) {
		t.Fatalf("returning key = (%#v, %v), want (%#v, true)", key, present, id)
	}
	if !returning.Equal(query.NewInsertPlanReturningKey("news_article", assignments, id)) {
		t.Fatal("identical returning insert plans differ")
	}
	if returning.Equal(legacy) {
		t.Fatal("returning insert plan equals plan without a returning key")
	}
	if returning.Equal(query.NewInsertPlanReturningKey("news_article", assignments, otherID)) {
		t.Fatal("insert plans with different returning keys compare equal")
	}
}

func TestNullValueBindsAsUntypedSQLNull(t *testing.T) {
	t.Parallel()

	value := query.Null()
	got, err := value.DatabaseValue()
	if err != nil {
		t.Fatalf("DatabaseValue() error = %v", err)
	}
	if !value.IsNull() || !reflect.DeepEqual(got, nil) {
		t.Fatalf("null value = kind %q database %#v", value.Kind(), got)
	}
}
