package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func TestIExactRequiresStringLiteralAndPreservesItsBytes(t *testing.T) {
	for _, nullable := range []bool{false, true} {
		field := query.NewFieldRef("title", "title", query.FieldString, nullable)
		for _, value := range []string{"", "ALPHA", `50%_\'`, "ÉLAN", "e\u0301", "a\nb"} {
			expression, err := query.NewExpression(query.NewCondition(field, query.LookupIExact, query.String(value)))
			if err != nil {
				t.Fatal(err)
			}
			condition, ok := expression.Condition()
			got, stringValue := condition.Value().String()
			if !ok || !stringValue || got != value || condition.Lookup() != query.LookupIExact {
				t.Fatal("literal normalized or changed")
			}
		}
		for _, value := range []query.Value{query.Null(), query.Integer(1), query.Boolean(true), {}} {
			if _, err := query.NewExpression(query.NewCondition(field, query.LookupIExact, value)); err == nil {
				t.Fatal("invalid iexact RHS accepted", value.Kind())
			}
		}
		if _, err := query.NewFieldCondition(field, query.LookupIExact, field); err == nil {
			t.Fatal("unsupported iexact field expression accepted")
		}
	}
	for _, kind := range []query.FieldKind{query.FieldInteger, query.FieldBoolean, query.FieldJSON, query.FieldFloat, query.FieldDateTime} {
		field := query.NewFieldRef("value", "value", kind, true)
		if _, err := query.NewExpression(query.NewCondition(field, query.LookupIExact, query.String("1"))); err == nil {
			t.Fatal("iexact accepted non-string field", kind)
		}
	}
}
