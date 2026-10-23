package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestRowLockOwnsTargetsAndSurvivesRowTransforms(t *testing.T) {
	t.Parallel()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	key := query.NewFieldRef("category", "category_id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	path, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "locks", ModelName: "item"}, "items", "category", "category_id", ir.ModelIdentity{AppLabel: "locks", ModelName: "category"}, "categories", "id", false, name, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.LockRelated(path)
	if err != nil {
		t.Fatal(err)
	}
	input := []query.RowLockTarget{target, query.LockSelf(), target}
	lock, err := query.NewRowLock(query.LockForNoKeyUpdate, query.LockNoWait, input...)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = query.RowLockTarget{}
	returned := lock.Targets()
	if len(returned) != 2 || !returned[0].Self() || !returned[1].Equal(target) {
		t.Fatalf("canonical targets = %#v", returned)
	}
	returned[1] = query.RowLockTarget{}
	hops := target.Hops()
	hops[0] = query.RelationHop{}
	if !lock.Targets()[1].Equal(target) || target.Hops()[0].Field() != "category" {
		t.Fatal("lock exposes mutable route storage")
	}
	base := query.NewPlan("items", []query.FieldRef{id, key, name})
	locked, err := base.WithRowLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := base.RowLock(); present || locked.Equal(base) {
		t.Fatal("locking changed the source or is absent from equality")
	}
	if !locked.WithoutRowLock().Equal(base) {
		t.Fatal("unlocking changed unrelated plan state")
	}
	derived, err := locked.WithOrderings(query.NewOrdering(id, query.Ascending)).WithLimit(3)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := query.NewProjectionResult(query.FieldResult(name))
	if err != nil {
		t.Fatal(err)
	}
	derived, err = derived.WithResultShape(projection)
	if err != nil {
		t.Fatal(err)
	}
	got, present := derived.RowLock()
	if !present || !got.Equal(lock) {
		t.Fatal("projection or pagination erased explicit lock targets")
	}
	aggregate, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	count, err := derived.WithResultShape(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := count.RowLock(); present {
		t.Fatal("aggregation retained row locking")
	}
	if original, present := derived.RowLock(); !present || !original.Equal(lock) {
		t.Fatal("aggregation mutated its source")
	}
	if _, err := count.WithRowLock(lock); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("aggregate lock = %v", err)
	}
	for _, change := range []query.RowLock{
		mustRowLock(t, query.LockForUpdate, query.LockNoWait, lock.Targets()...),
		mustRowLock(t, query.LockForNoKeyUpdate, query.LockWait, lock.Targets()...),
		mustRowLock(t, query.LockForNoKeyUpdate, query.LockNoWait, query.LockSelf()),
	} {
		changed, err := base.WithRowLock(change)
		if err != nil || changed.Equal(locked) {
			t.Fatalf("lock equality omitted an option: %v", err)
		}
	}
}

func TestRowLockRejectsInvalidOptionsAndSourceProvenance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		strength query.LockStrength
		wait     query.LockWaitPolicy
		targets  []query.RowLockTarget
	}{
		{"zero_strength", "", query.LockWait, nil},
		{"unknown_strength", "share", query.LockWait, nil},
		{"zero_wait", query.LockForUpdate, "", nil},
		{"combined_wait", query.LockForUpdate, "nowait,skip_locked", nil},
		{"zero_target", query.LockForUpdate, query.LockWait, []query.RowLockTarget{{}}},
		{"too_many_targets", query.LockForUpdate, query.LockWait, make([]query.RowLockTarget, query.MaximumRelatedLockTargets+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := query.NewRowLock(test.strength, test.wait, test.targets...); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatalf("invalid lock = %v", err)
			}
		})
	}
	if _, err := query.LockRelated(query.RelationPath{}); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("zero route = %v", err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	root := ir.ModelIdentity{AppLabel: "locks", ModelName: "item"}
	category := ir.ModelIdentity{AppLabel: "locks", ModelName: "category"}
	path, err := query.NewForwardRelationPath(root, "items", "category", "category_id", category, "categories", "id", false, id, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	target, err := query.LockRelated(path)
	if err != nil {
		t.Fatal(err)
	}
	lock := mustRowLock(t, query.LockForUpdate, query.LockWait, target)
	for _, plan := range []query.Plan{query.NewPlan("other", []query.FieldRef{id}), query.NewPlan("items", []query.FieldRef{id}), query.NewPlan("items", []query.FieldRef{id, query.NewFieldRef("category", "category_id", query.FieldInteger, true)})} {
		if _, err := plan.WithRowLock(lock); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatalf("foreign source lock = %v", err)
		}
	}
	other, err := query.NewForwardRelationPath(root, "items", "category", "category_id", category, "wrong_table", "id", false, id, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := query.LockRelated(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewRowLock(query.LockForUpdate, query.LockWait, target, conflict); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("conflicting routes = %v", err)
	}
	reverse, err := query.NewReverseRelationPath(root, "items", "category", "category_id", category, "categories", "id", "items", false, id, ir.RelationOneToMany)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.LockRelated(reverse); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatalf("collection lock target = %v", err)
	}
}

func mustRowLock(t *testing.T, strength query.LockStrength, wait query.LockWaitPolicy, targets ...query.RowLockTarget) query.RowLock {
	t.Helper()
	lock, err := query.NewRowLock(strength, wait, targets...)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}
