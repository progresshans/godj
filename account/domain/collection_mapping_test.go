package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain/models"
)

func TestCollectionMappingKeepsAdditionalAndMappedIdentitiesDistinct(t *testing.T) {
	accounts := []Mapping{{SourceID: -3, SourceRevision: 2, TargetID: 0, TargetRevision: 1}}
	catalog := &CatalogMappings{Groups: []Mapping{{SourceID: 0, SourceRevision: 3, TargetID: -2, TargetRevision: 4}}, Permissions: []Mapping{}}
	collections := []CollectionIdentity{{Kind: "account", Key: 0, Revision: 1}, {Kind: "group", Key: -2, Revision: 4}, {Kind: "account", Key: -1, Revision: 1, Additional: true}, {Kind: "permission", Key: 0, Revision: 1, Additional: true}}
	prepared, err := prepareMappingSet(MappingInventory{Accounts: accounts, Catalog: catalog, Collections: collections})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":3,"accounts":[{"source_id":-3,"source_revision":2,"target_id":0,"target_revision":1}],"catalog":{"groups":[{"source_id":0,"source_revision":3,"target_id":-2,"target_revision":4}],"permissions":[]},"collections":[{"kind":"account","key":0,"revision":1,"additional":false},{"kind":"group","key":-2,"revision":4,"additional":false},{"kind":"account","key":-1,"revision":1,"additional":true},{"kind":"permission","key":0,"revision":1,"additional":true}]}`
	digest := sha256.Sum256(append([]byte("godj.account-mapping.v3\x00"), want...))
	if prepared.payload != want || prepared.digest != hex.EncodeToString(digest[:]) {
		t.Fatal("collection receipt lost canonical signed keys or kind")
	}
	collections[0].Key = 99
	accounts[0].TargetID = 99
	catalog.Groups[0].TargetID = 99
	receipt := Receipt{state: &receiptState{id: 1, mappingCount: 1, mappingSHA256: prepared.digest}}
	actual, err := decodeMappingSet(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: want})
	if err != nil || !reflect.DeepEqual(actual, prepared) {
		t.Fatal("collection mapping round trip lost owned state", err)
	}
	actual.collections[0].Key = 99
	if prepared.collections[0].Key != 0 {
		t.Fatal("decoded collection aliases prepared state")
	}
	shared, err := prepareMappingSet(MappingInventory{Accounts: prepared.rows, Collections: []CollectionIdentity{{Kind: "group", Key: 0, Revision: 1, Additional: true}}})
	if err != nil || !strings.Contains(shared.payload, `"catalog":null`) {
		t.Fatal("shared catalog presence changed", err)
	}
	for _, bad := range [][]CollectionIdentity{
		{{Kind: "unknown", Revision: 1, Additional: true}}, {{Kind: "account", Key: 0, Revision: 1, Additional: true}}, {{Kind: "account", Key: 7, Revision: 1}}, {{Kind: "group", Key: -2, Revision: 1}}, {{Kind: "permission", Revision: 2, Additional: true}}, {{Kind: "permission", Revision: 0, Additional: true}}, {{Kind: "permission", Revision: 1, Additional: true}, {Kind: "permission", Revision: 1, Additional: true}}, make([]CollectionIdentity, MaximumMappings+1),
	} {
		if _, err := prepareMappingSet(MappingInventory{Accounts: prepared.rows, Catalog: prepared.catalog, Collections: bad}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid additional/native identity accepted", err)
		}
	}
	for _, payload := range []string{
		want + " ", want + "{}", strings.Replace(want, `"version":3`, `"version":2`, 1), strings.Replace(want, `"collections":[`, `"collections":null,"ignored":[`, 1), strings.Replace(want, `"kind":"account"`, `"kind":"account","kind":"account"`, 1), strings.Replace(want, `"key":-1`, `"key":0`, 1), strings.Replace(want, `"additional":true`, `"additional":false`, 1), strings.Replace(want, `"collections":[`, `"collections":[`+strings.Repeat(`{},`, MaximumMappings), 1), strings.Replace(want, `"collections":[`, `"other":0,"collections":[`, 1),
	} {
		sum := sha256.Sum256(append([]byte("godj.account-mapping.v3\x00"), payload...))
		receipt.state.mappingSHA256 = hex.EncodeToString(sum[:])
		decoded, err := decodeMappingSet(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: payload})
		if !errors.Is(err, ErrCorrupt) || decoded.rows != nil || decoded.collections != nil {
			t.Fatal("noncanonical collection payload passed its own checksum", err)
		}
	}
	if !slices.Equal(prepared.collections, []CollectionIdentity{{Kind: "account", Key: 0, Revision: 1}, {Kind: "group", Key: -2, Revision: 4}, {Kind: "account", Key: -1, Revision: 1, Additional: true}, {Kind: "permission", Key: 0, Revision: 1, Additional: true}}) {
		t.Fatal("validation changed preparation")
	}
}
