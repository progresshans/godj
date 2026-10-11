package query_test

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestOrderingExpressionOwnershipAndSourceAuthority(t *testing.T) {
	payload := query.NewFieldRef("payload", "payload", query.FieldJSON, false)
	fk := query.NewFieldRef("document", "document_id", query.FieldInteger, true)
	root := ir.ModelIdentity{AppLabel: "app", ModelName: "entry"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "document"}
	route, err := query.NewForwardRelationPath(root, "app_entry", "document", "document_id", target, "app_document", "id", true, payload, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	makeOrdering := func(related bool, direction query.Direction) query.Ordering {
		t.Helper()
		segments := []query.JSONPathSegment{query.JSONKey("a"), query.JSONIndex(0)}
		path, err := query.NewJSONPath(segments...)
		if err != nil {
			t.Fatal(err)
		}
		expression, err := query.JSONPathResult(payload, path)
		if related {
			expression, err = query.RelatedJSONPathResult(route, path)
		}
		if err != nil {
			t.Fatal(err)
		}
		ordering, err := query.NewResultOrdering(expression, direction)
		if err != nil {
			t.Fatal(err)
		}
		segments[0] = query.JSONKey("changed")
		detached, _ := ordering.Expression().JSONPath()
		detached.Segments()[0] = query.JSONKey("changed")
		return ordering
	}
	first, same := makeOrdering(true, query.Ascending), makeOrdering(true, query.Ascending)
	if !first.Equal(same) || first.Equal(makeOrdering(false, query.Ascending)) || first.Equal(makeOrdering(true, query.Descending)) {
		t.Fatal("ordering identity is not structural or route-specific")
	}
	base := query.NewPlan("app_entry", []query.FieldRef{payload, fk})
	orderings := []query.Ordering{first, makeOrdering(false, query.Descending)}
	plan := base.WithOrderings(orderings...)
	orderings[0] = query.NewOrdering(payload, query.Descending)
	plan.Orderings()[0] = orderings[0]
	if !plan.Equal(base.WithOrderings(same, makeOrdering(false, query.Descending))) || len(base.Orderings()) != 0 {
		t.Fatal("ordering storage is shared with callers")
	}
	if err := plan.ValidateOrderings(); err != nil {
		t.Fatal(err)
	}
	if first.Field().Nullable() || !first.Field().Equal(payload) {
		t.Fatal("optional relation changed target metadata")
	}
	for _, source := range []query.Plan{query.NewPlan("foreign", base.SourceFields()), query.NewPlan("app_entry", []query.FieldRef{payload}), query.NewPlan("app_entry", []query.FieldRef{payload, query.NewFieldRef("document", "document_id", query.FieldInteger, false)})} {
		if err := source.WithOrderings(first).ValidateOrderings(); err == nil {
			t.Fatal("ordering bypassed source authority")
		}
	}
	for _, expression := range []query.ResultExpression{{}, query.CountAllResult(), query.MaxResult(payload)} {
		if _, err := query.NewResultOrdering(expression, query.Ascending); err == nil {
			t.Fatal("ordering accepted invalid/aggregate expression")
		}
	}
	if _, err := query.NewResultOrdering(query.FieldResult(payload), query.Direction("sideways")); err == nil {
		t.Fatal("invalid ordering direction accepted")
	}
}
