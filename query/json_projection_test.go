package query_test

import (
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
