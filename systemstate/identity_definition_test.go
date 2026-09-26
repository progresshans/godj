package systemstate

import (
	"bytes"
	"fmt"
	"github.com/progresshans/godj/migrations/definition"
	"strings"
	"testing"
)

func TestIdentityTransitionDefinitionMatchesNormalizedSchemaAndIsDetached(t *testing.T) {
	migration, err := identityTransitionMigration()
	if err != nil {
		t.Fatal(err)
	}
	document, err := definition.Encode(definition.Producer{Name: "godj-systemstate", Version: "1"}, migration)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(document, identityTransitionDocument) {
		t.Fatal("identity transition migration drift")
	}
	first, second := IdentityMigrationSources(), IdentityMigrationSources()
	if len(first) != 3 {
		t.Fatal("incomplete identity migration graph")
	}
	for index := range first {
		first[index].Document[0] ^= 0xff
		if bytes.Equal(first[index].Document, second[index].Document) {
			t.Fatal("shared migration document")
		}
	}
	if _, _, err := definition.Load(second...); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityTransitionReceiptKeepsDiagnosticFallbackOpaque(t *testing.T) {
	const marker = "private-source-fingerprint-marker"
	receipt := IdentityTransition{state: &identityTransitionState{fingerprint: marker}}
	for _, value := range []any{receipt, &receipt} {
		for _, format := range []string{"%v", "%+v", "%#v", "%d", "%f", "%p", "%w"} {
			if strings.Contains(fmt.Sprintf(format, value), marker) {
				t.Fatal("transition fingerprint exposed", format)
			}
		}
	}
}

func TestIdentityTransitionFingerprintBindsExactFramedSourceBytes(t *testing.T) {
	base := credentialRow{id: 1, principalID: "operator", username: "name", encodedPassword: "hash", active: true, permissions: "grants", definitionDigest: "policy"}
	seen := map[string]bool{credentialFingerprint(base): true}
	variants := []credentialRow{base, base, base, base, base, base, base, base, base}
	variants[0].id++
	variants[1].principalID += "x"
	variants[2].username += "x"
	variants[3].encodedPassword += "x"
	variants[4].active = false
	variants[5].permissions += "x"
	variants[6].definitionDigest += "x"
	variants[7].encodedPassword = string([]byte{0xfe})
	variants[8].encodedPassword = string([]byte{0xff})
	for _, row := range variants {
		digest := credentialFingerprint(row)
		if len(digest) != 64 || seen[digest] {
			t.Fatal("distinct source bytes have an aliased receipt")
		}
		seen[digest] = true
	}
	left, right := base, base
	left.username, left.encodedPassword = "ab", "c"
	right.username, right.encodedPassword = "a", "bc"
	if credentialFingerprint(left) == credentialFingerprint(right) {
		t.Fatal("source fields are not framed")
	}
}
