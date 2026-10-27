package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain/models"
)

func TestMappingPayloadIsBoundedCanonicalAndOwned(t *testing.T) {
	rows := []Mapping{{SourceID: 0, SourceRevision: 1, TargetID: -9, TargetRevision: 2}, {SourceID: -1, SourceRevision: 3, TargetID: 0, TargetRevision: 4}}
	prepared, err := prepareMappings(rows)
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
		if decoded, err := decodeMappings(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: payload}); !errors.Is(err, ErrCorrupt) || decoded != nil {
			t.Fatal("corrupt payload passed even with matching checksum")
		}
	}
	receipt.state.mappingSHA256 = prepared.digest
	decoded, err := decodeMappings(receipt, models.Mapping{ID: 1, TransitionID: 1, Payload: want})
	if err != nil || !slices.Equal(decoded, prepared.rows) {
		t.Fatal("canonical payload did not round trip", err)
	}
	decoded[0].TargetID = 77
	if prepared.rows[0].TargetID != 0 {
		t.Fatal("decoded data aliases preparation")
	}
}

type nilMappingContext struct{ context.Context }

func TestMappedOwnershipRejectsTypedNilContext(t *testing.T) {
	var ctx *nilMappingContext
	if _, err := Prepare(ctx, strings.Repeat("a", 64), strings.Repeat("b", 64)); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil context accepted", err)
	}
	if _, err := TransitionMappedIn(ctx, nil, Receipt{}, Change{}, time.Time{}, []Mapping{{SourceRevision: 1, TargetRevision: 1}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("typed nil mapped context accepted", err)
	}
}
