package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func resetTestState(t *testing.T) PasswordResetState {
	t.Helper()
	principal, err := NewPrincipal(PrincipalConfig{ID: "reset-member", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := NewCredential("member", "private-salted-encoding", principal)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := NewPasswordResetState(17, credential, "member@example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func issueTestResetToken(t *testing.T, keys PasswordResetKeyRing, observation PasswordResetState, at int64) PasswordResetToken {
	t.Helper()
	token, err := keys.Issue(observation, at)
	if err != nil {
		t.Fatal(err)
	}
	return token
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
	account := resetTestState(t)
	token := issueTestResetToken(t, oldRing, account, 1790000000)
	parsed, ok := ParsePasswordResetToken(token.Encoded())
	if !ok || !keys.Accepts(account, parsed, 1790000001, time.Hour) {
		t.Fatal("fallback token denied")
	}
	activeOnly, _ := NewPasswordResetKeyRing(active)
	if activeOnly.Accepts(account, parsed, 1790000001, time.Hour) {
		t.Fatal("unconfigured fallback accepted")
	}
	newToken := issueTestResetToken(t, keys, account, 1790000000)
	newParsed, _ := ParsePasswordResetToken(newToken.Encoded())
	if oldRing.Accepts(account, newParsed, 1790000001, time.Hour) || !activeOnly.Accepts(account, newParsed, 1790000001, time.Hour) {
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
	lastToken, _ := ParsePasswordResetToken(issueTestResetToken(t, last, account, 1790000000).Encoded())
	if !full.Accepts(account, lastToken, 1790000000, time.Hour) {
		t.Fatal("last verification key was not visited")
	}
	if _, err := NewPasswordResetKeyRing(active, fallback[0], fallback[0]); err == nil {
		t.Fatal("duplicate fallback key accepted")
	}
	clear(active)
	clear(old)
	if issueTestResetToken(t, keys, account, 1790000000).Encoded() != newToken.Encoded() || !keys.Accepts(account, parsed, 1790000001, time.Hour) {
		t.Fatal("ring borrowed mutable key material")
	}
	for _, value := range []any{keys, &keys, token, &token, account, &account, parsed, &parsed, PasswordResetToken{}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		outputs := []string{string(data)}
		for _, verb := range []string{"%v", "%+v", "%#v", "%d", "%x", "%s", "%f", "%p", "%w"} {
			outputs = append(outputs, fmt.Sprintf(verb, value))
		}
		for _, output := range outputs {
			for _, secret := range []string{token.Encoded(), "private-salted-encoding", "member@example.test", "reset-member", strings.Repeat("]", 32), strings.Repeat(")", 32)} {
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
	account := resetTestState(t)
	token := issueTestResetToken(t, keys, account, 1790000000)
	parts := strings.Split(token.Encoded(), ".")
	for _, input := range []string{"", strings.Repeat("x", 10000), "r2." + parts[1] + "." + parts[2], "r1.0" + parts[1] + "." + parts[2], "r1.+" + parts[1] + "." + parts[2], "r1.-1." + parts[2], "r1.Z." + parts[2], "r1.zzzzzzzzz." + parts[2], token.Encoded() + ".", token.Encoded() + "=", token.Encoded() + "\n", "r1." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 31))} {
		if _, ok := ParsePasswordResetToken(input); ok {
			t.Fatal("noncanonical reset token accepted")
		}
	}
	parsed, ok := ParsePasswordResetToken(token.Encoded())
	if !ok {
		t.Fatal("issued token is invalid")
	}
	for _, item := range []struct {
		name string
		now  int64
		want bool
	}{{"same", 1790000000, true}, {"boundary", 1790003600, true}, {"expired", 1790003601, false}, {"future", 1789999999, false}} {
		t.Run(item.name, func(t *testing.T) {
			if keys.Accepts(account, parsed, item.now, time.Hour) != item.want {
				t.Fatal("wrong reset time admission")
			}
		})
	}
	parsed.mac[0] ^= 1
	if keys.Accepts(account, parsed, 1790000000, time.Hour) {
		t.Fatal("modified token accepted")
	}
	for _, timestamp := range []int64{0, 253402300799} {
		value := issueTestResetToken(t, keys, account, timestamp)
		decoded, ok := ParsePasswordResetToken(value.Encoded())
		if !ok || !keys.Accepts(account, decoded, timestamp, time.Hour) {
			t.Fatal("valid supported clock boundary rejected")
		}
	}
}

func TestPasswordResetObservationBindsCurrentIdentityAndDomain(t *testing.T) {
	keys, err := NewPasswordResetKeyRing(bytes.Repeat([]byte{61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	domain, err := NewCredentialDomain(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	otherDomain, err := NewCredentialDomain(strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	type values struct {
		id                            int64
		principal, login, hash, email string
		active                        bool
		domain                        CredentialDomain
		permissions                   []Permission
		lastLogin                     *time.Time
	}
	observe := func(v values) PasswordResetState {
		t.Helper()
		principal, err := NewPrincipal(PrincipalConfig{ID: v.principal, Active: v.active, Permissions: v.permissions})
		if err != nil {
			t.Fatal(err)
		}
		var credential Credential
		if v.domain.Identifier() == "" {
			credential, err = NewCredential(v.login, v.hash, principal)
		} else {
			credential, err = NewCredentialForDomain(v.domain, v.login, v.hash, principal)
		}
		if err != nil {
			t.Fatal(err)
		}
		state, err := NewPasswordResetState(v.id, credential, v.email, v.lastLogin)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	lastLogin := time.Date(2026, 10, 25, 10, 11, 12, 123456789, time.UTC)
	initial := values{17, "member-principal", "member@example.test", "private-salted-encoding", "recovery@example.test", true, domain, []Permission{"account.view"}, &lastLogin}
	state := observe(initial)
	token := issueTestResetToken(t, keys, state, 1790000000)
	parsed, valid := ParsePasswordResetToken(token.Encoded())
	if !valid || token.PrincipalID() != initial.principal || token.Username() != initial.login || token.Email() != initial.email {
		t.Fatal("delivery lost its signing observation")
	}
	for _, test := range []struct {
		name     string
		change   func(*values)
		accepted bool
	}{
		{"unchanged", func(*values) {}, true},
		{"row_identity", func(v *values) { v.id++ }, false},
		{"principal", func(v *values) { v.principal = "other-principal" }, false},
		{"password", func(v *values) { v.hash = "different-salted-encoding" }, false},
		{"email", func(v *values) { v.email = "changed@example.test" }, false},
		{"email_case", func(v *values) { v.email = "Recovery@example.test" }, false},
		{"last_login_nanosecond", func(v *values) { n := v.lastLogin.Add(time.Nanosecond); v.lastLogin = &n }, false},
		{"last_login_absent", func(v *values) { v.lastLogin = nil }, false},
		{"last_login_same_instant", func(v *values) { n := v.lastLogin.In(time.FixedZone("different-offset", 3600)); v.lastLogin = &n }, true},
		{"inactive", func(v *values) { v.active = false }, false},
		{"unusable", func(v *values) { v.hash = "!private-unusable-marker" }, false},
		{"other_domain", func(v *values) { v.domain = otherDomain }, false},
		{"unscoped", func(v *values) { v.domain = CredentialDomain{} }, false},
		{"login_renamed", func(v *values) { v.login = "renamed@example.test" }, true},
		{"grants_changed", func(v *values) { v.permissions = []Permission{"account.manage"} }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := initial
			test.change(&current)
			if keys.Accepts(observe(current), parsed, 1790000001, time.Hour) != test.accepted {
				t.Fatal("wrong current reset observation admission")
			}
		})
	}
	for _, id := range []int64{math.MinInt64, -17, 0, math.MaxInt64} {
		t.Run(fmt.Sprintf("stored_key_%d", id), func(t *testing.T) {
			v := initial
			v.id = id
			v.lastLogin = nil
			v.email = ""
			observation := observe(v)
			issued := issueTestResetToken(t, keys, observation, 1790000000)
			proof, ok := ParsePasswordResetToken(issued.Encoded())
			if !ok || !keys.Accepts(observation, proof, 1790000001, time.Hour) {
				t.Fatal("stored integer key or absent optional fields rejected")
			}
		})
	}
	lastLogin = lastLogin.Add(time.Hour)
	if issueTestResetToken(t, keys, state, 1790000000).Encoded() != token.Encoded() {
		t.Fatal("observation borrowed mutable last login")
	}
	if token.Username() != initial.login || token.Email() != initial.email {
		t.Fatal("delivery snapshot changed")
	}
}

func TestPasswordResetObservationRejectsInvalidStateWithoutSecrets(t *testing.T) {
	keys, err := NewPasswordResetKeyRing(bytes.Repeat([]byte{62}, 32))
	if err != nil {
		t.Fatal(err)
	}
	observation := resetTestState(t)
	for _, test := range []struct {
		name       string
		credential Credential
		email      string
		at         *time.Time
	}{
		{"zero_credential", Credential{}, "member@example.test", nil},
		{"invalid_utf8", observation.state.credential, "private\xff@example.test", nil},
		{"nul_email", observation.state.credential, "private\x00@example.test", nil},
		{"oversize_email", observation.state.credential, strings.Repeat("x", 4097), nil},
		{"early_last_login", observation.state.credential, "member@example.test", timePointer(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC))},
		{"late_last_login", observation.state.credential, "member@example.test", timePointer(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := NewPasswordResetState(17, test.credential, test.email, test.at)
			if err == nil || value.state != nil {
				t.Fatal("invalid observation published")
			}
			if strings.Contains(fmt.Sprintf("%#v", err), test.email) || strings.Contains(fmt.Sprintf("%#v", err), "private-salted-encoding") {
				t.Fatal("invalid observation disclosed secret")
			}
		})
	}
	for _, timestamp := range []int64{-1, 253402300800} {
		if token, err := keys.Issue(observation, timestamp); err == nil || token.Encoded() != "" {
			t.Fatal("invalid issuance clock published token")
		}
	}
	if token, err := (PasswordResetKeyRing{}).Issue(observation, 1790000000); err == nil || token.Encoded() != "" {
		t.Fatal("zero keys issued token")
	}
	if token, err := keys.Issue(PasswordResetState{}, 1790000000); err != ErrInvalidResetToken || token.Encoded() != "" {
		t.Fatal("zero state issued token")
	}
	issued := issueTestResetToken(t, keys, observation, 1790000000)
	parsed, _ := ParsePasswordResetToken(issued.Encoded())
	for _, timeout := range []time.Duration{-time.Second, 0, time.Nanosecond, time.Second + time.Nanosecond} {
		if keys.Accepts(observation, parsed, 1790000000, timeout) {
			t.Fatal("invalid timeout accepted")
		}
	}
	if keys.Accepts(observation, PasswordResetSignature{}, 1790000000, time.Hour) || keys.Accepts(PasswordResetState{}, parsed, 1790000000, time.Hour) || (PasswordResetKeyRing{}).Accepts(observation, parsed, 1790000000, time.Hour) || keys.Accepts(observation, parsed, -1, time.Hour) || keys.Accepts(observation, parsed, 253402300800, time.Hour) {
		t.Fatal("zero or unsupported verification input accepted")
	}
}

func timePointer(value time.Time) *time.Time { return &value }
