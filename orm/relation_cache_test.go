package orm

import (
	"errors"
	"sync"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestRelationCacheRejectsInvalidTuplesWithoutChangingState(t *testing.T) {
	target := 42
	wantError := &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}
	tests := []struct {
		name    string
		state   RelationCacheState
		target  *int
		pending bool
	}{
		{"unassigned pending", RelationUnassigned, nil, true},
		{"unassigned target", RelationUnassigned, &target, false},
		{"absent target", RelationAssignedAbsent, &target, false},
		{"absent pending", RelationAssignedAbsent, nil, true},
		{"present nil", RelationAssignedPresent, nil, false},
		{"unknown state", RelationCacheState(255), nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := NewRelationCache[int]()
			if err := cache.Store(RelationAssignedPresent, &target, true); err != nil {
				t.Fatal(err)
			}
			if err := cache.Store(test.state, test.target, test.pending); !errors.Is(err, wantError) {
				t.Fatalf("invalid store error = %v", err)
			}
			state, value, pending, err := cache.Snapshot()
			if err != nil || state != RelationAssignedPresent || value != &target || !pending {
				t.Fatalf("rejected store changed prior value: %v %p %v %v", state, value, pending, err)
			}
			// State corruption belongs to this runtime's tests now that generated
			// consumers cannot construct invalid private state tuples.
			corrupt := &RelationCache[int]{state: test.state, target: test.target, pending: test.pending}
			if _, _, _, err := corrupt.Snapshot(); !errors.Is(err, wantError) {
				t.Fatalf("corrupt snapshot error = %v", err)
			}
			if _, err := corrupt.Clone(); !errors.Is(err, wantError) {
				t.Fatalf("corrupt clone error = %v", err)
			}
		})
	}
	var absent *RelationCache[int]
	if _, _, _, err := absent.Snapshot(); !errors.Is(err, wantError) {
		t.Fatalf("nil snapshot error = %v", err)
	}
	if err := absent.Store(RelationUnassigned, nil, false); !errors.Is(err, wantError) {
		t.Fatalf("nil store error = %v", err)
	}
}

func TestRelationCacheCloneHasIndependentCellAndSharedTargetIdentity(t *testing.T) {
	target := 42
	original := NewRelationCache[int]()
	if err := original.Store(RelationAssignedPresent, &target, true); err != nil {
		t.Fatal(err)
	}
	clone, err := original.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if clone == original {
		t.Fatal("clone reused the source cell")
	}
	state, value, pending, err := clone.Snapshot()
	if err != nil || state != RelationAssignedPresent || value != &target || !pending {
		t.Fatalf("clone lost assignment identity: %v %p %v %v", state, value, pending, err)
	}
	if err := clone.Store(RelationAssignedAbsent, nil, false); err != nil {
		t.Fatal(err)
	}
	state, value, pending, err = original.Snapshot()
	if err != nil || state != RelationAssignedPresent || value != &target || !pending {
		t.Fatalf("clone update mutated original: %v %p %v %v", state, value, pending, err)
	}
}

func TestRelationCacheConcurrentSnapshotsPreserveWholeTuples(t *testing.T) {
	target := 42
	cache := NewRelationCache[int]()
	var writers sync.WaitGroup
	for index := 0; index < 4; index++ {
		writers.Go(func() {
			for iteration := 0; iteration < 100; iteration++ {
				if err := cache.Store(RelationAssignedPresent, &target, true); err != nil {
					t.Error(err)
				}
				if _, _, _, err := cache.Snapshot(); err != nil {
					t.Error(err)
				}
				if err := cache.Store(RelationAssignedAbsent, nil, false); err != nil {
					t.Error(err)
				}
			}
		})
	}
	writers.Wait()
}
