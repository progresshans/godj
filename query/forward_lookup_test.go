package query_test

import (
	"errors"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestForwardMembershipOwnsValuesAndRetainsOptionalOperand(t *testing.T) {
	source := ir.ModelIdentity{AppLabel: "blog", ModelName: "post"}
	target := ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}
	field := query.NewFieldRef("active", "active", query.FieldBoolean, false)
	path, err := query.NewForwardRelationPath(source, "blog_post", "reviewer", "reviewer_id", target, "authors_author", "id", true, field, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	values := []query.Value{query.Boolean(false), query.Null()}
	condition, err := query.NewRelatedInCondition(path, values)
	if err != nil {
		t.Fatal(err)
	}
	values[0] = query.Boolean(true)
	detached, ok := condition.Values()
	if !ok || !detached[0].Equal(query.Boolean(false)) || !detached[1].IsNull() {
		t.Fatal("membership input storage leaked")
	}
	detached[1] = query.Boolean(true)
	original, ok := condition.Values()
	if !ok || !original[1].IsNull() || !condition.OperandNullable() || condition.Field().Nullable() {
		t.Fatal("optional operand or detached values changed")
	}
	sourceKey := query.NewFieldRef("reviewer", "reviewer_id", query.FieldInteger, true)
	nullPath, err := query.NewForwardRelationIsNullPath(source, "blog_post", sourceKey, target, "authors_author", "id", ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := query.NewReverseRelationPath(source, "blog_post", "reviewer", "reviewer_id", target, "authors_author", "id", "reviews", true, query.NewFieldRef("title", "title", query.FieldString, false), ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []query.RelationPath{{}, nullPath, reverse} {
		if _, err := query.NewRelatedInCondition(invalid, nil); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatalf("unsupported path=%v", err)
		}
	}
	if _, err := query.NewRelatedInCondition(path, []query.Value{query.Integer(1)}); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("wrong membership type=%v", err)
	}
}
