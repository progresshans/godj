package query

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestComparisonRHSUnionRejectsMalformedAndRelationShapes(t *testing.T) {
	t.Parallel()

	title := NewFieldRef("title", "title", FieldString, false)
	summary := NewFieldRef("summary", "summary", FieldString, true)

	malformed := []Condition{
		{field: title, lookup: LookupExact},
		{field: title, lookup: LookupExact, rhs: &conditionRHS{kind: conditionRHSKind(99)}},
		{field: title, lookup: LookupExact, rhs: &conditionRHS{kind: conditionRHSLiteral, value: String("x"), field: summary}},
		{field: title, lookup: LookupIn, rhs: &conditionRHS{kind: conditionRHSList, value: String("x"), values: []Value{String("x")}}},
		{field: title, lookup: LookupExact, rhs: &conditionRHS{kind: conditionRHSField, value: String("x"), field: summary}},
	}
	for index, condition := range malformed {
		if err := validateExpressionCondition(condition); err == nil {
			t.Fatalf("malformed condition %d unexpectedly validated: %#v", index, condition)
		}
	}

	path, err := NewForwardRelationPath(
		ir.ModelIdentity{AppLabel: "blog", ModelName: "post"},
		"blog_post", "author", "author_id",
		ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		"authors_author", "id", false, summary,
	)
	if err != nil {
		t.Fatalf("NewForwardRelationPath() error = %v", err)
	}
	relatedFieldRHS := Condition{
		field:        summary,
		lookup:       LookupExact,
		rhs:          &conditionRHS{kind: conditionRHSField, field: title},
		relationPath: &path,
	}
	if err := validateExpressionCondition(relatedFieldRHS); err == nil {
		t.Fatal("relation condition with field RHS unexpectedly validated")
	}
}

func TestComparisonConditionCloneDoesNotAliasRHSList(t *testing.T) {
	t.Parallel()

	field := NewFieldRef("id", "id", FieldInteger, false)
	condition, err := NewInCondition(field, []Value{Integer(1), Integer(2)})
	if err != nil {
		t.Fatalf("NewInCondition() error = %v", err)
	}
	clone := condition.clone()
	clone.rhs.values[0] = Integer(99)
	value, ok := condition.rhs.values[0].Integer()
	if !ok || value != 1 {
		t.Fatalf("clone mutated source RHS list: (%d, %v)", value, ok)
	}
}
