package query_test

import (
	"errors"
	"github.com/progresshans/godj/schema/ir"
	"strconv"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestJSONProjectionKeepsSourceIdentityAndOwnsSelectedPaths(t *testing.T) {
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, false)
	segments := []query.JSONPathSegment{query.JSONKey("a"), query.JSONIndex(0)}
	path, err := query.NewJSONPath(segments...)
	if err != nil {
		t.Fatal(err)
	}
	samePath, _ := query.NewJSONPath(segments...)
	first, err := query.JSONPathResult(field, path)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := query.JSONPathResult(field, samePath)
	segments[0] = query.JSONKey("changed")
	returned, _ := first.JSONPath()
	returned.Segments()[0] = query.JSONKey("changed")
	if !first.Equal(same) {
		t.Fatal("projection path identity depends on pointer or caller storage")
	}
	source, ok := first.Field()
	if !ok || !source.Equal(field) || source.Nullable() {
		t.Fatal("path projection forged source nullability")
	}
	keyPath, _ := query.NewJSONPath(query.JSONKey("a"), query.JSONKey("0"))
	key, _ := query.JSONPathResult(field, keyPath)
	if first.Equal(key) {
		t.Fatal("key and index selected the same expression")
	}
	expressions := []query.ResultExpression{query.FieldResult(field), first, key}
	shape, err := query.NewProjectionResult(expressions...)
	if err != nil {
		t.Fatal(err)
	}
	expressions[1] = query.FieldResult(field)
	shape.Expressions()[1] = query.FieldResult(field)
	equal, err := query.NewProjectionResult(query.FieldResult(field), same, key)
	if err != nil || !shape.Equal(equal) {
		t.Fatal("selected expressions leaked mutation", err)
	}
	plan, err := query.NewPlan("records", []query.FieldRef{field}).WithResultShape(shape)
	if err != nil || !plan.ResultShape().Equal(shape) {
		t.Fatal(err)
	}
	for _, selected := range [][]query.ResultExpression{{first, same}, {query.FieldResult(field), query.FieldResult(field)}, {query.CountAllResult()}, {{}}} {
		if _, err := query.NewProjectionResult(selected...); err == nil {
			t.Fatal("invalid/duplicate selection accepted")
		}
	}
	for _, candidate := range []query.FieldRef{{}, query.NewFieldRef("label", "label", query.FieldString, false)} {
		if _, err := query.JSONPathResult(candidate, path); err == nil {
			t.Fatal("non-JSON path source accepted")
		}
	}
	if _, err := query.JSONPathResult(field, query.JSONPath{}); err == nil {
		t.Fatal("invalid path accepted")
	}
	foreign := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	expression, _ := query.JSONPathResult(foreign, path)
	foreignShape, _ := query.NewProjectionResult(expression)
	if _, err := query.NewPlan("records", []query.FieldRef{field}).WithResultShape(foreignShape); err == nil {
		t.Fatal("source metadata mismatch accepted")
	}
	if _, err := query.NewAggregateResult(first); err == nil {
		t.Fatal("path projection became aggregate")
	}
	// Delimiter-like literal keys must not collide in the duplicate detector.
	expressions = []query.ResultExpression{first, key}
	for _, segments := range [][]query.JSONPathSegment{{query.JSONKey("")}, {query.JSONKey("a;k0:")}, {query.JSONKey("a"), query.JSONKey("")}, {query.JSONKey("a"), query.JSONKey("i0;")}} {
		p, _ := query.NewJSONPath(segments...)
		e, _ := query.JSONPathResult(field, p)
		expressions = append(expressions, e)
	}
	if _, err := query.NewProjectionResult(expressions...); err != nil {
		t.Fatal("literal path keys collided", err)
	}
	boundary := make([]query.ResultExpression, query.MaxProjectionExpressions)
	for i := range boundary {
		p, _ := query.NewJSONPath(query.JSONKey(strconv.Itoa(i)))
		boundary[i], _ = query.JSONPathResult(field, p)
	}
	if _, err := query.NewProjectionResult(boundary...); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewProjectionResult(append(boundary, first)...); err == nil {
		t.Fatal("projection expression limit bypassed")
	}
}

func TestRelatedJSONProjectionOwnsRouteAndPreservesRootAuthority(t *testing.T) {
	payload := query.NewFieldRef("payload", "payload", query.FieldJSON, false)
	root := ir.ModelIdentity{AppLabel: "app", ModelName: "entry"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "document"}
	a, err := query.NewForwardRelationPath(root, "app_entry", "primary", "primary_id", target, "app_document", "id", true, payload, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	b, err := query.NewForwardRelationPath(root, "app_entry", "secondary", "secondary_id", target, "app_document", "id", true, payload, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	hops := a.Hops()
	route, err := query.NewForwardRelationChain(hops, payload, query.RelationTerminalRelatedField)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := query.NewJSONPath(query.JSONKey("a"))
	selected, err := query.RelatedJSONPathResult(route, p)
	if err != nil {
		t.Fatal(err)
	}
	hops[0] = b.Hops()[0]
	returned, ok := selected.RelationPath()
	if !ok {
		t.Fatal("missing selected route")
	}
	returned.Hops()[0] = b.Hops()[0]
	same, _ := query.RelatedJSONPathResult(a, p)
	other, _ := query.RelatedJSONPathResult(b, p)
	rootValue, _ := query.JSONPathResult(payload, p)
	if !selected.Equal(same) || selected.Equal(other) || selected.Equal(rootValue) {
		t.Fatal("result route identity is not structural/owned")
	}
	field, ok := selected.Field()
	if !ok || field.Nullable() || !field.Equal(payload) {
		t.Fatal("optional hop rewrote source terminal nullability")
	}
	shape, err := query.NewProjectionResult(rootValue, selected, other)
	if err != nil || !shape.HasRelations() {
		t.Fatal("root/related or repeated target columns collided", err)
	}
	source := []query.FieldRef{payload, query.NewFieldRef("primary", "primary_id", query.FieldInteger, true), query.NewFieldRef("secondary", "secondary_id", query.FieldInteger, true)}
	plan, err := query.NewPlan("app_entry", source).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	equalShape, _ := query.NewProjectionResult(rootValue, same, other)
	equalPlan, _ := query.NewPlan("app_entry", source).WithResultShape(equalShape)
	if !plan.Equal(equalPlan) {
		t.Fatal("identical selected routes differ")
	}
	for _, input := range []struct {
		table  string
		fields []query.FieldRef
	}{{"foreign", source}, {"app_entry", source[:1]}, {"app_entry", []query.FieldRef{payload, query.NewFieldRef("primary", "primary_id", query.FieldInteger, false), source[2]}}} {
		if _, err := query.NewPlan(input.table, input.fields).WithResultShape(shape); err == nil {
			t.Fatal("related selection bypassed root FK provenance")
		}
	}
	if _, err := query.NewProjectionResult(selected, same); err == nil {
		t.Fatal("same selected route repeated")
	}
	if _, err := query.NewAggregateResult(selected); err == nil {
		t.Fatal("related JSON result became aggregate")
	}
	reverse, err := query.NewReverseRelationPath(root, "app_entry", "primary", "primary_id", target, "app_document", "id", "entries", true, payload, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.RelatedJSONPathResult(reverse, p); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("reverse result widened", err)
	}
	if _, err := query.RelatedJSONPathResult(query.RelationPath{}, p); err == nil {
		t.Fatal("zero selected route accepted")
	}
	if _, err := query.RelatedJSONPathResult(a, query.JSONPath{}); err == nil {
		t.Fatal("zero selected JSON path accepted")
	}
}
