package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTransitionReferenceIdentityNamespacesAndPendingCreation(t *testing.T) {
	fields := []TransitionReferenceFields{{Source: "old_user", Target: "user"}, {Source: "old_group", Target: "group", Kind: TransitionReferenceGroup}, {Source: "old_permission", Target: "permission", Kind: TransitionReferencePermission}}
	config := TransitionReferenceConfig[struct{}]{Fields: fields, RevisionField: "revision"}
	values := map[string]query.Value{"old_user": query.Integer(0), "user": query.Null(), "old_group": query.Integer(0), "group": query.Null(), "old_permission": query.Integer(0), "permission": query.Null(), "revision": query.Integer(7)}
	catalog := transitionCatalogObservation{groups: map[int64]int64{0: -3}, direct: map[int64]int64{0: -5}}
	sources, targets := map[int64]bool{0: true}, map[int64]int64{0: 99}
	bound := referenceMappings(sources, targets, catalog)
	sources[0], targets[0], catalog.groups[0], catalog.direct[0] = false, 88, 77, 66
	updates, err := transitionReferenceUpdates(values, config, bound)
	want := map[string]query.Value{"user": query.Integer(99), "group": query.Integer(-3), "permission": query.Integer(-5), "revision": query.Integer(8)}
	if err != nil || !maps.Equal(updates, want) {
		t.Fatal("coincident keys crossed identity namespaces or input ownership", err)
	}
	pending := referenceMappings(map[int64]bool{0: true}, nil, transitionCatalogObservation{groups: map[int64]int64{0: -3}, direct: map[int64]int64{0: -5}})
	updates, err = transitionReferenceUpdates(values, config, pending)
	if err != nil || !maps.Equal(updates, map[string]query.Value{"group": query.Integer(-3), "permission": query.Integer(-5)}) {
		t.Fatal("unknown account key was guessed or a mixed revision was published", err)
	}
	if err := pending.validateResolved(fields, pending); err == nil {
		t.Fatal("unallocated account identity reached host writes")
	}
	if err := pending.validateResolved(fields, bound); err != nil {
		t.Fatal("complete native account keys were rejected", err)
	}
	changed := referenceMappings(map[int64]bool{0: true}, map[int64]int64{0: 99}, transitionCatalogObservation{groups: map[int64]int64{0: -4}, direct: map[int64]int64{0: -5}})
	if err := pending.validateResolved(fields, changed); err == nil {
		t.Fatal("prepared catalog mapping changed at the write boundary")
	}
	for _, kind := range []TransitionReferenceKind{TransitionReferenceAccount, TransitionReferenceGroup, TransitionReferencePermission} {
		missing := referenceMappings(map[int64]bool{0: true}, map[int64]int64{0: 99}, transitionCatalogObservation{groups: map[int64]int64{0: -3}, direct: map[int64]int64{0: -5}})
		delete(missing, kind)
		if _, err := transitionReferenceUpdates(values, config, missing); err == nil {
			t.Fatal("missing identity namespace was treated as a zero key")
		}
	}
	wrong := maps.Clone(values)
	wrong["group"] = query.Integer(99)
	if _, err := transitionReferenceUpdates(wrong, config, bound); err == nil {
		t.Fatal("an account key was accepted as an existing mapped group")
	}
	wrong = maps.Clone(values)
	wrong["old_group"], wrong["group"] = query.Null(), query.Integer(-3)
	if _, err := transitionReferenceUpdates(wrong, config, bound); err == nil {
		t.Fatal("catalog target appeared without a source reference")
	}
	unknown := []TransitionReferenceFields{{Source: "old_group", Target: "group", Kind: "unknown"}}
	if _, err := normalizeTransitionReferenceFields(ir.ModelIdentity{AppLabel: "host", ModelName: "record"}, unknown, ""); err == nil {
		t.Fatal("unknown reference identity kind accepted")
	}
}

func TestTransitionReferenceManifestVersionsKeepHistoricalEvidence(t *testing.T) {
	manifest := TransitionManifest{
		Version: 4, OperationID: strings.Repeat("a", 64), PriorOperationID: strings.Repeat("b", 64), PriorSequence: 1,
		ProjectSHA256: strings.Repeat("c", 64), SourceRoleSHA256: strings.Repeat("d", 64), TargetRoleSHA256: strings.Repeat("e", 64), StateSHA256: strings.Repeat("f", 64),
		Mappings:   []AccountMapping{{SourceID: 0, SourceRevision: 1, TargetID: -1, TargetRevision: 1}},
		References: []TransitionReferenceManifest{{Model: ir.ModelIdentity{AppLabel: "host", ModelName: "record"}, Fields: []TransitionReferenceFields{{Source: "old_user", Target: "user"}}, Rows: 1, StateSHA256: strings.Repeat("f", 64)}},
	}
	// This frozen wire contains no new kind member. Its hash must remain
	// usable for receipts committed before catalog references existed.
	wire := `{"version":4,"operation_id":"` + strings.Repeat("a", 64) + `","prior_operation_id":"` + strings.Repeat("b", 64) + `","prior_sequence":1,"project_sha256":"` + strings.Repeat("c", 64) + `","source_role_sha256":"` + strings.Repeat("d", 64) + `","target_role_sha256":"` + strings.Repeat("e", 64) + `","state_sha256":"` + strings.Repeat("f", 64) + `","mappings":[{"source_id":0,"source_revision":1,"target_id":-1,"target_revision":1}],"references":[{"model":{"app_label":"host","model_name":"record"},"fields":[{"source":"old_user","target":"user"}],"rows":1,"state_sha256":"` + strings.Repeat("f", 64) + `"}]}`
	want := sha256.Sum256(append([]byte("godj.account-transition-manifest.v1\x00"), wire...))
	actual, err := manifest.evidence()
	if err != nil || actual != hex.EncodeToString(want[:]) {
		t.Fatal("historical v4 evidence changed", err)
	}
	manifest.Catalog = &CatalogMappings{Groups: []AccountMapping{}, Permissions: []AccountMapping{}}
	manifest.Version = 5
	if _, err := manifest.evidence(); err != nil {
		t.Fatal("historical v5 catalog evidence rejected", err)
	}
	manifest.References[0].Fields = []TransitionReferenceFields{{Source: "old_group", Target: "group", Kind: TransitionReferenceGroup}}
	for _, version := range []int{4, 5} {
		manifest.Version = version
		if _, err := manifest.evidence(); err == nil {
			t.Fatal("catalog reference used a format that predates its identity kind")
		}
	}
	for _, catalog := range []*CatalogMappings{nil, {Groups: []AccountMapping{}, Permissions: []AccountMapping{}}} {
		manifest.Version, manifest.Catalog = 6, catalog
		if _, err := manifest.evidence(); err != nil {
			t.Fatal("v6 shared or explicitly mapped catalog reference rejected", err)
		}
		encoded, err := json.Marshal(manifest)
		if err != nil || !strings.Contains(string(encoded), `"kind":"group"`) {
			t.Fatal("catalog identity kind was absent from retained evidence", err)
		}
	}
}
