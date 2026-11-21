package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func TestAutoKeyPlanPreservesDeclaredAllocatorAndBounds(t *testing.T) {
	key := query.NewFieldRef("id", "record_key", query.FieldInteger, false)
	plan, err := query.NewAutoKeyPlan("records", key, query.MaximumAutoKeyReservations)
	if err != nil || plan.Validate() != nil || plan.Table() != "records" || !plan.Key().Equal(key) || plan.Count() != query.MaximumAutoKeyReservations {
		t.Fatal("bounded allocator intent was not preserved", err)
	}
	same, err := query.NewAutoKeyPlan("records", key, query.MaximumAutoKeyReservations)
	if err != nil || !plan.Equal(same) {
		t.Fatal("equal allocator plans differ", err)
	}
	for _, fields := range []struct {
		table string
		key   query.FieldRef
		count int
	}{
		{"other", key, plan.Count()},
		{"records", query.NewFieldRef("id", "other_key", query.FieldInteger, false), plan.Count()},
		{"records", key, 1},
	} {
		other, err := query.NewAutoKeyPlan(fields.table, fields.key, fields.count)
		if err != nil || plan.Equal(other) {
			t.Fatal("allocator plan lost table, key or count identity", err)
		}
	}
	for _, fields := range []struct {
		table string
		key   query.FieldRef
		count int
	}{
		{"", key, 1}, {"records", query.FieldRef{}, 1},
		{"records", query.NewFieldRef("", "id", query.FieldInteger, false), 1},
		{"records", query.NewFieldRef("id", "", query.FieldInteger, false), 1},
		{"records", query.NewFieldRef("id", "id", query.FieldInteger, true), 1},
		{"records", query.NewFieldRef("id", "id", query.FieldString, false), 1},
		{"records", key, 0}, {"records", key, -1}, {"records", key, query.MaximumAutoKeyReservations + 1},
	} {
		if actual, err := query.NewAutoKeyPlan(fields.table, fields.key, fields.count); err == nil || !actual.Equal(query.AutoKeyPlan{}) {
			t.Fatal("invalid allocator intent was accepted")
		}
	}
	if (query.AutoKeyPlan{}).Validate() == nil {
		t.Fatal("zero allocator plan was executable")
	}
}
