package identity

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
)

func resetTestAccount(t *testing.T) Account {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "reset-member", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredential("member", "private-salted-encoding", principal)
	if err != nil {
		t.Fatal(err)
	}
	return Account{&accountState{Profile{ID: 17, PrincipalID: principal.ID(), Username: "member", Active: true, Email: "member@example.test", Revision: 1, PasswordUsable: true}, credential}}
}

func TestPasswordResetKeyOwnershipRotationAndDiagnostics(t *testing.T) {
	active, old := bytes.Repeat([]byte{41}, 32), bytes.Repeat([]byte{93}, 32)
	oldRing, err := NewPasswordResetKeyRing(old)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewPasswordResetKeyRing(active, old)
	if err != nil {
		t.Fatal(err)
	}
	account := resetTestAccount(t)
	token := oldRing.issue(account, 1790000000)
	parsed, ok := parsePasswordResetToken(token.Encoded())
	if !ok || !keys.accepts(account, parsed, 1790000001, time.Hour) {
		t.Fatal("fallback token denied")
	}
	activeOnly, _ := NewPasswordResetKeyRing(active)
	if activeOnly.accepts(account, parsed, 1790000001, time.Hour) {
		t.Fatal("unconfigured fallback accepted")
	}
	newToken := keys.issue(account, 1790000000)
	newParsed, _ := parsePasswordResetToken(newToken.Encoded())
	if oldRing.accepts(account, newParsed, 1790000001, time.Hour) || !activeOnly.accepts(account, newParsed, 1790000001, time.Hour) {
		t.Fatal("issuance used a fallback key")
	}
	var fallback [][]byte
	for index := byte(1); index < 8; index++ {
		fallback = append(fallback, bytes.Repeat([]byte{index}, 32))
	}
	full, err := NewPasswordResetKeyRing(active, fallback...)
	if err != nil {
		t.Fatal(err)
	}
	last, _ := NewPasswordResetKeyRing(fallback[6])
	lastToken, _ := parsePasswordResetToken(last.issue(account, 1790000000).Encoded())
	if !full.accepts(account, lastToken, 1790000000, time.Hour) {
		t.Fatal("last verification key was not visited")
	}
	if _, err := NewPasswordResetKeyRing(active, fallback[0], fallback[0]); err == nil {
		t.Fatal("duplicate fallback key accepted")
	}
	clear(active)
	clear(old)
	if keys.issue(account, 1790000000).Encoded() != newToken.Encoded() || !keys.accepts(account, parsed, 1790000001, time.Hour) {
		t.Fatal("ring borrowed mutable key material")
	}
	for _, value := range []any{keys, &keys, token, &token, PasswordResetConfig{Keys: keys}, PasswordResetToken{}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		outputs := []string{string(data)}
		for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%x", "%s", "%f", "%p", "%w"} {
			outputs = append(outputs, fmt.Sprintf(verb, value))
		}
		for _, output := range outputs {
			for _, secret := range []string{token.Encoded(), "private-salted-encoding", strings.Repeat("]", 32), strings.Repeat(")", 32)} {
				if strings.Contains(output, secret) {
					t.Fatal("reset diagnostic disclosed secret material")
				}
			}
		}
	}
	if (PasswordResetToken{}).Encoded() != "" || (PasswordResetToken{}).PrincipalID() != "" {
		t.Fatal("zero token was usable")
	}
	for _, item := range []struct {
		name      string
		active    []byte
		fallbacks [][]byte
	}{
		{"missing", nil, nil}, {"short", make([]byte, 31), nil}, {"long", make([]byte, 33), nil},
		{"duplicate", make([]byte, 32), [][]byte{make([]byte, 32)}}, {"bad_fallback", make([]byte, 32), [][]byte{make([]byte, 31)}},
		{"excess", make([]byte, 32), make([][]byte, 8)},
	} {
		t.Run(item.name, func(t *testing.T) {
			if _, err := NewPasswordResetKeyRing(item.active, item.fallbacks...); err == nil {
				t.Fatal("invalid key ring accepted")
			}
		})
	}
}

func TestPasswordResetTokenGrammarAndBoundaries(t *testing.T) {
	keys, _ := NewPasswordResetKeyRing(bytes.Repeat([]byte{42}, 32))
	account := resetTestAccount(t)
	token := keys.issue(account, 1790000000)
	parts := strings.Split(token.Encoded(), ".")
	for _, input := range []string{"", strings.Repeat("x", 10000), "r2." + parts[1] + "." + parts[2], "r1.0" + parts[1] + "." + parts[2], "r1.+" + parts[1] + "." + parts[2], "r1.-1." + parts[2], "r1.Z." + parts[2], "r1.zzzzzzzzz." + parts[2], token.Encoded() + ".", token.Encoded() + "=", token.Encoded() + "\n", "r1." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 31))} {
		if _, ok := parsePasswordResetToken(input); ok {
			t.Fatal("noncanonical reset token accepted")
		}
	}
	parsed, ok := parsePasswordResetToken(token.Encoded())
	if !ok {
		t.Fatal("issued token is invalid")
	}
	for _, item := range []struct {
		name string
		now  int64
		want bool
	}{{"same", 1790000000, true}, {"boundary", 1790003600, true}, {"expired", 1790003601, false}, {"future", 1789999999, false}} {
		t.Run(item.name, func(t *testing.T) {
			if keys.accepts(account, parsed, item.now, time.Hour) != item.want {
				t.Fatal("wrong reset time admission")
			}
		})
	}
	parsed.mac[0] ^= 1
	if keys.accepts(account, parsed, 1790000000, time.Hour) {
		t.Fatal("modified token accepted")
	}
	for _, timestamp := range []int64{0, 253402300799} {
		value := keys.issue(account, timestamp)
		decoded, ok := parsePasswordResetToken(value.Encoded())
		if !ok || !keys.accepts(account, decoded, timestamp, time.Hour) {
			t.Fatal("valid supported clock boundary rejected")
		}
	}
}
