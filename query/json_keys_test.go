package query

import (
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
)

func TestJSONKeysOwnershipLimitsAndClosedOperand(t *testing.T) {
	input := []string{"", "0", "a\x00", "a\"\\😀", "a", "a"}
	keys, err := NewJSONKeyList(input...)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := NewJSONKeyList(input...)
	input[0] = "changed"
	copy := keys.Values()
	copy[1] = "changed"
	if !keys.Equal(same) || !slices.Equal(keys.Values(), []string{"", "0", "a\x00", "a\"\\😀", "a", "a"}) {
		t.Fatal("keys leaked mutable storage")
	}
	empty, err := NewJSONKeyList()
	if err != nil || !empty.Valid() || empty.Values() == nil || len(empty.Values()) != 0 || empty.Equal(JSONKeyList{}) {
		t.Fatal("empty list and invalid zero became equivalent")
	}
	if (JSONKeyList{}).Valid() || (JSONKeyList{}).Values() != nil || !(JSONKeyList{}).Equal(JSONKeyList{}) {
		t.Fatal("invalid zero list")
	}
	for _, input := range [][]string{{string([]byte{255})}, {strings.Repeat("a", MaxJSONKeyBytes), "a"}, make([]string, MaxJSONKeys+1)} {
		if got, err := NewJSONKeyList(input...); err == nil || got.Valid() {
			t.Fatal("invalid key list accepted")
		}
	}
	boundary := make([]string, MaxJSONKeys)
	boundary[0] = strings.Repeat("😀", MaxJSONKeyBytes/4)
	if _, err := NewJSONKeyList(boundary...); err != nil {
		t.Fatal(err)
	}
	field := NewFieldRef("payload", "payload", FieldJSON, true)
	path, _ := NewJSONPath(JSONKey("a"))
	for _, lookup := range []Lookup{LookupHasKey, LookupHasKeys, LookupHasAnyKeys} {
		condition, err := NewJSONKeysCondition(field, lookup, "a")
		if err != nil {
			t.Fatal(err)
		}
		withPath, err := condition.WithJSONPath(path)
		if err != nil || withPath.Equal(condition) {
			t.Fatal("key presence lost path", err)
		}
		owned, ok := withPath.JSONKeys()
		if !ok || !slices.Equal(owned.Values(), []string{"a"}) {
			t.Fatal("key operand lost")
		}
		plan, err := NewPlan("records", []FieldRef{field}).WithConditions(withPath)
		if err != nil || !plan.Conditions()[0].Equal(withPath) {
			t.Fatal("plan lost key presence", err)
		}
		if lookup != LookupHasKey {
			c, err := NewJSONKeysCondition(field, lookup)
			if err != nil {
				t.Fatal(err)
			}
			p, err := NewPlan("records", []FieldRef{field}).WithConditions(c)
			if err != nil || p.EmptyResult() {
				t.Fatal("empty key list became empty IN", err)
			}
		}
	}
	for _, tc := range []struct {
		field  FieldRef
		lookup Lookup
		keys   []string
	}{
		{field, LookupHasKey, nil}, {field, LookupHasKey, []string{"a", "b"}},
		{field, LookupExact, []string{"a"}}, {NewFieldRef("label", "label", FieldString, false), LookupHasKey, []string{"a"}},
	} {
		if _, err := NewJSONKeysCondition(tc.field, tc.lookup, tc.keys...); err == nil {
			t.Fatal("invalid key predicate accepted")
		}
	}
	one, _ := NewJSONKeyList("a")
	for _, rhs := range []conditionRHS{
		{kind: conditionRHSJSONKeys},
		{kind: conditionRHSJSONKeys, keys: one, value: String("a")},
		{kind: conditionRHSJSONKeys, keys: one, values: []Value{String("a")}},
		{kind: conditionRHSJSONKeys, keys: one, field: field},
		{kind: conditionRHSLiteral, value: JSON(jsonvalue.Null()), keys: one},
		{kind: conditionRHSList, values: []Value{JSON(jsonvalue.Null())}, keys: one},
		{kind: conditionRHSField, field: field, keys: one},
	} {
		lookup := LookupHasKey
		switch rhs.kind {
		case conditionRHSLiteral, conditionRHSField:
			lookup = LookupExact
		case conditionRHSList:
			lookup = LookupIn
		}
		if err := validateExpressionCondition(Condition{field: field, lookup: lookup, rhs: &rhs}); err == nil {
			t.Fatal("mixed or missing key operand accepted")
		}
	}
	a, _ := NewJSONKeysCondition(field, LookupHasKeys, "a", "b")
	for _, other := range [][]string{{"b", "a"}, {"a", "b", "a"}} {
		b, _ := NewJSONKeysCondition(field, LookupHasKeys, other...)
		if a.Equal(b) {
			t.Fatal("key order/duplicates removed from AST")
		}
	}
}
