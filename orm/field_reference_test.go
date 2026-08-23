package orm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTypedAndDynamicOrderedComparisonsConvergeWithoutIO(t *testing.T) {
	t.Parallel()

	backend := &spyBackend{}
	base := models.ArticleObjects.Using(backend)
	tests := []struct {
		name    string
		typed   orm.QuerySet[models.Article]
		dynamic orm.LookupInput
	}{
		{name: "integer gt", typed: base.Filter(models.ArticleFields.ID.GreaterThan(1)), dynamic: orm.LookupInput{Key: "id__gt", Value: int64(1)}},
		{name: "integer gte", typed: base.Filter(models.ArticleFields.ID.GreaterThanOrEqual(1)), dynamic: orm.LookupInput{Key: "id__gte", Value: int64(1)}},
		{name: "integer lt", typed: base.Filter(models.ArticleFields.ID.LessThan(4)), dynamic: orm.LookupInput{Key: "id__lt", Value: int64(4)}},
		{name: "integer lte", typed: base.Filter(models.ArticleFields.ID.LessThanOrEqual(4)), dynamic: orm.LookupInput{Key: "id__lte", Value: int64(4)}},
		{name: "string gt", typed: base.Filter(models.ArticleFields.Title.GreaterThan("a")), dynamic: orm.LookupInput{Key: "title__gt", Value: "a"}},
		{name: "string gte", typed: base.Filter(models.ArticleFields.Title.GreaterThanOrEqual("a")), dynamic: orm.LookupInput{Key: "title__gte", Value: "a"}},
		{name: "nullable string lt", typed: base.Filter(models.ArticleFields.Summary.LessThan("z")), dynamic: orm.LookupInput{Key: "summary__lt", Value: "z"}},
		{name: "nullable string lte", typed: base.Filter(models.ArticleFields.Summary.LessThanOrEqual("z")), dynamic: orm.LookupInput{Key: "summary__lte", Value: "z"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			predicates, err := orm.ParseDynamic(models.ArticleDescriptor{}, nil, []orm.LookupInput{test.dynamic})
			if err != nil {
				t.Fatalf("ParseDynamic() error = %v", err)
			}
			dynamic := base.Filter(predicates...)
			if !test.typed.Plan().Equal(dynamic.Plan()) {
				t.Fatalf("typed and dynamic comparison plans differ:\ntyped=%#v\ndynamic=%#v", test.typed.Plan(), dynamic.Plan())
			}
		})
	}
	if backend.calls.Load() != 0 {
		t.Fatalf("comparison construction performed %d backend calls", backend.calls.Load())
	}
}

func TestTypedFieldReferencesPreserveModelKindAndNullableBaseType(t *testing.T) {
	t.Parallel()

	backend := &spyBackend{}
	base := models.ArticleObjects.Using(backend)
	predicates := []struct {
		lookup    query.Lookup
		predicate orm.Predicate[models.Article]
		right     query.FieldRef
	}{
		{lookup: query.LookupExact, predicate: models.ArticleFields.ID.ExactField(orm.F(models.ArticleFields.ID)), right: query.NewFieldRef("id", "id", query.FieldInteger, false)},
		{lookup: query.LookupGreaterThan, predicate: models.ArticleFields.ID.GreaterThanField(orm.F(models.ArticleFields.ID)), right: query.NewFieldRef("id", "id", query.FieldInteger, false)},
		{lookup: query.LookupGreaterThanOrEqual, predicate: models.ArticleFields.ID.GreaterThanOrEqualField(orm.F(models.ArticleFields.ID)), right: query.NewFieldRef("id", "id", query.FieldInteger, false)},
		{lookup: query.LookupLessThan, predicate: models.ArticleFields.ID.LessThanField(orm.F(models.ArticleFields.ID)), right: query.NewFieldRef("id", "id", query.FieldInteger, false)},
		{lookup: query.LookupLessThanOrEqual, predicate: models.ArticleFields.ID.LessThanOrEqualField(orm.F(models.ArticleFields.ID)), right: query.NewFieldRef("id", "id", query.FieldInteger, false)},
		{lookup: query.LookupExact, predicate: models.ArticleFields.Title.ExactField(orm.F(models.ArticleFields.Summary)), right: query.NewFieldRef("summary", "summary", query.FieldString, true)},
		{lookup: query.LookupGreaterThan, predicate: models.ArticleFields.Summary.GreaterThanField(orm.F(models.ArticleFields.Title)), right: query.NewFieldRef("title", "title", query.FieldString, false)},
		{lookup: query.LookupGreaterThanOrEqual, predicate: models.ArticleFields.Title.GreaterThanOrEqualField(orm.F(models.ArticleFields.Summary)), right: query.NewFieldRef("summary", "summary", query.FieldString, true)},
		{lookup: query.LookupLessThan, predicate: models.ArticleFields.Summary.LessThanField(orm.F(models.ArticleFields.Title)), right: query.NewFieldRef("title", "title", query.FieldString, false)},
		{lookup: query.LookupLessThanOrEqual, predicate: models.ArticleFields.Title.LessThanOrEqualField(orm.F(models.ArticleFields.Summary)), right: query.NewFieldRef("summary", "summary", query.FieldString, true)},
	}
	for _, test := range predicates {
		plan := base.Filter(test.predicate).Plan()
		conditions := plan.Conditions()
		if len(conditions) != 1 || conditions[0].Lookup() != test.lookup {
			t.Fatalf("field predicate conditions = %#v, want one %q", conditions, test.lookup)
		}
		if right, ok := conditions[0].RHSField(); !ok || !right.Equal(test.right) {
			t.Fatalf("RHSField() = (%#v, %v), want %#v/true", right, ok, test.right)
		}
	}
	if backend.calls.Load() != 0 {
		t.Fatalf("field-reference construction performed %d backend calls", backend.calls.Load())
	}
}

func TestInvalidFieldReferencesFailBeforeIO(t *testing.T) {
	t.Parallel()

	metadata := models.ArticleDescriptor{}.Metadata()
	foreignMetadata := ir.Field{
		Name:      "other_title",
		GoName:    "OtherTitle",
		Column:    "other_title",
		Kind:      ir.FieldChar,
		MaxLength: 200,
	}
	foreign := orm.NewStringField[models.Article](foreignMetadata)
	wrong := orm.NewStringField[models.Article](metadata.Fields[2])
	var zero orm.FieldReference[models.Article, string]

	for name, predicate := range map[string]orm.Predicate[models.Article]{
		"RHS absent from source":  models.ArticleFields.Title.ExactField(orm.F(foreign)),
		"invalid RHS constructor": models.ArticleFields.Title.ExactField(orm.F(wrong)),
		"zero field reference":    models.ArticleFields.Title.ExactField(zero),
	} {
		t.Run(name, func(t *testing.T) {
			backend := &spyBackend{}
			_, err := models.ArticleObjects.Using(backend).Filter(predicate).All(context.Background())
			if !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
				t.Fatalf("All() error = %v, want invalid_plan", err)
			}
			if backend.calls.Load() != 0 {
				t.Fatalf("invalid field reference performed %d backend calls", backend.calls.Load())
			}
		})
	}
}

func TestDynamicOrderedComparisonsRejectBooleanAndWrongValues(t *testing.T) {
	t.Parallel()

	for _, input := range []orm.LookupInput{
		{Key: "published__gt", Value: true},
		{Key: "id__gte", Value: "1"},
		{Key: "title__lte", Value: int64(1)},
	} {
		if predicates, err := orm.ParseDynamic(models.ArticleDescriptor{}, nil, []orm.LookupInput{input}); err == nil || predicates != nil {
			t.Fatalf("ParseDynamic(%#v) = (%#v, %v), want nil/error", input, predicates, err)
		}
	}
}
