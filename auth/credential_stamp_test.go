package auth_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestCredentialSessionStampTracksPasswordAndIdentityOnly(t *testing.T) {
	t.Parallel()
	makeCredential := func(id, username, encoded string, permissions ...auth.Permission) auth.Credential {
		t.Helper()
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: true, Permissions: permissions})
		if err != nil {
			t.Fatal(err)
		}
		credential, err := auth.NewCredential(username, encoded, principal)
		if err != nil {
			t.Fatal(err)
		}
		return credential
	}
	initial := makeCredential("user-1", "alice", "encoded-salted-password-v1", "article.view")
	stamp := initial.SessionStamp()
	if len(stamp) != 64 || !initial.MatchesSessionStamp(stamp) {
		t.Fatal("credential did not produce valid session state")
	}
	for name, credential := range map[string]auth.Credential{
		"username": makeCredential("user-1", "renamed", "encoded-salted-password-v1", "article.view"),
		"grants":   makeCredential("user-1", "alice", "encoded-salted-password-v1", "article.change"),
	} {
		if !credential.MatchesSessionStamp(stamp) {
			t.Errorf("%s change invalidated credential session", name)
		}
	}
	for name, credential := range map[string]auth.Credential{
		"password": makeCredential("user-1", "alice", "encoded-salted-password-v2"),
		"identity": makeCredential("user-2", "alice", "encoded-salted-password-v1"),
		"zero":     {},
	} {
		if credential.MatchesSessionStamp(stamp) {
			t.Errorf("%s accepted previous session", name)
		}
	}
	for _, invalid := range []string{"", stamp[:63], stamp + "0", strings.ToUpper(stamp), strings.Repeat("0", 64)} {
		if initial.MatchesSessionStamp(invalid) {
			t.Error("credential accepted malformed or forged stamp")
		}
	}
	if (auth.Credential{}).MatchesSessionStamp("") || (auth.Credential{}).SessionStamp() != "" {
		t.Fatal("zero credential supplied authentication state")
	}
	encoded, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{fmt.Sprintf("%v %+v %#v", initial, initial, initial), string(encoded)} {
		for _, secret := range []string{"user-1", "alice", "encoded-salted-password-v1", stamp} {
			if strings.Contains(output, secret) {
				t.Fatal("credential formatting exposed authentication material")
			}
		}
	}
}
