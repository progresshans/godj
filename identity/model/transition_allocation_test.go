package model

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

type allocationTestScope struct {
	db.RelationSession
	failure error
}

func (scope *allocationTestScope) ValidateSession(ctx context.Context) error {
	return errors.Join(ctx.Err(), scope.failure)
}

func TestTransitionAllocationJoinsNamespacesWithoutPartialKeys(t *testing.T) {
	for _, mode := range []string{"complete", "source_revision", "callback_failure", "duplicate_key", "expired_scope", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scope := &allocationTestScope{}
			failure := errors.New("allocation boundary failure")
			references := transitionTargetMappings{
				TransitionReferenceAccount:    {revisions: map[int64]int64{-7: 1, 0: 2}},
				TransitionReferenceGroup:      {revisions: map[int64]int64{0: 3}},
				TransitionReferencePermission: {revisions: map[int64]int64{0: 4}},
			}
			identities := []transitionReferenceIdentity{{TransitionReferenceAccount, 0}, {TransitionReferencePermission, 0}, {TransitionReferenceGroup, 0}, {TransitionReferenceAccount, -7}}
			native := []int64{-5, 0, -4, 0}
			var calls []int
			nodes := make([]transitionAllocation, len(identities))
			for i, identity := range identities {
				nodes[i].reference = transitionReferenceNode{kind: identity.kind, source: SourceAccount{ID: identity.id, Revision: references[identity.kind].revisions[identity.id]}}
				if i+1 < len(nodes) {
					next := identities[i+1]
					nodes[i].reference.references = map[string]TransitionTargetReference{"dependency": {Kind: next.kind, Source: SourceAccount{ID: next.id, Revision: references[next.kind].revisions[next.id]}}}
				}
				nodes[i].insert = func(ctx context.Context, actualScope db.RelationSession, _ auth.Principal, allocated transitionTargetMappings) (int64, error) {
					if actualScope != scope || ctx.Err() != nil {
						t.Fatal("allocation escaped its live borrowed scope")
					}
					if i+1 < len(nodes) {
						next := identities[i+1]
						key, present := allocated[next.kind].targets[next.id]
						if !present || key != native[i+1] {
							t.Fatal("dependency used a source key, zero default or another namespace")
						}
					}
					calls = append(calls, i)
					if mode == "callback_failure" && i == 2 {
						return 0, failure
					}
					if mode == "expired_scope" && i == 2 {
						scope.failure = failure
					}
					if mode == "duplicate_key" && i == 0 {
						return 0, nil
					}
					return native[i], nil
				}
			}
			if mode == "source_revision" {
				nodes[0].reference.source.Revision++
			}
			if mode == "cancelled" {
				cancel()
			}
			actual, err := applyTransitionAllocations(ctx, scope, auth.Principal{}, references, nodes)
			for _, namespace := range references {
				if namespace.targets != nil {
					t.Fatal("allocation mutated the reviewed namespace")
				}
			}
			if mode != "complete" {
				if actual != nil || err == nil {
					t.Fatal("failed allocation published a partial key map", actual, err)
				}
				if (mode == "source_revision" || mode == "cancelled") && len(calls) != 0 {
					t.Fatal("invalid observation reached an insertion")
				}
				if (mode == "callback_failure" || mode == "expired_scope") && (!errors.Is(err, failure) || !slices.Equal(calls, []int{3, 2})) {
					t.Fatal("failed scope did not stop the dependent graph", calls, err)
				}
				return
			}
			if err != nil || !slices.Equal(calls, []int{3, 2, 1, 0}) {
				t.Fatal("joint namespace order is incomplete", calls, err)
			}
			for i, identity := range identities {
				if key, found := actual[identity.kind].targets[identity.id]; !found || key != native[i] {
					t.Fatal("actual native key was lost")
				}
			}
			actual[TransitionReferenceGroup].revisions[0] = 99
			if !reflect.DeepEqual(references[TransitionReferenceGroup].revisions, map[int64]int64{0: 3}) {
				t.Fatal("allocation result aliases reviewed revisions")
			}
		})
	}
}
