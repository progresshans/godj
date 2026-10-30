package model

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type allocationTestScope struct {
	db.RelationSession
	failure error
}

func TestTransitionAllocationFinalizesOnlyAfterAllNativeKeys(t *testing.T) {
	for _, mode := range []string{"complete", "missing_finish", "insert_failure", "finish_failure", "expired_finish", "cancelled_finish", "expired_after_finish"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			scope := &allocationTestScope{}
			failure := errors.New("staged allocation boundary failure")
			refs := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{-7: 1}}, TransitionReferenceGroup: {revisions: map[int64]int64{0: 2}}}
			identities := []transitionReferenceIdentity{{TransitionReferenceAccount, -7}, {TransitionReferenceGroup, 0}}
			keys := []int64{0, -9}
			var calls []string
			nodes := make([]transitionAllocation, 2)
			for i, id := range identities {
				nodes[i].values = func() map[string]query.Value { return map[string]query.Value{"id": query.Integer(keys[i])} }
				next := identities[1-i]
				nodes[i].reference = transitionReferenceNode{kind: id.kind, source: SourceAccount{ID: id.id, Revision: refs[id.kind].revisions[id.id]},
					references: map[string]TransitionTargetReference{"peer": {Kind: next.kind, Source: SourceAccount{ID: next.id, Revision: refs[next.kind].revisions[next.id]}}}, nullable: map[string]bool{"peer": true}}
				nodes[i].insert = func(_ context.Context, actual db.RelationSession, _ auth.Principal, allocated transitionTargetMappings, deferred map[string]bool) (int64, error) {
					if actual != scope || !maps.Equal(deferred, map[string]bool{"peer": true}) {
						t.Fatal("staged INSERT lost reviewed scope or field set")
					}
					calls = append(calls, "insert")
					delete(deferred, "peer")
					deferred["unrelated"] = true
					if mode == "insert_failure" && i == 1 {
						return 0, failure
					}
					if i == 1 {
						if mode == "expired_finish" {
							scope.failure = failure
						}
						if mode == "cancelled_finish" {
							cancel()
						}
					}
					return keys[i], nil
				}
				nodes[i].finish = func(_ context.Context, actual db.RelationSession, _ auth.Principal, allocated transitionTargetMappings, deferred map[string]bool) error {
					if actual != scope || !maps.Equal(deferred, map[string]bool{"peer": true}) {
						t.Fatal("finish reused callback-mutated fields")
					}
					for j, identity := range identities {
						key, present := allocated[identity.kind].targets[identity.id]
						if !present || key != keys[j] {
							t.Fatal("finish ran before all real keys existed")
						}
					}
					calls = append(calls, "finish")
					delete(deferred, "peer")
					if mode == "finish_failure" {
						return failure
					}
					if mode == "expired_after_finish" && i == 1 {
						scope.failure = failure
					}
					return nil
				}
			}
			if mode == "missing_finish" {
				nodes[1].finish = nil
			}
			actual, err := applyTransitionAllocations(ctx, scope, auth.Principal{}, refs, nodes)
			for _, namespace := range refs {
				if namespace.targets != nil {
					t.Fatal("staging changed caller correspondence")
				}
			}
			if mode == "complete" {
				if err != nil || actual == nil || !slices.Equal(calls, []string{"insert", "insert", "finish", "finish"}) {
					t.Fatal("allocation did not complete one graph", calls, err)
				}
				return
			}
			if err == nil || actual != nil {
				t.Fatal("failed staged graph published a partial result", err)
			}
			if mode == "missing_finish" && len(calls) != 0 {
				t.Fatal("missing finalizer wrote a row")
			}
			if (mode == "insert_failure" || mode == "expired_finish" || mode == "cancelled_finish") && !slices.Equal(calls, []string{"insert", "insert"}) {
				t.Fatal("invalid scope reached finalization", calls)
			}
			if mode == "finish_failure" && !slices.Equal(calls, []string{"insert", "insert", "finish"}) {
				t.Fatal("failed finalizer did not stop the graph", calls)
			}
			if mode == "cancelled_finish" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost ownership", err)
			}
			if mode != "missing_finish" && mode != "cancelled_finish" && !errors.Is(err, failure) {
				t.Fatal("native failure lost ownership", err)
			}
		})
	}
}

func TestTransitionDeferredUpdateKeepsOneLogicalCreation(t *testing.T) {
	deferred := map[string]bool{"mentor": true, "group": true}
	for _, changed := range [][]string{{"mentor", "group"}, {"group", "mentor"}, {"mentor"}, {"mentor", "revision"}, {"mentor", "mentor"}, {"mentor", "group", "password"}} {
		err := validateTransitionDeferredUpdate(changed, deferred)
		valid := slices.Equal(changed, []string{"mentor", "group"}) || slices.Equal(changed, []string{"group", "mentor"})
		if (err == nil) != valid {
			t.Fatal("staged write changed fields outside the reviewed FK completion", changed, err)
		}
	}
}

func TestTransitionAllocationBoundsIntermediateAndCompletedRows(t *testing.T) {
	// Shared string storage keeps this boundary test small in memory while the
	// actual serialized values span the full combined admission budget.
	payload := query.String(strings.Repeat("x", 1<<20))
	for _, mode := range []string{"intermediate", "completion"} {
		t.Run(mode, func(t *testing.T) {
			scope := &allocationTestScope{}
			refs := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{0: 1, 1: 1}}}
			inserted, finished := 0, 0
			nodes := make([]transitionAllocation, 2)
			for i := range nodes {
				source := SourceAccount{ID: int64(i), Revision: 1}
				complete := false
				nodes[i] = transitionAllocation{
					reference: transitionReferenceNode{source: source, references: map[string]TransitionTargetReference{"self": {Source: source}}, nullable: map[string]bool{"self": true}},
					insert: func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) (int64, error) {
						inserted++
						return int64(i + 10), nil
					},
					finish: func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) error {
						complete = true
						finished++
						return nil
					},
					values: func() map[string]query.Value {
						values := map[string]query.Value{"id": query.Integer(int64(i + 10))}
						if mode == "intermediate" || complete {
							for field := range MaximumTransitionStateBytes / (2 << 20) {
								values[strconv.Itoa(field)] = payload
							}
						}
						return values
					},
				}
			}
			keys, err := applyTransitionAllocations(t.Context(), scope, auth.Principal{}, refs, nodes)
			if err == nil || keys != nil || inserted != 2 || mode == "intermediate" && finished != 0 || mode == "completion" && finished != 2 {
				t.Fatal("allocation escaped combined row-state bound", mode, inserted, finished, err)
			}
		})
	}
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
				nodes[i].values = func() map[string]query.Value { return map[string]query.Value{"id": query.Integer(native[i])} }
				nodes[i].reference = transitionReferenceNode{kind: identity.kind, source: SourceAccount{ID: identity.id, Revision: references[identity.kind].revisions[identity.id]}}
				if i+1 < len(nodes) {
					next := identities[i+1]
					nodes[i].reference.references = map[string]TransitionTargetReference{"dependency": {Kind: next.kind, Source: SourceAccount{ID: next.id, Revision: references[next.kind].revisions[next.id]}}}
				}
				nodes[i].insert = func(ctx context.Context, actualScope db.RelationSession, _ auth.Principal, allocated transitionTargetMappings, deferred map[string]bool) (int64, error) {
					if actualScope != scope || ctx.Err() != nil || len(deferred) != 0 {
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
