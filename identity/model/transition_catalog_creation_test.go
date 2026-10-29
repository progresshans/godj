package model

import (
	"maps"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestCatalogCreationEvidenceOwnsSourcesAndResolvesActualKeys(t *testing.T) {
	creation := &CatalogCreationManifest{CreatePermissions: true,
		Groups:      []CatalogCreationSource{{ID: -2, Revision: 5, TargetRevision: 6}, {ID: 0, Revision: 3, TargetRevision: 3}},
		Permissions: []CatalogCreationSource{{ID: 0, Revision: 2, TargetRevision: 1}}}
	manifest := TransitionManifest{Version: 7, OperationID: strings.Repeat("a", 64), PriorOperationID: strings.Repeat("b", 64), PriorSequence: 1,
		ProjectSHA256: strings.Repeat("c", 64), SourceRoleSHA256: strings.Repeat("d", 64), TargetRoleSHA256: strings.Repeat("e", 64), StateSHA256: strings.Repeat("f", 64),
		Mappings: []AccountMapping{{SourceID: 0, SourceRevision: 1, TargetID: -1, TargetRevision: 2}}, CatalogCreation: creation}
	if _, err := manifest.evidence(); err != nil {
		t.Fatal(err)
	}
	owned := cloneCatalogCreationManifest(creation)
	creation.Groups[0].Revision++
	if owned.Groups[0].Revision != 5 {
		t.Fatal("catalog creation evidence aliases caller lists")
	}
	actual := CatalogMappings{Groups: []AccountMapping{{SourceID: -2, SourceRevision: 5, TargetID: -2, TargetRevision: 6}, {SourceID: 0, SourceRevision: 3, TargetID: 0, TargetRevision: 3}},
		Permissions: []AccountMapping{{SourceID: 0, SourceRevision: 2, TargetID: -31, TargetRevision: 1}}}
	if !catalogCreationMatches(*owned, actual) {
		t.Fatal("actual allocated signed key or shared revision advance rejected")
	}
	for _, mutate := range []func(*CatalogMappings){
		func(v *CatalogMappings) { v.Permissions[0].SourceRevision++ },
		func(v *CatalogMappings) { v.Groups[0].TargetRevision++ },
		func(v *CatalogMappings) { v.Groups[0].TargetID = 0 },
		func(v *CatalogMappings) { v.Permissions = nil },
	} {
		copy := cloneCatalogMappings(&actual)
		mutate(copy)
		if catalogCreationMatches(*owned, *copy) {
			t.Fatal("different source, shared identity or final revision accepted")
		}
	}
	for _, mutate := range []func(*CatalogCreationManifest){
		func(v *CatalogCreationManifest) { v.CreatePermissions = false },
		func(v *CatalogCreationManifest) { v.Groups = nil },
		func(v *CatalogCreationManifest) { v.Permissions[0].TargetRevision = 2 },
		func(v *CatalogCreationManifest) { v.Groups[0].Revision = 0 },
		func(v *CatalogCreationManifest) { v.Groups[0], v.Groups[1] = v.Groups[1], v.Groups[0] },
		func(v *CatalogCreationManifest) { v.Groups[1].ID = v.Groups[0].ID },
		func(v *CatalogCreationManifest) {
			v.Permissions = make([]CatalogCreationSource, MaximumTransitionCatalogRows+1)
		},
	} {
		copy := cloneCatalogCreationManifest(owned)
		mutate(copy)
		manifest.CatalogCreation = copy
		if _, err := manifest.evidence(); err == nil {
			t.Fatal("invalid creation evidence accepted")
		}
	}
	manifest.CatalogCreation = owned
	manifest.Catalog = &actual
	if _, err := manifest.evidence(); err == nil {
		t.Fatal("creation evidence mixed with already allocated correspondence")
	}
	manifest.Catalog = nil
	for _, version := range []int{4, 5, 6} {
		manifest.Version = version
		if _, err := manifest.evidence(); err == nil {
			t.Fatal("catalog creation accepted under historical evidence version")
		}
	}
}

func TestCatalogCreationPendingKeysCannotBecomeInventedIdentityMappings(t *testing.T) {
	catalog := transitionCatalogObservation{pendingGroups: true, pendingPermissions: true,
		before: transitionCatalogInventory{groups: map[int64]transitionCatalogRow{0: {revision: 1}}, permissions: map[int64]transitionCatalogRow{-3: {revision: 1}}}}
	membership, err := catalog.memberships(accountMemberships{groups: []int64{0}, direct: []int64{-3}})
	if err != nil || len(membership.groups) != 0 || len(membership.direct) != 0 {
		t.Fatal("pending target keys were guessed from source identities", err)
	}
	if _, err := catalog.memberships(accountMemberships{groups: []int64{91}}); err == nil {
		t.Fatal("unobserved source identity was accepted as pending")
	}
	before := referenceMappings(map[int64]bool{5: true}, map[int64]int64{5: 9}, catalog)
	if !before[TransitionReferenceGroup].sources[0] || before[TransitionReferenceGroup].targets != nil || !before[TransitionReferencePermission].sources[-3] || before[TransitionReferencePermission].targets != nil {
		t.Fatal("pending namespaces lost signed sources or invented targets")
	}
	resolved := referenceMappings(map[int64]bool{5: true}, map[int64]int64{5: 9}, transitionCatalogObservation{groups: map[int64]int64{0: -7}, direct: map[int64]int64{-3: 0}})
	fields := []TransitionReferenceFields{{Kind: TransitionReferenceAccount}, {Kind: TransitionReferenceGroup}, {Kind: TransitionReferencePermission}}
	if err := before.validateResolved(fields, resolved); err != nil {
		t.Fatal("actual catalog keys did not resolve independently", err)
	}
	delete(catalog.before.groups, 0)
	if !before[TransitionReferenceGroup].sources[0] {
		t.Fatal("reference source inventory aliases catalog state")
	}
}

func TestCatalogCreationReadbackRejectsPhysicalChangesWithEqualLogicalGrants(t *testing.T) {
	old := transitionCatalogInventory{
		groups:      map[int64]transitionCatalogRow{0: {revision: 1, values: map[string]query.Value{"name": query.String("source")}, permissions: []int64{-3}}},
		permissions: map[int64]transitionCatalogRow{-3: {revision: 2, code: "app.read", values: map[string]query.Value{"code": query.String("app.read")}}},
		links:       []deletionLink{{key: 1, source: 0, target: -3, sourceValid: true, targetValid: true}},
	}
	next := transitionCatalogInventory{
		groups:      map[int64]transitionCatalogRow{-7: {revision: 1, values: map[string]query.Value{"title": query.String("target")}, permissions: []int64{0}}},
		permissions: map[int64]transitionCatalogRow{0: {revision: 1, code: "app.read", values: map[string]query.Value{"symbol": query.String("app.read")}}},
		links:       []deletionLink{{key: -2}}, // An existing nullable physical row remains untouched.
	}
	expected := createdCatalogState{before: cloneTransitionCatalogInventory(old), after: cloneTransitionCatalogInventory(next)}
	expected.after.links = append(expected.after.links, deletionLink{key: 0, source: -7, target: 0, sourceValid: true, targetValid: true})
	actual := transitionCatalogObservation{before: cloneTransitionCatalogInventory(old), after: cloneTransitionCatalogInventory(next)}
	actual.after.links = append(actual.after.links, deletionLink{key: 0, source: -7, target: 0, sourceValid: true, targetValid: true})
	if err := expected.verify(actual); err != nil {
		t.Fatal("valid native key and retained nullable row rejected", err)
	}
	for _, mutate := range []func(*transitionCatalogObservation){
		func(v *transitionCatalogObservation) { v.before.groups[0].values["name"] = query.String("changed") },
		func(v *transitionCatalogObservation) { v.after.groups[-7].values["title"] = query.String("changed") },
		func(v *transitionCatalogObservation) {
			v.after.links = append(v.after.links, deletionLink{key: 3, source: -7, target: 0, sourceValid: true, targetValid: true})
		},
		func(v *transitionCatalogObservation) {
			v.after.links = append(v.after.links, deletionLink{key: 4, target: 0, targetValid: true})
		},
		func(v *transitionCatalogObservation) { v.after.links = v.after.links[1:] },
		func(v *transitionCatalogObservation) { v.after.links[1].target = 88 },
		func(v *transitionCatalogObservation) { v.after.links[1].key = 73 },
		func(v *transitionCatalogObservation) { v.after.links = v.after.links[:1] },
	} {
		copy := transitionCatalogObservation{before: cloneTransitionCatalogInventory(actual.before), after: cloneTransitionCatalogInventory(actual.after)}
		mutate(&copy)
		if err := expected.verify(copy); err == nil {
			t.Fatal("different complete physical/catalog state was accepted")
		}
	}
	if !maps.Equal(old.groups[0].values, actual.before.groups[0].values) || len(next.links) != 1 || next.links[0] != (deletionLink{key: -2}) {
		t.Fatal("readback or negative controls mutated retained input")
	}
}

func TestCatalogCreationFinalBudgetIncludesAccountsCatalogLinksAndReferences(t *testing.T) {
	state := transitionObservation[struct{}, struct{}]{
		catalog:    transitionCatalogObservation{bytes: MaximumTransitionStateBytes / 2, digest: strings.Repeat("a", 64)},
		pairs:      []transitionPair[struct{}, struct{}]{{old: transitionRow[struct{}]{values: map[string]query.Value{"profile": query.String("before")}}, next: transitionRow[struct{}]{values: map[string]query.Value{"profile": query.String("after")}}}},
		references: []transitionReferenceObservation{{bytes: MaximumTransitionStateBytes / 4}},
	}
	if err := checkTransitionReadbackBudget(state, 1024); err != nil {
		t.Fatal("bounded final state rejected", err)
	}
	if err := checkTransitionReadbackBudget(state, MaximumTransitionStateBytes/4); err == nil {
		t.Fatal("individually bounded final components bypassed aggregate state limit")
	}
	state.inserted.bytes = MaximumTransitionStateBytes / 4
	if err := checkTransitionReadbackBudget(state, 0); err == nil {
		t.Fatal("full inserted through values omitted from aggregate final budget")
	}
	state.inserted.bytes = 0
	state.catalog.bytes = 0
	state.references[0].bytes = MaximumTransitionStateBytes - 16
	if err := checkTransitionReadbackBudget(state, 0); err == nil {
		t.Fatal("host rows omitted final account/catalog framing")
	}
}
