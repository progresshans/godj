package model

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestTransitionTargetOrderPreservesSignedSourcesAndNamespaces(t *testing.T) {
	requests := []AccountCreation[struct{}]{
		{Source: SourceAccount{ID: -7, Revision: 1}, References: map[string]TransitionTargetReference{"mentor": {Source: SourceAccount{ID: 5, Revision: 1}}, "also": {Source: SourceAccount{ID: 5, Revision: 1}}}},
		{Source: SourceAccount{ID: 0, Revision: 1}, References: map[string]TransitionTargetReference{"group": {Kind: TransitionReferenceGroup, Source: SourceAccount{ID: 0, Revision: 1}}}},
		{Source: SourceAccount{ID: 5, Revision: 1}, References: map[string]TransitionTargetReference{"mentor": {Source: SourceAccount{ID: 0, Revision: 1}}}},
	}
	before := slices.Clone(requests)
	for i := range before {
		before[i].References = maps.Clone(before[i].References)
	}
	order, err := transitionTargetOrder(requests)
	if err != nil || !slices.Equal(order, []int{1, 2, 0}) || !reflect.DeepEqual(before, requests) {
		t.Fatal("target order lost dependency, namespace or caller ownership", order, err)
	}
	for _, mode := range []string{"unknown", "duplicate", "self", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			rows := slices.Clone(before)
			for i := range rows {
				rows[i].References = maps.Clone(rows[i].References)
			}
			switch mode {
			case "unknown":
				rows[0].References["mentor"] = TransitionTargetReference{Source: SourceAccount{ID: 99, Revision: 1}}
			case "duplicate":
				rows[1].Source.ID = rows[0].Source.ID
			case "self":
				rows[0].References["mentor"] = TransitionTargetReference{Source: rows[0].Source}
			case "cycle":
				rows[1].References["mentor"] = TransitionTargetReference{Source: rows[0].Source}
			}
			partial, err := transitionTargetOrder(rows)
			if err == nil || partial != nil {
				t.Fatal("invalid dependency graph published a partial order", partial, err)
			}
			if (mode == "self" || mode == "cycle") && !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				t.Fatal("native auto-key cycle lost its explicit boundary", err)
			}
		})
	}
}

func TestTransitionTargetReferencesRetainReviewedCatalogRevisions(t *testing.T) {
	catalog := transitionCatalogObservation{
		before: transitionCatalogInventory{groups: map[int64]transitionCatalogRow{-5: {revision: 2}}, permissions: map[int64]transitionCatalogRow{0: {revision: 7}}},
		groups: map[int64]int64{-5: 0}, direct: map[int64]int64{0: -9},
		mapping: &CatalogMappings{Groups: []AccountMapping{{SourceID: -5, SourceRevision: 1, TargetID: 0, TargetRevision: 2}}, Permissions: []AccountMapping{{SourceID: 0, SourceRevision: 7, TargetID: -9, TargetRevision: 1}}},
	}
	actual := targetReferenceMappings([]SourceAccount{{ID: 0, Revision: 3}}, map[int64]int64{0: 55}, catalog)
	if actual[TransitionReferenceGroup].revisions[-5] != 1 || actual[TransitionReferenceGroup].targets[-5] != 0 || actual[TransitionReferencePermission].revisions[0] != 7 || actual[TransitionReferencePermission].targets[0] != -9 || actual[TransitionReferenceAccount].targets[0] != 55 {
		t.Fatal("resolved namespaces lost their original revision or real signed target")
	}
	catalog.pendingGroups, catalog.pendingPermissions = true, true
	pending := targetReferenceMappings([]SourceAccount{{ID: 0, Revision: 3}}, nil, catalog)
	for _, kind := range []TransitionReferenceKind{TransitionReferenceAccount, TransitionReferenceGroup, TransitionReferencePermission} {
		if pending[kind].targets != nil {
			t.Fatal("pending reference namespace anticipated a target key", kind)
		}
	}
}
