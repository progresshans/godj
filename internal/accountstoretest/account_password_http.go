package accountstoretest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountPasswordForm(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	h := newAccountHTTP(t, f, backend, nil)
	h.login(t, h.client, password)
	h.now.Add(int64(time.Minute))
	before := userReferenceSnapshot(t, reopen(t))
	response, body := h.send(t, h.client, "GET", "/account/password/", "", "", nil)
	if response.StatusCode != 200 {
		t.Fatal("password GET", response.StatusCode)
	}
	token := accountCSRF(t, body)
	cases := []struct{ name, old, next, confirm string }{
		{"wrong_old", "wrong", selfPassword, selfPassword},
		{"stripped_old", " " + password, selfPassword, selfPassword},
		{"mismatch", password, selfPassword, strings.TrimSpace(selfPassword)},
		{"weak", password, "short", "short"},
		{"wrong_old_weak", "wrong", "short", "short"},
		{"wrong_old_mismatch", "wrong", selfPassword, "different"},
		{"missing_old_weak", "", "short", "short"},
		{"missing_first_weak", password, "", "short"},
		{"missing_confirmation", password, selfPassword, ""},
		{"all_missing", "", "", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response, body := h.send(t, h.client, "POST", "/account/password/", accountForm(test.old, test.next, test.confirm, token).Encode(), "application/x-www-form-urlencoded", nil)
			after := userReferenceSnapshot(t, reopen(t))
			if len(response.Cookies()) != 0 || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("invalid account form published cookies or caching")
			}
			for _, vendor := range []string{"sqlite", "postgres"} {
				reference := readPasswordHTTPReference(t, vendor).Observations.Refusals[test.name]
				if response.StatusCode != reference.Status || !reflect.DeepEqual(accountErrors(body), reference.Errors) || reference.NewCookie || !reference.PasswordUnchanged || !reference.SessionUnchanged || f.target.LastLogin == nil || reference.LastLogin != f.target.LastLogin.Format("2006-01-02T15:04:05.999999-07:00") {
					t.Fatal("selected account form differs from independent Django", test.name, vendor)
				}
			}
			for _, private := range []string{password, selfPassword, "value=\"short\"", "value=\"wrong\"", f.target.EncodedPassword} {
				if strings.Contains(body, private) {
					t.Fatal("account form rendered private password input")
				}
			}
			if f.owner.hasher.hashes.Load() != 0 || !reflect.DeepEqual(before, after) {
				t.Fatal("invalid account form changed native/host state or hashed a password")
			}
		})
	}
}

func accountPasswordLifecycle(t *testing.T, open Open, surface string) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	h := newAccountHTTP(t, f, backend, nil)
	otherClient := h.newClient(t)
	otherRecord := h.login(t, otherClient, password)
	previous := h.login(t, h.client, password)
	withPayload, err := previous.WithValue("payload", "preserved private data")
	if err != nil {
		t.Fatal(err)
	}
	setupBefore := userReferenceSnapshot(t, reopen(t))
	entropy := f.entropy.count()
	rotated, err := h.manager.Rotate(t.Context(), withPayload)
	if err != nil {
		t.Fatal(err)
	}
	expectedSetup := loginExpectedRecord(t, f.credential, withPayload, "rotate", f.entropy.one(t, entropy), f.clock())
	if !reflect.DeepEqual(rotated.Snapshot(), expectedSetup.Snapshot()) {
		t.Fatal("account payload setup lost its expected rotation")
	}
	setupAfter := userReferenceSnapshot(t, reopen(t))
	loginRemoveRecord(t, &setupBefore.account, loginNamespace(t, f.credential), previous.ID())
	loginPutRecord(t, &setupBefore.account, setupAfter.account, loginNamespace(t, f.credential), expectedSetup, false)
	assertUserReferenceState(t, setupBefore, setupAfter, f.actor.Principal())
	for index, record := range f.targetSessions {
		if record.ID() == previous.ID() {
			f.targetSessions[index] = expectedSetup
		}
	}
	previous = expectedSetup
	address, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	h.client.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
	response, _ := h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
	if response.StatusCode != 204 {
		t.Fatal("account CSRF response", response.StatusCode)
	}
	token := response.Header.Get(sessionauth.DefaultCSRFHeader)
	if token == "" {
		t.Fatal("missing account CSRF header")
	}
	h.now.Add(int64(time.Minute))
	initial := previous
	var firstChanged sessions.Record
	var firstHash string
	for index := range 2 {
		old := password
		if index == 1 {
			old = selfPassword
		}
		before := userReferenceSnapshot(t, reopen(t))
		entropy := f.entropy.count()
		f.owner.hasher.hashes.Store(0)
		f.owner.hasher.verifies.Store(0)
		f.owner.boundary.calls = 0
		f.owner.policies.Store(0)
		var body string
		if surface == "form" {
			response, body = h.send(t, h.client, "POST", "/account/password/", accountForm(old, selfPassword, selfPassword, token).Encode(), "application/x-www-form-urlencoded", nil)
		} else {
			payload, err := json.Marshal(map[string]string{"old_password": old, "new_password": selfPassword})
			if err != nil {
				t.Fatal(err)
			}
			headers := make(http.Header)
			headers.Set(sessionauth.DefaultCSRFHeader, token)
			response, body = h.send(t, h.client, "POST", "/api/account/password/", string(payload), "application/json", headers)
		}
		want := 204
		if surface == "form" {
			want = 302
		}
		cookies := response.Cookies()
		if response.StatusCode != want || len(cookies) != 1 || cookies[0].Name != sessionauth.DefaultSessionCookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Value == previous.ID().Encoded() {
			t.Fatal("account password success publication", surface, response.StatusCode)
		}
		if surface == "form" && response.Header.Get("Location") != "/account/password/done/" {
			t.Fatal("account password success redirect changed")
		}
		wantBody := ""
		if surface == "form" {
			wantBody = "Found\n"
		}
		if body != wantBody {
			t.Fatal("password success response published unexpected body")
		}
		after := userReferenceSnapshot(t, reopen(t))
		expected, envelope := selfPasswordExpected(t, f, f.owner, before, after, previous, f.entropy.one(t, entropy), f.clock())
		if cookies[0].Value != envelope.Record.ID().Encoded() || !reflect.DeepEqual(h.current(t, h.client).Snapshot(), envelope.Record.Snapshot()) {
			t.Fatal("account response did not publish exactly its committed bearer")
		}
		assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
		if f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 1 || f.owner.boundary.calls != 1 || f.owner.policies.Load() != 2 {
			t.Fatal("account HTTP repeated or skipped password work")
		}
		accountSessionRecord(t, h.runtime.SessionStore(), previous, false)
		accountSessionRecord(t, h.runtime.SessionStore(), otherRecord, false)
		if index == 0 {
			firstChanged = envelope.Record
			firstHash = f.owner.encoded.Load().(string)
		}
		f.target.EncodedPassword, f.target.Revision = f.owner.encoded.Load().(string), f.target.Revision+1
		f.credential, f.targetSessions, previous = envelope.Credential, []sessions.Record{envelope.Record}, envelope.Record
	}
	reopened := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
	credential, err := reopened.runtime.Authenticator().Authenticate(t.Context(), "member", selfPassword)
	if err != nil || credential.Principal().Staff() || credential.Principal().Superuser() || credential.Principal().Has(identity.ChangeUser) || !credential.MatchesSessionStamp(f.credential.SessionStamp()) {
		t.Fatal("account password reopen lost ordinary self authority", err)
	}
	validities := make([]bool, 2)
	for index, raw := range []string{password, strings.TrimSpace(selfPassword)} {
		_, err := reopened.runtime.Authenticator().Authenticate(t.Context(), "member", raw)
		validities[index] = err == nil
		if err != auth.ErrInvalidCredentials {
			t.Fatal("old or trimmed password unexpectedly authenticates", err)
		}
	}
	before := userReferenceSnapshot(t, reopen(t))
	response, _ = h.send(t, otherClient, "GET", "/api/account/csrf/", "", "", nil)
	if response.StatusCode != 403 || len(response.Cookies()) != 0 {
		t.Fatal("revoked browser retained account access")
	}
	staleClient := h.newClient(t)
	staleClient.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: firstChanged.ID().Encoded(), Path: "/"}})
	response, _ = h.send(t, staleClient, "GET", "/api/account/csrf/", "", "", nil)
	if response.StatusCode != 403 || len(response.Cookies()) != 0 {
		t.Fatal("previous self password bearer retained access")
	}
	response, _ = h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
	if response.StatusCode != 204 {
		t.Fatal("current self password browser lost access")
	}
	response, body := h.send(t, h.client, "GET", "/account/password/done/", "", "", nil)
	if response.StatusCode != 200 || !strings.Contains(body, "Password changed") {
		t.Fatal("account password done page unavailable")
	}
	if !reflect.DeepEqual(before, userReferenceSnapshot(t, reopen(t))) {
		t.Fatal("account status reads refreshed or cleaned up sessions")
	}
	if f.target.LastLogin == nil || !f.target.LastLogin.Equal(nativeLoginInstant) {
		t.Fatal("self password change overwrote last login")
	}
	_, oldPresent, err := reopened.runtime.SessionStore().Load(t.Context(), initial.ID())
	if err != nil {
		t.Fatal(err)
	}
	stored := profileRow(t, reopen(t), f.target.ID)
	if stored.LastLogin == nil {
		t.Fatal("password reference lost last login")
	}
	common := map[string]any{"session_rotated": firstChanged.ID() != initial.ID(), "old_session_retained": oldPresent, "old_password_valid": validities[0], "new_password_valid": credential.Principal().Authenticated(), "trimmed_password_valid": validities[1], "last_login": stored.LastLogin.Format("2006-01-02T15:04:05.999999-07:00"), "active": stored.Active, "staff": stored.Staff, "superuser": stored.Superuser}
	for _, vendor := range []string{"sqlite", "postgres"} {
		reference := readPasswordHTTPReference(t, vendor)
		for key, actual := range common {
			if !reflect.DeepEqual(reference.Observations.Success[key], actual) {
				t.Fatal("selected self password common behavior differs from Django", vendor, key)
			}
		}
		if reference.Observations.Success["other_session_before_access"] != true || reference.Observations.SamePassword["hash_changed"] != (stored.EncodedPassword != firstHash) || reference.Observations.SamePassword["key_changed"] != (previous.ID() != firstChanged.ID()) || reference.Observations.SamePassword["last_login"] != common["last_login"] {
			t.Fatal("independent self password behavior or eager revocation difference changed")
		}
	}
	response, _ = h.send(t, h.client, "POST", "/account/logout/", url.Values{"csrfmiddlewaretoken": {accountCSRF(t, body)}}.Encode(), "application/x-www-form-urlencoded", nil)
	if response.StatusCode != 302 || len(response.Cookies()) != 2 {
		t.Fatal("account logout publication", response.StatusCode)
	}
	loginRemoveRecord(t, &before.account, loginNamespace(t, f.credential), previous.ID())
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	accountSessionRecord(t, reopened.runtime.SessionStore(), previous, false)
}

func runAccountHTTP(t *testing.T, open Open) {
	t.Run("account_password_form", func(t *testing.T) { accountPasswordForm(t, open) })
	t.Run("account_password_lifecycle", func(t *testing.T) {
		for _, surface := range []string{"form", "json"} {
			t.Run(surface, func(t *testing.T) { accountPasswordLifecycle(t, open, surface) })
		}
	})
	t.Run("account_password_refusals", func(t *testing.T) { accountPasswordRefusals(t, open) })
	t.Run("account_password_failures", func(t *testing.T) { accountPasswordFailures(t, open) })
	t.Run("account_entry_refusals", func(t *testing.T) { accountEntryRefusals(t, open) })
}
