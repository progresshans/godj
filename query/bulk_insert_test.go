package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestBulkInsertOwnsItsInputMatrixAndConflictPolicy(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	target, update := []query.FieldRef{name}, []query.FieldRef{amount}
	policy, err := query.NewBulkConflict(query.BulkConflictUpdate, target, update)
	if err != nil {
		t.Fatal(err)
	}
	fields := []query.FieldRef{name, amount}
	rows := [][]query.Value{{query.String("one"), query.Integer(1)}, {query.String("two"), query.Integer(2)}}
	plan, err := query.NewBulkInsertPlan("items", fields, rows, id, policy)
	if err != nil {
		t.Fatal(err)
	}
	same, err := query.NewBulkInsertPlan("items", fields, rows, id, policy)
	if err != nil {
		t.Fatal(err)
	}
	fields[0], target[0], update[0] = id, id, id
	rows[0][0] = query.String("outside")
	rows[1] = nil
	plan.Rows()[0][1] = query.Integer(99)
	plan.Fields()[0] = id
	plan.Conflict().Target()[0] = id
	plan.Conflict().Update()[0] = id
	if !plan.Equal(same) || plan.RowCount() != 2 || plan.ValueCount() != 4 || !plan.ReturnsKeys() {
		t.Fatal("bulk AST aliases input or accessor containers")
	}
	changed, err := query.NewBulkInsertPlan("other", same.Fields(), same.Rows(), id, policy)
	if err != nil || same.Equal(changed) {
		t.Fatal("table identity lost", err)
	}
	for _, mode := range []query.BulkConflictMode{query.BulkConflictError, query.BulkConflictIgnore} {
		policy, err := query.NewBulkConflict(mode, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := query.NewBulkInsertPlan("auto_only", nil, [][]query.Value{nil, nil}, id, policy)
		if err != nil || plan.RowCount() != 2 || plan.ValueCount() != 0 || plan.ReturnsKeys() != (mode != query.BulkConflictIgnore) {
			t.Fatal("auto-only/ignore contract", plan, err)
		}
	}
}

func TestBulkInsertRejectsInvalidShapeAndFieldProvenance(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	other := query.NewFieldRef("other", "other", query.FieldString, false)
	for _, test := range []struct {
		name   string
		fields []query.FieldRef
		rows   [][]query.Value
		key    query.FieldRef
	}{
		{"no_rows", []query.FieldRef{name}, nil, id},
		{"wide_row", []query.FieldRef{name}, [][]query.Value{{query.String("x"), query.String("y")}}, id},
		{"short_row", []query.FieldRef{name}, [][]query.Value{nil}, id},
		{"duplicate_column", []query.FieldRef{name, name}, [][]query.Value{{query.String("x"), query.String("y")}}, id},
		{"wrong_key_type", []query.FieldRef{name}, [][]query.Value{{query.String("x")}}, name},
		{"nullable_key", nil, [][]query.Value{nil}, query.NewFieldRef("id", "id", query.FieldInteger, true)},
		{"key_alias", []query.FieldRef{query.NewFieldRef("foreign", "id", query.FieldInteger, false)}, [][]query.Value{{query.Integer(1)}}, id},
		{"invalid_field", []query.FieldRef{{}}, [][]query.Value{{query.String("x")}}, id},
		{"row_bound", nil, make([][]query.Value, query.MaximumBulkRows+1), id},
		{"value_bound", []query.FieldRef{name, other}, make([][]query.Value, query.MaximumBulkValues/2+1), id},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := query.NewBulkInsertPlan("items", test.fields, test.rows, test.key, query.BulkConflict{}); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("invalid matrix admitted", err)
			}
		})
	}
	if err := (query.BulkInsertPlan{}).Validate(); err == nil {
		t.Fatal("zero plan admitted")
	}
	for _, mode := range []query.BulkConflictMode{query.BulkConflictError, query.BulkConflictIgnore, "unknown"} {
		if _, err := query.NewBulkConflict(mode, []query.FieldRef{name}, []query.FieldRef{name}); err == nil {
			t.Fatal("foreign conflict fields or unknown policy admitted")
		}
	}
	for _, test := range []struct{ target, update []query.FieldRef }{
		{nil, []query.FieldRef{name}}, {[]query.FieldRef{name}, nil}, {[]query.FieldRef{name, name}, []query.FieldRef{name}},
	} {
		if _, err := query.NewBulkConflict(query.BulkConflictUpdate, test.target, test.update); err == nil {
			t.Fatal("invalid conflict update admitted")
		}
	}
	for _, test := range []struct{ target, update []query.FieldRef }{
		{[]query.FieldRef{other}, []query.FieldRef{name}}, {[]query.FieldRef{name}, []query.FieldRef{other}},
		{[]query.FieldRef{name}, []query.FieldRef{id}},
	} {
		policy, err := query.NewBulkConflict(query.BulkConflictUpdate, test.target, test.update)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := query.NewBulkInsertPlan("items", []query.FieldRef{name}, [][]query.Value{{query.String("one")}}, id, policy); err == nil {
			t.Fatal("conflict columns escaped the declared matrix")
		}
	}
}
