package query_test

import (
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

func TestJSONQueryValuesKeepDocumentTypeAndSnapshotOwnership(t *testing.T) {
	original := jsonvalue.Value{Text: ` {"value":340282366920938463463374607431768211455} `}
	value := query.JSON(original)
	original.Text = `false`
	_, stringKind := value.String()
	if value.Kind() != query.ValueJSON || value.IsNull() || stringKind || value.Equal(query.String(`{"value":340282366920938463463374607431768211455}`)) {
		t.Fatal("JSON query value was coerced into another scalar kind")
	}
	decoded, ok := value.JSON()
	if !ok || decoded.Text != `{"value":340282366920938463463374607431768211455}` {
		t.Fatal("JSON query lost exact digits or snapshot ownership")
	}
	database, err := value.DatabaseValue()
	if typed, ok := database.(jsonvalue.Value); err != nil || !ok || typed != decoded {
		t.Fatal("backend did not receive an explicitly typed JSON document", err)
	}
	decoded.Text = `null`
	again, ok := value.JSON()
	if !ok || again.Text == decoded.Text {
		t.Fatal("JSON getter exposed mutable query state")
	}
	null := query.JSON(jsonvalue.Null())
	if null.IsNull() || null.Equal(query.Null()) {
		t.Fatal("JSON null collapsed into SQL NULL")
	}
	if invalid := query.JSON(jsonvalue.Value{}); invalid != (query.Value{}) {
		t.Fatal("invalid JSON acquired a valid query tag")
	}
}

func TestJSONPredicatesValidateKindAndLiteralComparisons(t *testing.T) {
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	other := query.NewFieldRef("mirror", "mirror", query.FieldJSON, false)
	value := query.JSON(jsonvalue.Value{Text: `{"a":1}`})
	if !field.ValidType() {
		t.Fatal("JSON field is not a supported metadata kind")
	}
	for _, condition := range []query.Condition{
		query.NewCondition(field, query.LookupExact, value),
		query.NewCondition(field, query.LookupGreaterThan, value),
		query.NewCondition(field, query.LookupGreaterThanOrEqual, value),
		query.NewCondition(field, query.LookupLessThan, value),
		query.NewCondition(field, query.LookupLessThanOrEqual, value),
		query.NewCondition(field, query.LookupExact, query.JSON(jsonvalue.Null())),
		query.NewCondition(field, query.LookupIsNull, query.Boolean(true)),
	} {
		if _, err := query.NewExpression(condition); err != nil {
			t.Fatal(err)
		}
	}
	condition, err := query.NewFieldCondition(field, query.LookupExact, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewExpression(condition); err != nil {
		t.Fatal(err)
	}
	values := []query.Value{value, query.JSON(jsonvalue.Null()), query.Null()}
	member, err := query.NewInCondition(field, values)
	if err != nil {
		t.Fatal(err)
	}
	values[0] = query.String("changed")
	owned, ok := member.Values()
	if !ok || !owned[0].Equal(value) {
		t.Fatal("JSON membership retained caller slice")
	}
	if _, err := query.NewExpression(member); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewProjectionResult(query.FieldResult(field), query.FieldResult(other)); err != nil {
		t.Fatal(err)
	}
	for _, condition := range []query.Condition{
		query.NewCondition(field, query.LookupExact, query.String(`{"a":1}`)),
		query.NewCondition(field, query.LookupExact, query.Null()),
		query.NewCondition(field, query.LookupGreaterThan, query.String("1")),
		query.NewCondition(field, query.LookupIContains, query.JSON(jsonvalue.Null())),
	} {
		if _, err := query.NewExpression(condition); err == nil {
			t.Fatal("unsupported or untyped JSON comparison accepted")
		}
	}
	if _, err := query.NewFieldCondition(field, query.LookupGreaterThan, other); err == nil {
		t.Fatal("JSON field order comparison accepted")
	}
	if _, err := query.NewFieldCondition(field, query.LookupExact, query.NewFieldRef("text", "text", query.FieldString, false)); err == nil {
		t.Fatal("JSON/string F comparison accepted")
	}
	if _, err := query.NewInCondition(field, []query.Value{query.JSON(jsonvalue.Value{})}); err == nil {
		t.Fatal("invalid JSON IN literal accepted")
	}
}

func TestJSONContainmentRequiresLiteralJSONAndRetainsPath(t *testing.T) {
	field := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	path, err := query.NewJSONPath(query.JSONKey("a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []query.Lookup{query.LookupContains, query.LookupContainedBy} {
		for _, value := range []query.Value{query.JSON(jsonvalue.Null()), query.JSON(jsonvalue.Value{Text: `{"a":[1,false,null]}`})} {
			base := query.NewCondition(field, lookup, value)
			if _, err := query.NewExpression(base); err != nil {
				t.Fatal(err)
			}
			condition, err := base.WithJSONPath(path)
			if err != nil {
				t.Fatal(err)
			}
			retained, ok := condition.JSONPath()
			if !ok || !retained.Equal(path) || condition.Lookup() != lookup || !condition.Value().Equal(value) {
				t.Fatal("containment lost its document/path")
			}
		}
		for _, value := range []query.Value{query.Null(), query.String("{}"), query.Boolean(false), query.Integer(1), query.JSON(jsonvalue.Value{})} {
			if _, err := query.NewExpression(query.NewCondition(field, lookup, value)); err == nil {
				t.Fatal("containment coerced non-JSON value")
			}
		}
		text := query.NewFieldRef("text", "text", query.FieldString, false)
		if _, err := query.NewExpression(query.NewCondition(text, lookup, query.String("x"))); err == nil {
			t.Fatal("JSON containment used string lookup semantics")
		}
		if _, err := query.NewFieldCondition(field, lookup, field); err == nil {
			t.Fatal("containment accepted unsupported F operand")
		}
	}
}
