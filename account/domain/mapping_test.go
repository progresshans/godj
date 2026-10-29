package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain/models"
)

func TestMappingPayloadIsBoundedCanonicalAndOwned(t *testing.T) {
	rows := []Mapping{{SourceID: 0, SourceRevision: 1, TargetID: -9, TargetRevision: 2}, {SourceID: -1, SourceRevision: 3, TargetID: 0, TargetRevision: 4}}
	prepared, err := prepareMappingSet(MappingInventory{Accounts: rows})
	if err != nil {
		t.Fatal(err)
	}
	rows[0].TargetID = 77
	want := `[{"source_id":-1,"source_revision":3,"target_id":0,"target_revision":4},{"source_id":0,"source_revision":1,"target_id":-9,"target_revision":2}]`
	if prepared.payload != want {
		t.Fatal("key presence, ordering or input ownership lost")
	}
	receipt := Receipt{state: &receiptState{id: 1, mappingCount: 2, mappingSHA256: prepared.digest}}
	for _, payload := range []string{"null", "{}", "[]", "[" + strings.Repeat("null,", MaximumMappings) + "null]", want + " ", " " + want, strings.Replace(want, `"source_id":-1`, `"source_id":-1,"source_id":-1`, 1), strings.Replace(want, `"target_id":0`, `"target_id":-9`, 1), strings.Repeat(" ", MaximumMappingBytes+1)} {
		sum := sha256.Sum256(append([]byte("godj.account-mapping.v1\x00"), []byte(payload)...))
		receipt.state.mappingSHA256 = hex.EncodeToString(sum[:])
		if decoded, err := decodeMappingSet(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: payload}); !errors.Is(err, ErrCorrupt) || decoded.rows != nil || decoded.catalog != nil {
			t.Fatal("corrupt payload passed even with matching checksum")
		}
	}
	receipt.state.mappingSHA256 = prepared.digest
	decoded, err := decodeMappingSet(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: want})
	if err != nil || !slices.Equal(decoded.rows, prepared.rows) {
		t.Fatal("canonical payload did not round trip", err)
	}
	decoded.rows[0].TargetID = 77
	if prepared.rows[0].TargetID != 0 {
		t.Fatal("decoded data aliases preparation")
	}
}

func TestCatalogMappingPayloadKeepsCompleteIndependentCorrespondence(t *testing.T) {
	accounts := []Mapping{{SourceID: 0, SourceRevision: 1, TargetID: -9, TargetRevision: 2}}
	catalog := CatalogMappings{Groups: []Mapping{{SourceID: 0, SourceRevision: 3, TargetID: -4, TargetRevision: 5}}, Permissions: []Mapping{{SourceID: -2, SourceRevision: 6, TargetID: 0, TargetRevision: 7}}}
	prepared, err := prepareMappingSet(MappingInventory{Accounts: accounts, Catalog: &catalog})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":2,"accounts":[{"source_id":0,"source_revision":1,"target_id":-9,"target_revision":2}],"catalog":{"groups":[{"source_id":0,"source_revision":3,"target_id":-4,"target_revision":5}],"permissions":[{"source_id":-2,"source_revision":6,"target_id":0,"target_revision":7}]}}`
	digest := sha256.Sum256(append([]byte("godj.account-mapping.v2\x00"), want...))
	if prepared.payload != want || prepared.digest != hex.EncodeToString(digest[:]) {
		t.Fatal("catalog correspondence lost its canonical independent namespaces")
	}
	accounts[0].TargetID, catalog.Groups[0].TargetID, catalog.Permissions[0].TargetID = 91, 92, 93
	receipt := Receipt{state: &receiptState{id: 1, mappingCount: 1, mappingSHA256: prepared.digest}}
	decoded, err := decodeMappingSet(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: want})
	if err != nil || !reflect.DeepEqual(decoded, prepared) {
		t.Fatal("catalog mapping did not retain owned arrays", err)
	}
	decoded.catalog.Groups[0].TargetID = 94
	if prepared.catalog.Groups[0].TargetID != -4 {
		t.Fatal("decoded catalog aliases prepared evidence")
	}
	for _, payload := range []string{
		want + " ", " " + want, want + `{}`,
		strings.Replace(want, `"version":2`, `"version":3`, 1),
		strings.Replace(want, `"version":2`, `"version":2,"version":2`, 1),
		strings.Replace(want, `"catalog":{`, `"unknown":0,"catalog":{`, 1),
		strings.Replace(want, `"source_revision":6`, `"source_revision":0`, 1),
		strings.Replace(want, `"source_revision":6`, `"source_revision":6,"source_revision":6`, 1),
		strings.Replace(want, `"groups":[`, `"groups":null,"ignored":[`, 1),
		strings.Replace(want, `"permissions":[`, `"permissions":[`+strings.Repeat(`{},`, MaximumMappings), 1),
		strings.Replace(want, `"permissions":[`, `"permissions":[{"source_id":-2,"source_revision":1,"target_id":10,"target_revision":1},`, 1),
		strings.Replace(want, `"permissions":[`, `"permissions":[{"source_id":-3,"source_revision":1,"target_id":0,"target_revision":1},`, 1),
	} {
		sum := sha256.Sum256(append([]byte("godj.account-mapping.v2\x00"), payload...))
		receipt.state.mappingSHA256 = hex.EncodeToString(sum[:])
		if actual, err := decodeMappingSet(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: payload}); !errors.Is(err, ErrCorrupt) || actual.rows != nil || actual.catalog != nil {
			t.Fatal("noncanonical catalog payload passed with its own checksum", err)
		}
	}
	empty, err := prepareMappingSet(MappingInventory{Accounts: prepared.rows, Catalog: &CatalogMappings{}})
	if err != nil || !strings.Contains(empty.payload, `"catalog":{"groups":[],"permissions":[]}`) {
		t.Fatal("explicit empty catalog was lost", err)
	}
	legacy, err := prepareMappingSet(MappingInventory{Accounts: prepared.rows})
	if err != nil || legacy.digest == empty.digest || legacy.catalog != nil {
		t.Fatal("absent catalog is indistinguishable from explicit empty", err)
	}
	for _, bad := range []CatalogMappings{
		{Groups: make([]Mapping, MaximumMappings+1)},
		{Permissions: make([]Mapping, MaximumMappings+1)},
		{Groups: []Mapping{{SourceRevision: 0, TargetRevision: 1}}},
		{Permissions: []Mapping{{SourceID: 1, SourceRevision: 1, TargetID: 2, TargetRevision: 1}, {SourceID: 2, SourceRevision: 1, TargetID: 2, TargetRevision: 1}}},
	} {
		if _, err := prepareMappingSet(MappingInventory{Accounts: prepared.rows, Catalog: &bad}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid catalog accepted", err)
		}
	}
}

type nilMappingContext struct{ context.Context }

func TestMappedOwnershipRejectsTypedNilContext(t *testing.T) {
	var ctx *nilMappingContext
	if _, err := Prepare(ctx, strings.Repeat("a", 64), strings.Repeat("b", 64)); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil context accepted", err)
	}
	if _, err := TransitionMappedIn(ctx, nil, Receipt{}, Change{}, time.Time{}, MappingInventory{Accounts: []Mapping{{SourceRevision: 1, TargetRevision: 1}}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil mapped context accepted", err)
	}
	if _, found, err := LookupCatalogMappingsIn(ctx, nil, Receipt{}); !errors.Is(err, ErrInvalid) || found {
		t.Fatal("typed nil catalog lookup accepted", err)
	}
}
