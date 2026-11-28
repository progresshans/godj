package postgres

import (
	"errors"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"reflect"
	"strings"
	"testing"
)

func TestJSONOrderingCompilerBindsExactSelectedPathAndHiddenModelValues(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	payload := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	key, _ := query.NewJSONPath(query.JSONKey("a\"'"), query.JSONIndex(0))
	expression, err := query.JSONPathResult(payload, key)
	if err != nil {
		t.Fatal(err)
	}
	ordering, err := query.NewResultOrdering(expression, query.Descending)
	if err != nil {
		t.Fatal(err)
	}
	base, err := query.NewPlan("records", []query.FieldRef{id, payload}).WithConditions(query.NewCondition(id, query.LookupGreaterThan, query.Integer(10)))
	if err != nil {
		t.Fatal(err)
	}
	base = base.WithOrderings(ordering, query.NewOrdering(id, query.Ascending))
	shape, err := query.NewProjectionResult(query.FieldResult(id), expression)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := base.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	for _, distinct := range []bool{false, true} {
		for _, model := range []bool{false, true} {
			plan := projected
			if model {
				plan = base
			}
			if distinct {
				plan = plan.WithDistinct()
			}
			plan, err = plan.WithLimit(3)
			if err != nil {
				t.Fatal(err)
			}
			plan, err = plan.WithOffset(1)
			if err != nil {
				t.Fatal(err)
			}
			statement, args, err := compilePlan("jsonref", plan)
			if err != nil {
				t.Fatal(model, distinct, err)
			}
			var want []any
			path := "strict " + jsonPathArgument(key)
			if model && !distinct {
				want = []any{int64(10), path, int64(3), int64(1)}
			} else if distinct {
				want = []any{path, int64(10), int64(3), int64(1)}
			} else {
				want = []any{path, int64(10), path, int64(3), int64(1)}
			}
			if !model && distinct && !strings.Contains(statement, `ORDER BY 2 DESC, "id" ASC LIMIT $3 OFFSET $4`) {
				t.Fatal("DISTINCT path was rebound", statement)
			}
			if !reflect.DeepEqual(args, want) {
				t.Fatal("ordering parameter positions", model, distinct, statement, args, want)
			}
			if strings.Contains(statement, `a"'`) {
				t.Fatal("literal path interpolated into SQL")
			}
			if model && distinct && (!strings.HasPrefix(statement, `SELECT "godj_order_source"."c0" AS "id", "godj_order_source"."c1" AS "payload" FROM (`) || !strings.Contains(statement, `ORDER BY "godj_order_source"."o0" DESC`)) {
				t.Fatal("hidden sort cell escaped row shape", statement)
			}
		}
	}
	// A relation target with identical field metadata is still a different value.
	fk := query.NewFieldRef("record", "record_id", query.FieldInteger, true)
	route, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "app", ModelName: "link"}, "links", "record", "record_id", ir.ModelIdentity{AppLabel: "app", ModelName: "record"}, "records", "id", true, id, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.RelatedFieldResult(route)
	if err != nil {
		t.Fatal(err)
	}
	shape, err = query.NewProjectionResult(target)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.NewPlan("links", []query.FieldRef{id, fk}).WithOrderings(query.NewOrdering(id, query.Ascending)).WithDistinct().WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := compilePlan("jsonref", plan); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("target ID satisfied root DISTINCT ordering", err)
	}
}
