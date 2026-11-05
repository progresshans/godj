package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/account/domain/models"
)

func legacyOriginInput() MappingInventory {
	return MappingInventory{Accounts: []Mapping{{SourceID: 0, TargetID: 0, SourceRevision: 2, TargetRevision: 2}}, Catalog: &CatalogMappings{Groups: []Mapping{{SourceID: -7, TargetID: -7, SourceRevision: 3, TargetRevision: 3}}, Permissions: []Mapping{}}, Legacy: &LegacyIdentityOrigin{
		SchemaSHA256: strings.Repeat("a", 64), ProjectSHA256: strings.Repeat("b", 64), OwnerSHA256: strings.Repeat("c", 64), AccountsSHA256: strings.Repeat("d", 64), CatalogSHA256: strings.Repeat("e", 64), SessionsSHA256: strings.Repeat("f", 64), SessionRecords: 3, AuditSHA256: strings.Repeat("1", 64), AuditEvents: 4}}
}

func TestLegacyOriginCanonicalPayloadPreservesOnlyInitialIdentities(t *testing.T) {
	input := legacyOriginInput()
	prepared, err := prepareMappingSet(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prepared.payload, `{"version":4,"accounts":[{"source_id":0,"source_revision":2,"target_id":0,"target_revision":2}],"catalog":`) || !strings.Contains(prepared.payload, `,"legacy":{"schema_sha256":`) {
		t.Fatal("legacy correspondence lost its exact version and order")
	}
	want := sha256.Sum256(append([]byte("godj.account-mapping.v4\x00"), prepared.payload...))
	if prepared.digest != hex.EncodeToString(want[:]) {
		t.Fatal("legacy evidence has no versioned digest")
	}
	input.Legacy.SessionRecords = 999
	input.Accounts[0].TargetID = 4
	input.Catalog.Groups[0].TargetRevision = 8
	receipt := Receipt{state: &receiptState{id: 1, sequence: 1, mappingCount: 1, mappingSHA256: prepared.digest}}
	decoded, err := decodeMappingSet(receipt, models.Mapping{ID: 2, TransitionID: 1, Payload: prepared.payload})
	if err != nil || !reflect.DeepEqual(decoded, prepared) || decoded.legacy.SessionRecords != 3 {
		t.Fatal("origin round trip lost owned evidence", err)
	}
	decoded.legacy.OwnerSHA256 = strings.Repeat("9", 64)
	if prepared.legacy.OwnerSHA256 != strings.Repeat("c", 64) {
		t.Fatal("origin decode retained an alias")
	}
	receipt.state.sequence = 2
	if value, err := decodeMappingSet(receipt, models.Mapping{ID: 2, TransitionID: 1, Payload: prepared.payload}); !errors.Is(err, ErrCorrupt) || value.legacy != nil {
		t.Fatal("later transition accepted an initial-source origin", err)
	}
	for _, mode := range []string{"changed_key", "changed_revision", "catalog_key", "missing_catalog", "collection", "invalid_digest", "negative_count", "session_bound", "audit_bound"} {
		t.Run(mode, func(t *testing.T) {
			input := legacyOriginInput()
			switch mode {
			case "changed_key":
				input.Accounts[0].TargetID++
			case "changed_revision":
				input.Accounts[0].TargetRevision++
			case "catalog_key":
				input.Catalog.Groups[0].TargetID++
			case "missing_catalog":
				input.Catalog = nil
			case "collection":
				input.Collections = []CollectionIdentity{{Kind: "account", Key: 5, Revision: 1, Additional: true}}
			case "invalid_digest":
				input.Legacy.OwnerSHA256 = strings.Repeat("A", 64)
			case "negative_count":
				input.Legacy.AuditEvents = -1
			case "session_bound":
				input.Legacy.SessionRecords = (1 << 16) + 1
			case "audit_bound":
				input.Legacy.AuditEvents = 1_000_001
			}
			value, err := prepareMappingSet(input)
			if !errors.Is(err, ErrInvalid) || value.legacy != nil || value.rows != nil {
				t.Fatal("invalid identity origin published partial evidence", err)
			}
		})
	}
	for _, payload := range []string{prepared.payload + " ", prepared.payload + "{}", strings.Replace(prepared.payload, `"version":4`, `"version":3`, 1), strings.Replace(prepared.payload, `"legacy":{`, `"unexpected":0,"legacy":{`, 1), strings.Replace(prepared.payload, `"session_records":3`, `"session_records":3,"session_records":3`, 1), strings.Replace(prepared.payload, `"audit_events":4`, `"audit_events":-1`, 1)} {
		receipt.state.sequence = 1
		sum := sha256.Sum256(append([]byte("godj.account-mapping.v4\x00"), payload...))
		receipt.state.mappingSHA256 = hex.EncodeToString(sum[:])
		if value, err := decodeMappingSet(receipt, models.Mapping{ID: 2, TransitionID: 1, Payload: payload}); !errors.Is(err, ErrCorrupt) || value.legacy != nil || value.rows != nil {
			t.Fatal("noncanonical origin passed its own checksum", err)
		}
	}
}
