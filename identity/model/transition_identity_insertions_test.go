package model

import (
	"maps"
	"reflect"
	"testing"

	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTransitionIdentityInventoryOwnsRowsAndRejectsPartialPublication(t *testing.T) {
	project, err := orm.BindProject(models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	row := orm.CollectionInsertedRow{Model: ir.ModelIdentity{AppLabel: "godj_identity", ModelName: "group"}, Key: 0, Values: map[string]query.Value{"id": query.Integer(0), "name": query.String("group"), "revision": query.Integer(1)}}
	var owned transitionInsertions
	if err := owned.addRows(project, []orm.CollectionInsertedRow{row}); err != nil {
		t.Fatal(err)
	}
	row.Values["name"] = query.String("caller-mutated")
	if value, _ := owned.rows[0].Values["name"].String(); value != "group" {
		t.Fatal("identity inventory retained caller values")
	}
	for _, mode := range []string{"duplicate_key", "unknown_model", "missing_key", "wrong_key", "budget"} {
		t.Run(mode, func(t *testing.T) {
			state := owned
			next := row
			next.Values = maps.Clone(row.Values)
			next.Key, next.Values["id"] = -7, query.Integer(-7)
			switch mode {
			case "duplicate_key":
				next.Key, next.Values["id"] = 0, query.Integer(0)
			case "unknown_model":
				next.Model.ModelName = "absent"
			case "missing_key":
				delete(next.Values, "id")
			case "wrong_key":
				next.Values["id"] = query.Integer(8)
			case "budget":
				state.bytes = MaximumTransitionStateBytes
			}
			before := state
			before.keys = maps.Clone(state.keys)
			first := row
			first.Values = maps.Clone(row.Values)
			first.Key, first.Values["id"] = 12, query.Integer(12)
			if err := state.addRows(project, []orm.CollectionInsertedRow{first, next}); err == nil || !reflect.DeepEqual(state, before) {
				t.Fatal("rejected identity record published partial state", err)
			}
		})
	}
}
