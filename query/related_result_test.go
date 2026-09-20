package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestRelatedFieldProjectionPreservesExactTargetAndRootAuthority(t *testing.T) {
	root := ir.ModelIdentity{AppLabel: "app", ModelName: "entry"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "datum"}
	fk := query.NewFieldRef("datum", "datum_id", query.FieldInteger, true)
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	amount := query.NewDecimalFieldRef("amount", "amount", false, 30, 6)
	payload := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	selected := make([]query.ResultExpression, 0, 3)
	for _, field := range []query.FieldRef{id, amount, payload} {
		path, err := query.NewForwardRelationPath(root, "app_entry", "datum", "datum_id", target, "app_datum", "id", true, field)
		if err != nil {
			t.Fatal(err)
		}
		value, err := query.RelatedFieldResult(path)
		if err != nil {
			t.Fatal(err)
		}
		actual, ok := value.Field()
		if !ok || !actual.Equal(field) || value.Kind() != query.ResultField {
			t.Fatal("related selection changed terminal metadata")
		}
		if _, ok := value.JSONPath(); ok {
			t.Fatal("whole target field acquired a JSON path")
		}
		if route, ok := value.RelationPath(); !ok || !route.Equal(path) {
			t.Fatal("whole target field lost its bound route")
		}
		if value.Equal(query.FieldResult(field)) {
			t.Fatal("root and target field identity collapsed")
		}
		same, err := query.RelatedFieldResult(path)
		if err != nil || !same.Equal(value) {
			t.Fatal("repeated route binding changed identity", err)
		}
		if _, err := query.NewProjectionResult(value, same); err == nil {
			t.Fatal("duplicate whole target field accepted")
		}
		if _, err := query.NewAggregateResult(value); err == nil {
			t.Fatal("related scalar selection widened aggregate support")
		}
		selected = append(selected, value)
	}
	path, _ := selected[2].RelationPath()
	key, _ := query.NewJSONPath(query.JSONKey("nested"))
	nested, err := query.RelatedJSONPathResult(path, key)
	if err != nil {
		t.Fatal(err)
	}
	// Root ID, target ID, whole JSON and a path of that JSON remain separate
	// selected cells, while the plan's source authority stays root-only.
	shape, err := query.NewProjectionResult(append(selected, query.FieldResult(id), nested)...)
	if err != nil || !shape.HasRelations() {
		t.Fatal("independent selected expressions collided", err)
	}
	base := query.NewPlan("app_entry", []query.FieldRef{id, fk})
	plan, err := base.WithResultShape(shape)
	if err != nil || len(plan.SourceFields()) != 2 || !plan.SourceFields()[1].Equal(fk) {
		t.Fatal("target projection rewrote root fields", err)
	}
	for _, candidate := range []query.Plan{
		query.NewPlan("foreign_entry", base.SourceFields()),
		query.NewPlan("app_entry", []query.FieldRef{id}),
		query.NewPlan("app_entry", []query.FieldRef{id, query.NewFieldRef("datum", "datum_id", query.FieldInteger, false)}),
	} {
		if _, err := candidate.WithResultShape(shape); err == nil {
			t.Fatal("whole target projection bypassed root provenance")
		}
	}
}
