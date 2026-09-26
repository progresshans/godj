package query_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

func TestJSONPathOwnershipLimitsAndPredicateDomain(t *testing.T) {
	segments := []query.JSONPathSegment{query.JSONKey(""), query.JSONKey("0"), query.JSONIndex(0), query.JSONKey("\x00\"\\😀")}
	path, err := query.NewJSONPath(segments...)
	if err != nil {
		t.Fatal(err)
	}
	same, err := query.NewJSONPath(segments...)
	if err != nil {
		t.Fatal(err)
	}
	segments[0] = query.JSONKey("changed")
	owned := path.Segments()
	owned[1] = query.JSONIndex(0)
	if !path.Equal(same) {
		t.Fatal("path leaked mutable segments")
	}
	if key, ok := path.Segments()[1].Key(); !ok || key != "0" {
		t.Fatal("numeric key became index")
	}
	if index, ok := path.Segments()[2].Index(); !ok || index != 0 {
		t.Fatal("index became key")
	}
	for _, segments := range [][]query.JSONPathSegment{nil, {{}}, {query.JSONIndex(-1)}, {query.JSONKey(string([]byte{255}))}, {query.JSONKey(strings.Repeat("a", 4096)), query.JSONKey("a")}, make([]query.JSONPathSegment, 65)} {
		if got, err := query.NewJSONPath(segments...); err == nil || got.Valid() {
			t.Fatal("invalid path accepted")
		}
	}
	boundary := make([]query.JSONPathSegment, 64)
	for i := range boundary {
		boundary[i] = query.JSONKey(strings.Repeat("a", 64))
	}
	if _, err := query.NewJSONPath(boundary...); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewJSONPath(query.JSONIndex(2147483647)); err != nil {
		t.Fatal(err)
	}
	var zero query.JSONPath
	if zero.Valid() || zero.Segments() != nil || zero.Equal(path) || !zero.Equal(query.JSONPath{}) {
		t.Fatal("invalid zero path")
	}
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, false)
	base := query.NewCondition(field, query.LookupExact, query.JSON(jsonvalue.Null()))
	condition, err := base.WithJSONPath(path)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := base.WithJSONPath(same)
	if err != nil || !condition.Equal(copy) || condition.Equal(base) || condition.OperandNullable() {
		t.Fatal("path changed identity/null compensation")
	}
	if _, ok := base.JSONPath(); ok {
		t.Fatal("WithJSONPath mutated source")
	}
	if _, err := base.WithJSONPath(zero); err == nil {
		t.Fatal("zero path accepted")
	}
	member, err := query.NewInCondition(field, []query.Value{query.JSON(jsonvalue.Null()), query.Null()})
	if err != nil {
		t.Fatal(err)
	}
	other := query.NewFieldRef("other", "other", query.FieldJSON, false)
	fCondition, err := query.NewFieldCondition(field, query.LookupExact, other)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []query.Condition{member, fCondition, query.NewCondition(other, query.LookupIContains, query.Integer(1)), query.NewCondition(query.NewFieldRef("text", "text", query.FieldString, false), query.LookupExact, query.String("a"))} {
		if _, err := c.WithJSONPath(path); err == nil {
			t.Fatal("unsupported path predicate accepted")
		}
	}
	plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(condition)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := plan.Conditions()[0].JSONPath()
	if !got.Equal(path) {
		t.Fatal("plan lost path")
	}
}
