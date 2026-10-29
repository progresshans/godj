package conflicttest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/query"
)

func CheckCompiler(t *testing.T, compile func(query.ConflictInsertPlan) (string, []any, error), want string) {
	t.Helper()
	plan := Plan(7, 9, 20)
	statement, arguments, err := compile(plan)
	if err != nil || statement != want || !reflect.DeepEqual(arguments, []any{int64(7), int64(9), int64(20)}) {
		t.Fatalf("compile = %q, %v, %v", statement, arguments, err)
	}
	// Nullability belongs to metadata; uniqueness here is for assigned values.
	for _, null := range []bool{false, true} {
		field := query.NewFieldRef("owner", "owner_id", query.FieldInteger, true)
		value := query.Integer(7)
		if null {
			value = query.Null()
		}
		sql, args, err := compile(query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", []query.Assignment{query.NewAssignment(field, value)}, query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{field}))
		if null {
			if sql != "" || args != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("NULL target accepted", sql, args, err)
			}
		} else if err != nil || sql == "" || !reflect.DeepEqual(args, []any{int64(7)}) {
			t.Fatal("non-NULL nullable tuple rejected", sql, args, err)
		}
	}
	assignments, target := plan.Assignments(), plan.Target()
	for _, test := range []struct {
		name string
		plan query.ConflictInsertPlan
	}{
		{"zero", query.ConflictInsertPlan{}},
		{"missing_returning", query.NewConflictInsertPlan(query.NewInsertPlan("links", assignments), target)},
		{"nullable_returning", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, true)), target)},
		{"string_returning", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldString, false)), target)},
		{"empty_returning_name", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("", "id", query.FieldInteger, false)), target)},
		{"nul_returning_name", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("bad\x00", "id", query.FieldInteger, false)), target)},
		{"empty_returning_column", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "", query.FieldInteger, false)), target)},
		{"alias_returning", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "owner_id", query.FieldInteger, false)), target)},
		{"table", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("bad\x00", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), target)},
		{"empty_target", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), nil)},
		{"empty_assignments", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", nil, query.NewFieldRef("id", "id", query.FieldInteger, false)), target)},
		{"partial_assignment", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments[:1], query.NewFieldRef("id", "id", query.FieldInteger, false)), target)},
		{"duplicate_target", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{target[0], target[0]})},
		{"duplicate_assignment", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", append(plan.Assignments(), assignments[0]), query.NewFieldRef("id", "id", query.FieldInteger, false)), target)},
		{"absent_target", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{query.NewFieldRef("missing", "missing", query.FieldInteger, false)})},
		{"forged_target", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{query.NewFieldRef("other", "owner_id", query.FieldInteger, false)})},
		{"wrong_value_type", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", []query.Assignment{query.NewAssignment(target[0], query.String("7"))}, query.NewFieldRef("id", "id", query.FieldInteger, false)), target[:1])},
		{"null_target", query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", []query.Assignment{query.NewAssignment(target[0], query.Null())}, query.NewFieldRef("id", "id", query.FieldInteger, false)), target[:1])},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, arguments, err := compile(test.plan)
			if statement != "" || arguments != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("invalid plan returned partial SQL or lost error", statement, arguments, err)
			}
		})
	}
	for _, field := range []query.FieldRef{
		query.NewFieldRef("", "owner_id", query.FieldInteger, false),
		query.NewFieldRef("bad\x00", "owner_id", query.FieldInteger, false),
		query.NewFieldRef("owner", "bad\x00", query.FieldInteger, false),
		query.NewFieldRef("owner", "", query.FieldInteger, false),
		query.NewFieldRef("owner", "owner_id", query.FieldKind("unknown"), false),
	} {
		statement, arguments, err := compile(query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", []query.Assignment{query.NewAssignment(field, query.Integer(7))}, query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{field}))
		if statement != "" || arguments != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("invalid field accepted", field, statement, arguments, err)
		}
	}
}
