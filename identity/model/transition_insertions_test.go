package model

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTransitionInsertionsProjectOnePhysicalRowIntoEveryNullableRole(t *testing.T) {
	bridge := ir.ModelIdentity{AppLabel: "host", ModelName: "bridge"}
	field := func(name string) query.FieldRef {
		return query.NewFieldRef(name, name+"_column", query.FieldInteger, true)
	}
	key := query.NewFieldRef("row_key", "physical_key", query.FieldInteger, false)
	membership := func(source, target string) deletionMembership {
		return deletionMembership{through: bridge, key: key, source: field(source), target: field(target)}
	}
	rows := []orm.CollectionInsertedRow{
		{Model: bridge, Key: 0, Values: map[string]query.Value{"row_key": query.Integer(0), "old_group": query.Null(), "old_permission": query.Null(), "group": query.Integer(-7), "permission": query.Integer(0), "member": query.Null(), "payload": query.String("grant")}},
		{Model: bridge, Key: -5, Values: map[string]query.Value{"row_key": query.Integer(-5), "old_group": query.Null(), "old_permission": query.Null(), "group": query.Null(), "permission": query.Integer(0), "member": query.Integer(8), "payload": query.String("direct")}},
		{Model: bridge, Key: 9007199254740993, Values: map[string]query.Value{"row_key": query.Integer(9007199254740993), "old_group": query.Null(), "old_permission": query.Null(), "group": query.Integer(-7), "permission": query.Null(), "member": query.Integer(8), "payload": query.String("group")}},
	}
	old := []deletionLink{{key: -10, source: 0, target: -3, sourceValid: true, targetValid: true}}
	empty := []deletionLink{{key: -10}}
	for _, test := range []struct {
		name       string
		membership deletionMembership
		before     []deletionLink
		want       []deletionLink
	}{
		{"old_grants", membership("old_group", "old_permission"), old, []deletionLink{old[0], {key: -5}, {key: 0}, {key: 9007199254740993}}},
		{"new_grants", membership("group", "permission"), empty, []deletionLink{empty[0], {key: -5, target: 0, targetValid: true}, {key: 0, source: -7, target: 0, sourceValid: true, targetValid: true}, {key: 9007199254740993, source: -7, sourceValid: true}}},
		{"new_groups", membership("member", "group"), empty, []deletionLink{empty[0], {key: -5, source: 8, sourceValid: true}, {key: 0, target: -7, targetValid: true}, {key: 9007199254740993, source: 8, target: -7, sourceValid: true, targetValid: true}}},
		{"new_direct", membership("member", "permission"), empty, []deletionLink{empty[0], {key: -5, source: 8, target: 0, sourceValid: true, targetValid: true}, {key: 0, target: 0, targetValid: true}, {key: 9007199254740993, source: 8, sourceValid: true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.before)
			actual, err := projectTransitionLinks(test.membership, test.before, rows)
			if err != nil || !slices.Equal(actual, test.want) {
				t.Fatal("exact physical identities or nullable endpoints differ", actual, err)
			}
			actual[0].key = 55
			if !slices.Equal(before, test.before) {
				t.Fatal("projected rows alias the reviewed inventory")
			}
		})
	}
	if value, ok := rows[0].Values["payload"].String(); !ok || value != "grant" {
		t.Fatal("projection changed the typed insertion record")
	}
}

func TestTransitionInsertionProjectionRejectsForgedKeysAndMissingNullableValues(t *testing.T) {
	bridge := ir.ModelIdentity{AppLabel: "host", ModelName: "bridge"}
	membership := deletionMembership{through: bridge, key: query.NewFieldRef("pk", "row_id", query.FieldInteger, false), source: query.NewFieldRef("left", "left_id", query.FieldInteger, true), target: query.NewFieldRef("right", "right_id", query.FieldInteger, true)}
	before := []deletionLink{{key: -7}}
	valid := orm.CollectionInsertedRow{Model: bridge, Key: 0, Values: map[string]query.Value{"pk": query.Integer(0), "left": query.Null(), "right": query.Integer(-3)}}
	for _, mode := range []string{"existing_key", "missing_key", "wrong_key", "null_key", "duplicate_key", "missing_left", "missing_right", "wrong_endpoint", "required_null"} {
		t.Run(mode, func(t *testing.T) {
			row := valid
			row.Values = maps.Clone(valid.Values)
			bound := membership
			switch mode {
			case "existing_key":
				row.Key, row.Values["pk"] = -7, query.Integer(-7)
			case "missing_key":
				delete(row.Values, "pk")
			case "wrong_key":
				row.Key = 1
			case "null_key":
				row.Values["pk"] = query.Null()
			case "missing_left":
				delete(row.Values, "left")
			case "missing_right":
				delete(row.Values, "right")
			case "wrong_endpoint":
				row.Values["left"] = query.String("0")
			case "required_null":
				bound.source = query.NewFieldRef("left", "left_id", query.FieldInteger, false)
			}
			rows := []orm.CollectionInsertedRow{row}
			if mode == "duplicate_key" {
				rows = append(rows, row)
			}
			if result, err := projectTransitionLinks(bound, before, rows); err == nil || result != nil {
				t.Fatal("invalid physical identity published a partial projection", err)
			}
		})
	}
	other := valid
	other.Model.ModelName = "other"
	actual, err := projectTransitionLinks(membership, before, []orm.CollectionInsertedRow{other, valid})
	if err != nil || !reflect.DeepEqual(actual, []deletionLink{{key: -7}, {key: 0, target: -3, targetValid: true}}) {
		t.Fatal("different physical models collided or a signed target was lost", err)
	}
}
