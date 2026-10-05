package query

import (
	"errors"
	"testing"
)

func TestBulkUpdatePreservesFilterScopesAndOwnsSelectedValues(t *testing.T) {
	id := NewFieldRef("id", "id", FieldInteger, false)
	amount := NewFieldRef("amount", "amount", FieldInteger, false)
	name := NewFieldRef("name", "name", FieldString, true)
	path := collectionRouteFixture(t, name)
	source := NewPlan("owners", []FieldRef{id, amount})
	for _, name := range []string{"first", "second"} {
		var err error
		source, err = source.WithConditions(NewRelatedCondition(path, LookupExact, String(name)))
		if err != nil {
			t.Fatal(err)
		}
	}
	where, _ := source.Where()
	source = source.WithDistinct().WithOrderings(NewOrdering(amount, Descending))
	lock, err := NewRowLock(LockForUpdate, LockNoWait)
	if err != nil {
		t.Fatal(err)
	}
	source, err = source.WithRowLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	fields := []FieldRef{amount}
	spec, err := NewBulkUpdateSpec(source, fields, id)
	if err != nil {
		t.Fatal(err)
	}
	predicate, _ := spec.Selection().Where()
	if !predicate.Equal(where) || spec.Selection().Distinct() || len(spec.Selection().Orderings()) != 0 || spec.Selection().ResultShape().Kind() != ResultProjection {
		t.Fatal("write source changed a collection-filter identity or kept a read result")
	}
	if _, locked := spec.Selection().RowLock(); locked {
		t.Fatal("SELECT row lock reached UPDATE membership")
	}
	if !source.Distinct() || len(source.Orderings()) != 1 {
		t.Fatal("write preparation changed the caller plan")
	}
	fields[0] = name
	keys, rows := []int64{0, 9007199254740993, 0}, [][]Value{{Integer(1)}, {Integer(2)}, {Integer(3)}}
	plan, err := NewBulkUpdatePlan(spec, keys, rows)
	if err != nil {
		t.Fatal(err)
	}
	keys[0], rows[0][0] = 99, Integer(99)
	ownedKeys, ownedRows, ownedFields := plan.Keys(), plan.Rows(), plan.Spec().Fields()
	ownedKeys[1], ownedRows[1][0], ownedFields[0] = -9, Integer(9), name
	if plan.Keys()[0] != 0 || plan.Keys()[1] != 9007199254740993 || !plan.Rows()[0][0].Equal(Integer(1)) || !plan.Rows()[1][0].Equal(Integer(2)) || plan.Spec().Fields()[0] != amount || plan.ValueCount() != 6 {
		t.Fatal("bulk update aliases caller input or loses exact/repeated keys")
	}
	copy, err := NewBulkUpdatePlan(spec, plan.Keys(), plan.Rows())
	if err != nil || !copy.Equal(plan) {
		t.Fatal("bulk update structural equality", err)
	}
}

func TestBulkUpdateRejectsWrongShapeAndMutationProvenance(t *testing.T) {
	id := NewFieldRef("id", "id", FieldInteger, false)
	amount := NewFieldRef("amount", "amount", FieldInteger, false)
	source := NewPlan("items", []FieldRef{id, amount})
	for _, fields := range [][]FieldRef{nil, {id}, {amount, amount}, {NewFieldRef("amount", "foreign", FieldInteger, false)}, {NewFieldRef("amount", "amount", FieldInteger, true)}} {
		if _, err := NewBulkUpdateSpec(source, fields, id); err == nil {
			t.Fatal("invalid selected field admitted", fields)
		}
	}
	for _, key := range []FieldRef{{}, amount, NewFieldRef("id", "id", FieldInteger, true)} {
		if _, err := NewBulkUpdateSpec(source, []FieldRef{amount}, key); err == nil {
			t.Fatal("invalid primary key provenance admitted", key)
		}
	}
	for _, plan := range []Plan{NewPlan("", []FieldRef{id, amount}), NewPlan("items", []FieldRef{id, amount, amount}), NewPlan("items", []FieldRef{amount})} {
		if _, err := NewBulkUpdateSpec(plan, []FieldRef{amount}, id); err == nil {
			t.Fatal("invalid source admitted")
		}
	}
	for _, limit := range []int{0, 1} {
		sliced, _ := source.WithLimit(limit)
		if _, err := NewBulkUpdateSpec(sliced, []FieldRef{amount}, id); !errors.Is(err, &Error{Code: CodeUnsupported}) {
			t.Fatal("sliced mutation accepted", err)
		}
	}
	offset, _ := source.WithOffset(1)
	if _, err := NewBulkUpdateSpec(offset, []FieldRef{amount}, id); err == nil {
		t.Fatal("offset mutation accepted")
	}
	offset, _ = source.WithOffset(0)
	if _, err := NewBulkUpdateSpec(offset, []FieldRef{amount}, id); err != nil {
		t.Fatal("zero offset narrowed an otherwise unsliced query", err)
	}
	spec, err := NewBulkUpdateSpec(source, []FieldRef{amount}, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		keys []int64
		rows [][]Value
	}{{}, {[]int64{1}, nil}, {[]int64{1}, [][]Value{{}}}, {[]int64{1}, [][]Value{{Integer(1), Integer(2)}}}, {make([]int64, MaximumBulkValues/2+1), make([][]Value, MaximumBulkValues/2+1)}} {
		if _, err := NewBulkUpdatePlan(spec, test.keys, test.rows); err == nil {
			t.Fatal("invalid matrix accepted")
		}
	}
	if (BulkUpdateSpec{}).Validate() == nil || (BulkUpdatePlan{}).Validate() == nil {
		t.Fatal("zero write plans admitted")
	}
}
