package query

import (
	"errors"
	"testing"
)

func graphTestInsert(table string, key FieldRef, value int64) InsertPlan {
	return NewInsertPlanReturningKey(table, []Assignment{NewAssignment(key, Integer(value))}, key)
}

func TestInsertGraphPlanOwnsRowsAndPreservesSignedKeys(t *testing.T) {
	key := NewFieldRef("record", "record_key", FieldInteger, false)
	input := []InsertPlan{graphTestInsert("a", key, 0), graphTestInsert("a", key, -3), graphTestInsert("b", key, 0)}
	plan, err := NewInsertGraphPlan(input)
	if err != nil || plan.RowCount() != 3 || plan.ValueCount() != 3 {
		t.Fatal(plan, err)
	}
	copy, err := NewInsertGraphPlan(input)
	if err != nil || !plan.Equal(copy) {
		t.Fatal("equal independently constructed graphs differ", err)
	}
	input[0] = InsertPlan{}
	returned := plan.Inserts()
	returned[1] = InsertPlan{}
	assignments := plan.Inserts()[2].Assignments()
	assignments[0] = NewAssignment(key, Integer(99))
	if err := plan.Validate(); err != nil || !plan.Equal(copy) {
		t.Fatal("caller mutated graph storage", err)
	}
	changed, err := NewInsertGraphPlan([]InsertPlan{graphTestInsert("a", key, -1)})
	if err != nil || plan.Equal(changed) || changed.Equal(plan) {
		t.Fatal("graph equality lost row content", err)
	}
}

func TestInsertGraphPlanRejectsIncompleteOrAmbiguousRows(t *testing.T) {
	key := NewFieldRef("record", "record_key", FieldInteger, false)
	other := NewFieldRef("renamed", "record_key", FieldInteger, false)
	text := NewFieldRef("token", "token", FieldString, false)
	for _, test := range []struct {
		name string
		rows []InsertPlan
	}{
		{"empty", nil},
		{"zero_row", []InsertPlan{{}}},
		{"missing_table", []InsertPlan{graphTestInsert("", key, 1)}},
		{"no_returning_key", []InsertPlan{NewInsertPlan("a", []Assignment{NewAssignment(key, Integer(1))})}},
		{"omitted_key", []InsertPlan{NewInsertPlanReturningKey("a", []Assignment{NewAssignment(text, String("one"))}, key)}},
		{"null_key", []InsertPlan{NewInsertPlanReturningKey("a", []Assignment{NewAssignment(key, Null())}, key)}},
		{"noninteger_key", []InsertPlan{NewInsertPlanReturningKey("a", []Assignment{NewAssignment(key, String("1"))}, key)}},
		{"nullable_key", []InsertPlan{graphTestInsert("a", NewFieldRef("record", "record_key", FieldInteger, true), 1)}},
		{"wrong_key_type", []InsertPlan{graphTestInsert("a", text, 1)}},
		{"duplicate_identity", []InsertPlan{graphTestInsert("a", key, 0), graphTestInsert("a", key, 0)}},
		{"inconsistent_key_metadata", []InsertPlan{graphTestInsert("a", key, 1), graphTestInsert("a", other, 2)}},
		{"aliased_key", []InsertPlan{NewInsertPlanReturningKey("a", []Assignment{NewAssignment(other, Integer(1))}, key)}},
		{"repeated_column", []InsertPlan{NewInsertPlanReturningKey("a", []Assignment{NewAssignment(key, Integer(1)), NewAssignment(key, Integer(2))}, key)}},
		{"too_many_rows", make([]InsertPlan, MaximumInsertGraphRows+1)},
		{"too_many_values", []InsertPlan{NewInsertPlanReturningKey("a", make([]Assignment, MaximumBulkValues+1), key)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := NewInsertGraphPlan(test.rows)
			if !errors.Is(err, &Error{Code: CodeInvalidPlan}) || plan.RowCount() != 0 || plan.ValueCount() != 0 {
				t.Fatal("invalid graph retained rows or succeeded", plan, err)
			}
		})
	}
	if err := (InsertGraphPlan{}).Validate(); !errors.Is(err, &Error{Code: CodeInvalidPlan}) {
		t.Fatal("zero-value plan validated", err)
	}
}
