package query_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestPlanDerivationDoesNotMutateSource(t *testing.T) {
	t.Parallel()

	title := query.NewFieldRef("title", "title", query.FieldString, false)
	base := query.NewPlan("news_article", []query.FieldRef{title})
	filtered := base.WithConditions(query.NewCondition(title, query.LookupExact, query.String("Django")))
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
	plan := base.WithConditions(condition)
	equalCondition, err := query.NewInCondition(author, []query.Value{
		query.Integer(3), query.Integer(1), query.Integer(2),
	})
	if err != nil {
		t.Fatalf("second NewInCondition() error = %v", err)
	}
	if !plan.Equal(base.WithConditions(equalCondition)) {
		t.Fatal("plans with equal IN contents differ")
	}
	differentCondition, err := query.NewInCondition(author, []query.Value{
		query.Integer(1), query.Integer(2), query.Integer(3),
	})
	if err != nil {
		t.Fatalf("different NewInCondition() error = %v", err)
	}
	if plan.Equal(base.WithConditions(differentCondition)) {
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
