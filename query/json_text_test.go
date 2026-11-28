package query_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

func TestJSONTextPredicatesRequireLiteralBoundedStringsAndKeepPathIdentity(t *testing.T) {
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	path, err := query.NewJSONPath(query.JSONKey("source"))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"", "50%_\\", "한글", "a\x00b", strings.Repeat("a", jsonvalue.MaxStringBytes)} {
		condition := query.NewCondition(field, query.LookupIContains, query.String(text))
		if _, err := query.NewExpression(condition); err != nil {
			t.Fatal(err)
		}
		nested, err := condition.WithJSONPath(path)
		if err != nil {
			t.Fatal(err)
		}
		if !nested.Field().Equal(field) || !nested.OperandNullable() {
			t.Fatal("text lookup changed metadata/null semantics")
		}
		if got, ok := nested.JSONPath(); !ok || !got.Equal(path) {
			t.Fatal("text lookup lost literal path")
		}
		if _, ok := condition.JSONPath(); ok {
			t.Fatal("WithJSONPath mutated root")
		}
		if got, ok := nested.Value().String(); !ok || got != text {
			t.Fatal("text became JSON literal")
		}
	}
	for _, value := range []query.Value{query.Null(), query.JSON(jsonvalue.Null()), query.Boolean(true), query.Integer(1)} {
		if _, err := query.NewExpression(query.NewCondition(field, query.LookupIContains, value)); err == nil {
			t.Fatal("text lookup accepted non-string")
		}
	}
	for _, text := range []string{"\xff", strings.Repeat("a", jsonvalue.MaxStringBytes+1)} {
		if _, err := query.NewExpression(query.NewCondition(field, query.LookupIContains, query.String(text))); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal("invalid text was not rejected", err)
		}
	}
	if _, err := query.NewFieldCondition(field, query.LookupIContains, field); err == nil {
		t.Fatal("text lookup widened JSON F capability")
	}
}
