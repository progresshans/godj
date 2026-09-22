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
	plan := query.NewConflictInsertPlan("links", assignments, target)
	same := query.NewConflictInsertPlan("links", assignments, target)
	assignments[0] = query.NewAssignment(owner, query.Integer(99))
	target[0] = label
	plan.Assignments()[1] = assignments[0]
	plan.Target()[1] = owner
	if !plan.Equal(same) || plan.Table() != "links" {
		t.Fatal("constructor or accessor aliases plan state")
	}
	for _, changed := range []query.ConflictInsertPlan{
		{}, query.NewConflictInsertPlan("other", same.Assignments(), same.Target()),
		query.NewConflictInsertPlan("links", assignments, same.Target()),
		query.NewConflictInsertPlan("links", same.Assignments(), []query.FieldRef{label, owner}),
	} {
		if plan.Equal(changed) {
			t.Fatal("plan equality omitted table, assignments or ordered target")
		}
	}
}
