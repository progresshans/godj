package query

import (
	"errors"
	"slices"
	"testing"
)

func TestCollectionAggregatesKeepRowCardinalityAndSourceAuthority(t *testing.T) {
	field := NewFieldRef("amount", "amount", FieldInteger, false)
	path := collectionRouteFixture(t, field)
	id := path.PrimaryKeys()[0]
	for _, kind := range []ResultExpressionKind{ResultCount, ResultMin, ResultMax, ResultSum, ResultAvg} {
		t.Run(string(kind), func(t *testing.T) {
			value, err := RelatedAggregateResult(kind, path)
			if err != nil {
				t.Fatal(err)
			}
			retained, related := value.RelationPath()
			leaf, hasField := value.Field()
			wantKind := FieldInteger
			if kind == ResultAvg {
				wantKind = FieldFloat
			}
			if !value.IsAggregate() || !related || !retained.Equal(path) || !hasField || !leaf.Equal(field) ||
				value.ResultValueKind() != wantKind || value.ResultNullable() != (kind != ResultCount) {
				t.Fatal("aggregate changed source identity or result domain")
			}
			shape, err := NewGroupedResult([]ResultExpression{FieldResult(id)}, []ResultExpression{value})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := NewPlan("owners", []FieldRef{id}).WithResultShape(shape)
			if err != nil {
				t.Fatal(err)
			}
			having, err := NewGroupExpression(value, LookupIsNull, Boolean(false))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.WithGroupHaving(having); err != nil {
				t.Fatal("aggregate identity was lost", err)
			}
			for _, source := range []Plan{
				NewPlan("other", []FieldRef{id}),
				NewPlan("owners", []FieldRef{NewFieldRef("other", "id", FieldInteger, false)}),
				NewPlan("owners", []FieldRef{NewFieldRef("id", "id", FieldInteger, true)}),
			} {
				if _, err := source.WithResultShape(shape); err == nil {
					t.Fatal("aggregate accepted a different root authority")
				}
			}
			if _, err := NewProjectionResult(value); err == nil {
				t.Fatal("aggregate escaped into a row projection")
			}
			if _, err := NewGroupedResult([]ResultExpression{value}, []ResultExpression{CountAllResult()}); err == nil {
				t.Fatal("aggregate escaped into a group key")
			}
			if _, err := NewResultOrdering(value, Ascending); err == nil {
				t.Fatal("aggregate escaped into row ordering")
			}
			if _, err := AggregateResult(ResultCount, value); err == nil {
				t.Fatal("nested aggregation lost its required result stage")
			}
		})
	}
	if _, err := RelatedFieldResult(path); !errors.Is(err, &Error{Code: CodeUnsupported}) {
		t.Fatal("collection became a row scalar selection", err)
	}
	collection, err := RelatedExpression(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ScalarResult(collection); !errors.Is(err, &Error{Code: CodeUnsupported}) {
		t.Fatal("collection escaped into a row scalar selection", err)
	}
	if err := NewPlan("owners", []FieldRef{id}).ValidateScalar(collection); !errors.Is(err, &Error{Code: CodeUnsupported}) {
		t.Fatal("collection escaped into row scalar source validation", err)
	}
	for _, kind := range []ResultExpressionKind{ResultField, ResultScalar, ResultCountAll, "unknown"} {
		if _, err := RelatedAggregateResult(kind, path); err == nil {
			t.Fatal("incomplete or malformed aggregate escaped", kind)
		}
	}
	if _, err := RelatedAggregateResult(ResultCount, collectionRouteFixture(t, NewFieldRef("payload", "payload", FieldJSON, true))); err == nil {
		t.Fatal("JSON aggregate accepted")
	}
	invalid := collectionRouteFixture(t, NewDecimalFieldRef("price", "price", false, 14, 2))
	invalid.terminal = NewFieldRef("price", "price", FieldDecimal, false)
	if _, err := RelatedAggregateResult(ResultSum, invalid); err == nil {
		t.Fatal("aggregate fabricated missing source precision")
	}
}

func TestCollectionAggregateFiltersOwnOccurrenceAndKeepImmutableMetadata(t *testing.T) {
	path := collectionRouteFixture(t, NewFieldRef("amount", "amount", FieldInteger, true))
	id := path.PrimaryKeys()[0]
	count, err := CountResult(FieldResult(id))
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := NewExpression(NewRelatedCondition(path, LookupGreaterThan, Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	not, err := NotExpression(predicate)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := count.WithFilter(not)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := NewAggregateResult(filtered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlan("owners", []FieldRef{id}).WithResultShape(shape); err != nil {
		t.Fatal(err)
	}
	if _, present := count.Filter(); present || count.Equal(filtered) {
		t.Fatal("conditional aggregate changed its input or lost predicate identity")
	}
	bound, err := path.WithCollectionFilterScope(1)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Equal(path) || !slices.Equal(bound.PrimaryKeys(), path.PrimaryKeys()) || bound.Terminal() != path.Terminal() {
		t.Fatal("occurrence binding changed declaration metadata")
	}
	for index, hop := range bound.Hops() {
		if hop.FilterScope() != 1 || path.Hops()[index].FilterScope() != 0 {
			t.Fatal("occurrence binding mutated its input")
		}
	}
	if _, err := RelatedAggregateResult(ResultSum, bound); !errors.Is(err, &Error{Code: CodeInvalidPlan}) {
		t.Fatal("aggregate borrowed a prebound filter occurrence", err)
	}
	borrowed, err := NewExpression(NewRelatedCondition(bound, LookupExact, Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := count.WithFilter(borrowed); !errors.Is(err, &Error{Code: CodeInvalidPlan}) {
		t.Fatal("aggregate condition borrowed a prebound filter occurrence", err)
	}
	if _, err := path.WithCollectionFilterScope(^uint32(0)); !errors.Is(err, &Error{Code: CodeInvalidPlan}) {
		t.Fatal("collection scope bypassed resource bound", err)
	}
	var zero RelationPath
	if _, err := zero.WithCollectionFilterScope(0); err == nil {
		t.Fatal("zero path became a compiler occurrence")
	}
}
