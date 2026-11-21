package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func TestConflictInsertPlanOwnsAssignmentsAndTarget(t *testing.T) {
	owner := query.NewFieldRef("owner", "owner_id", query.FieldInteger, false)
	label := query.NewFieldRef("label", "label_id", query.FieldInteger, false)
	assignments := []query.Assignment{query.NewAssignment(owner, query.Integer(7)), query.NewAssignment(label, query.Integer(9))}
	target := []query.FieldRef{owner, label}
	plan := query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), target)
	same := query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), target)
	assignments[0] = query.NewAssignment(owner, query.Integer(99))
	target[0] = label
	plan.Assignments()[1] = assignments[0]
	plan.Target()[1] = owner
	plan.Insert().Assignments()[0] = assignments[0]
	if !plan.Equal(same) || plan.Table() != "links" {
		t.Fatal("constructor or accessor aliases plan state")
	}
	for _, changed := range []query.ConflictInsertPlan{
		query.NewConflictInsertPlan(query.NewInsertPlan("links", same.Assignments()), same.Target()),
		query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", same.Assignments(), query.NewFieldRef("key", "link_key", query.FieldInteger, false)), same.Target()),
		{}, query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("other", same.Assignments(), query.NewFieldRef("id", "id", query.FieldInteger, false)), same.Target()),
		query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", assignments, query.NewFieldRef("id", "id", query.FieldInteger, false)), same.Target()),
		query.NewConflictInsertPlan(query.NewInsertPlanReturningKey("links", same.Assignments(), query.NewFieldRef("id", "id", query.FieldInteger, false)), []query.FieldRef{label, owner}),
	} {
		if plan.Equal(changed) {
			t.Fatal("plan equality omitted table, assignments, returning key or ordered target")
		}
	}
}
