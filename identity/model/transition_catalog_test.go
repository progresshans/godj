package model

import (
	"crypto/sha256"
	"slices"
	"testing"

	"github.com/progresshans/godj/account/domain"
)

func TestTransitionCatalogMappingsKeepIdentityAndOwnedSignedKeys(t *testing.T) {
	input := CatalogMappings{Groups: []domain.Mapping{{SourceID: 0, SourceRevision: 2, TargetID: -8, TargetRevision: 3}, {SourceID: -4, SourceRevision: 1, TargetID: 0, TargetRevision: 5}}, Permissions: []domain.Mapping{{SourceRevision: 1, TargetRevision: 1}}}
	normalized, err := normalizeCatalogMappings(&input)
	if err != nil {
		t.Fatal(err)
	}
	input.Groups[0].TargetID = 999
	if normalized.Groups[0].SourceID != -4 || normalized.Groups[1].TargetID != -8 {
		t.Fatal("catalog evidence aliases the caller or loses signed ordering")
	}
	observed := transitionCatalogObservation{groups: map[int64]int64{-4: 0, 0: -8}, direct: map[int64]int64{0: -2}}
	mapped, err := observed.memberships(accountMemberships{groups: []int64{-4, 0}, direct: []int64{0}})
	if err != nil || !slices.Equal(mapped.groups, []int64{-8, 0}) || !slices.Equal(mapped.direct, []int64{-2}) {
		t.Fatal("zero/signed catalog keys were treated as absence", err)
	}
	if _, err := observed.memberships(accountMemberships{groups: []int64{33}}); err == nil {
		t.Fatal("missing mapping defaulted to key zero")
	}
	rows := map[int64]transitionCatalogRow{0: {revision: 1}, -2: {revision: 1}}
	permutation := []domain.Mapping{{SourceID: -2, SourceRevision: 1, TargetID: 0, TargetRevision: 1}, {SourceID: 0, SourceRevision: 1, TargetID: -2, TargetRevision: 1}}
	if _, err := transitionCatalogPairs(rows, rows, permutation, true); err == nil {
		t.Fatal("shared model identity was reassigned because grants happened to match")
	}
	for _, bad := range []CatalogMappings{
		{Groups: append(slices.Clone(normalized.Groups), normalized.Groups[0])},
		{Permissions: []domain.Mapping{{SourceID: 1, SourceRevision: 1, TargetID: 0, TargetRevision: 1}, {SourceID: 2, SourceRevision: 1, TargetID: 0, TargetRevision: 1}}},
		{Groups: []domain.Mapping{{SourceRevision: 0, TargetRevision: 1}}},
		{Permissions: make([]domain.Mapping, MaximumTransitionCatalogRows+1)},
	} {
		if _, err := normalizeCatalogMappings(&bad); err == nil {
			t.Fatal("invalid or incomplete catalog identity evidence accepted")
		}
	}
}

func TestTransitionCatalogObservationSharesCompleteStateBudget(t *testing.T) {
	parent := &transitionDigestWriter{hash: sha256.New()}
	parent.part([]byte("account state framing"))
	observed := transitionCatalogObservation{bytes: MaximumTransitionStateBytes - parent.size, digest: "catalog digest"}
	if err := appendTransitionCatalogDigest(parent, observed); err == nil {
		t.Fatal("catalog state left no room for its framing but was accepted")
	}
	parent = &transitionDigestWriter{hash: sha256.New(), size: MaximumTransitionStateBytes - 10}
	if err := appendTransitionCatalogDigest(parent, transitionCatalogObservation{bytes: 11}); err == nil {
		t.Fatal("aggregate account/catalog budget was ignored")
	}
	parent = &transitionDigestWriter{hash: sha256.New()}
	if err := appendTransitionCatalogDigest(parent, transitionCatalogObservation{bytes: 100, digest: "digest"}); err != nil || parent.size != 100+8+len("digest") {
		t.Fatal("catalog framing did not count its complete observed state", err)
	}
}
