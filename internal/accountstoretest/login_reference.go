package accountstoretest

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/sessions"
	websession "github.com/progresshans/godj/web/sessionauth"
)

func assertLoginReference(t *testing.T, authentication map[string]any, lifecycle []map[string]any) {
	t.Helper()
	if len(lifecycle) != 6 {
		t.Fatal("selected login lifecycle is incomplete")
	}
	var prior map[string]string
	for _, vendor := range []string{"sqlite", "postgres"} {
		raw, err := identityreference.LoginLifecycle(vendor)
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django, Python, Backend string
			Sources                 map[string]string `json:"source_sha256"`
			Observations            struct {
				Authentication map[string]any `json:"authentication_only"`
				Lifecycle      []map[string]any
			}
		}
		if err := json.Unmarshal(raw, &reference); err != nil || reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 9 || len(reference.Observations.Lifecycle) != 6 {
			t.Fatal("login reference lost its pinned independent producer", err)
		}
		for _, name := range []string{"admin_forms", "auth", "backends", "base_user", "forms", "hashers", "models", "session_backend", "session_middleware"} {
			value := reference.Sources[name]
			digest, err := hex.DecodeString(value)
			if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
				t.Fatal("login reference lost a source digest", name)
			}
		}
		if prior != nil && !reflect.DeepEqual(prior, reference.Sources) {
			t.Fatal("login reference sources differ between databases")
		}
		prior = reference.Sources
		if !reflect.DeepEqual(authentication, reference.Observations.Authentication) {
			t.Fatal("authentication-only observation differs from Django", vendor)
		}
		// GoDj deliberately rotates even a repeated authenticated login.
		// Assert that deviation before comparing every other observed field.
		if reference.Observations.Lifecycle[3]["key_changed"] != false || lifecycle[3]["key_changed"] != true {
			t.Fatal("repeat-login policy deviation changed")
		}
		encoded, err := json.Marshal(lifecycle)
		if err != nil {
			t.Fatal(err)
		}
		var observed []map[string]any
		if err := json.Unmarshal(encoded, &observed); err != nil {
			t.Fatal(err)
		}
		delete(observed[3], "key_changed")
		delete(reference.Observations.Lifecycle[3], "key_changed")
		if !reflect.DeepEqual(observed, reference.Observations.Lifecycle) {
			t.Fatal("selected login lifecycle differs from independent Django", vendor)
		}
	}
}

func runLoginReference(add registerCase, open Open) {
	add("login_reference", func(t *testing.T) {
		backend, reopen := open(t)
		f := provisionLogin(t, backend, false)
		browser := newUserReferenceHTTPConfigured(t, f.runtime, f.clock, 200)
		defer browser.close()
		previous, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"payload": "anonymous"})
		if err != nil {
			t.Fatal(err)
		}
		address, err := url.Parse(browser.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		browser.client.Jar.SetCookies(address, []*http.Cookie{{Name: websession.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
		var authentication map[string]any
		var observations []map[string]any
		if !t.Run("authentication_only", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			credential, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", password)
			if err != nil || !credential.Principal().Authenticated() {
				t.Fatal("selected password authentication failed", err)
			}
			if _, err := f.runtime.Authenticator().Resolve(t.Context(), f.target.PrincipalID); err != nil {
				t.Fatal(err)
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			stored := profileRow(t, backend, f.target.ID)
			if stored.LastLogin != nil {
				t.Fatal("authentication or resolution recorded a login")
			}
			authentication = map[string]any{"authenticated": credential.Principal().Authenticated(), "last_login": nil}
		}) {
			return
		}
		if !t.Run("denied", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			entropy, writes := f.entropy.count(), f.owner.boundary.calls
			wrong, noID := browser.loginAttempt(t, "member", "wrong", false, 401)
			denied, otherID := browser.loginAttempt(t, "member", password, true, 401)
			if noID.Valid() || otherID.Valid() || len(wrong.Cookies()) != 0 || len(denied.Cookies()) != 0 || f.entropy.count() != entropy || f.owner.boundary.calls != writes {
				t.Fatal("denied HTTP login published a cookie, bearer or write")
			}
			after := userReferenceSnapshot(t, reopen(t))
			assertUserReferenceState(t, before, after, f.actor.Principal())
			accountSessionRecord(t, f.runtime.SessionStore(), previous, true)
			observations = append(observations, map[string]any{"stage": "denied", "wrong_status": wrong.StatusCode, "nonstaff_status": denied.StatusCode, "last_login": nil, "session_retained": reflect.DeepEqual(before.account.sessions, after.account.sessions)})
		}) {
			return
		}
		observeLogin := func(t *testing.T, stage, username, operation string, credential auth.Credential, row models.User) {
			t.Helper()
			before := userReferenceSnapshot(t, reopen(t))
			entropy, writes := f.entropy.count(), f.owner.boundary.calls
			response, id := browser.loginAttempt(t, username, password, false, 200)
			if id != f.entropy.one(t, entropy) || f.owner.boundary.calls != writes+1 {
				t.Fatal("HTTP login did not publish its single native attempt")
			}
			expected := loginExpectedRecord(t, credential, previous, operation, id, f.clock())
			after := userReferenceSnapshot(t, reopen(t))
			assertUserReferenceState(t, loginExpectedState(t, before, after, credential, row, previous, expected, f.clock()), after, f.actor.Principal())
			accountSessionRecord(t, f.runtime.SessionStore(), previous, false)
			accountSessionRecord(t, f.runtime.SessionStore(), expected, true)
			status, _ := browser.exchange(t, "GET", "/view/", nil)
			if status != 200 {
				t.Fatal("published browser lost current view authority", status)
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			stored := profileRow(t, backend, row.ID)
			var payload any
			if value, found := expected.Value("payload"); found {
				payload = value
			}
			observations = append(observations, map[string]any{"stage": stage, "status": response.StatusCode, "last_login": stored.LastLogin.Format("2006-01-02T15:04:05.999999-07:00"), "key_changed": id != previous.ID(), "payload": payload, "authenticated": status == 200})
			previous = expected
		}
		if !t.Run("login", func(t *testing.T) { observeLogin(t, "login", "member", "rotate", f.credential, f.target) }) {
			return
		}
		if !t.Run("access", func(t *testing.T) {
			f.instant.Store(nativeLoginInstant.Add(time.Second).UnixNano())
			before := userReferenceSnapshot(t, reopen(t))
			status, _ := browser.exchange(t, "GET", "/view/", nil)
			if status != 200 {
				t.Fatal("ordinary access lost login", status)
			}
			snapshot := previous.Snapshot()
			snapshot.AccessedAt, snapshot.IdleExpiresAt = f.clock(), f.clock().Add(loginIdle)
			touched, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			after := userReferenceSnapshot(t, reopen(t))
			loginPutRecord(t, &before.account, after.account, loginNamespace(t, f.credential), touched, true)
			assertUserReferenceState(t, before, after, f.actor.Principal())
			accountSessionRecord(t, f.runtime.SessionStore(), touched, true)
			previous = touched
			observations = append(observations, map[string]any{"stage": "access", "last_login": profileRow(t, backend, f.target.ID).LastLogin.Format("2006-01-02T15:04:05.999999-07:00")})
		}) {
			return
		}
		if !t.Run("repeat_login", func(t *testing.T) {
			f.instant.Store(nativeLoginInstant.Add(2 * time.Second).UnixNano())
			observeLogin(t, "repeat_login", "member", "rotate", f.credential, f.target)
		}) {
			return
		}
		if !t.Run("different_user", func(t *testing.T) {
			f.instant.Store(nativeLoginInstant.Add(3 * time.Second).UnixNano())
			observeLogin(t, "different_user", "alice", "replace", f.actor, f.base.user)
		}) {
			return
		}
		if !t.Run("logout", func(t *testing.T) {
			f.instant.Store(nativeLoginInstant.Add(4 * time.Second).UnixNano())
			before := userReferenceSnapshot(t, reopen(t))
			token := browser.request(t, "GET", "/login/", nil, 200)
			response, _ := browser.send(t, "POST", "/logout/", url.Values{"csrfmiddlewaretoken": {token}})
			if response.StatusCode != 200 {
				t.Fatal("selected HTTP logout failed", response.StatusCode)
			}
			cookies := response.Cookies()
			if len(cookies) != 2 {
				t.Fatal("logout did not clear both authentication cookies")
			}
			for _, cookie := range cookies {
				if cookie.MaxAge >= 0 || cookie.Value != "" {
					t.Fatal("logout published a retained cookie")
				}
			}
			loginRemoveRecord(t, &before.account, loginNamespace(t, f.actor), previous.ID())
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			accountSessionRecord(t, f.runtime.SessionStore(), previous, false)
			status, _ := browser.exchange(t, "GET", "/view/", nil)
			if status != 403 {
				t.Fatal("logged-out browser retained authority", status)
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			observations = append(observations, map[string]any{"stage": "logout", "last_login": profileRow(t, backend, f.base.user.ID).LastLogin.Format("2006-01-02T15:04:05.999999-07:00"), "authenticated": status == 200, "payload": nil})
		}) {
			return
		}
		if !t.Run("reopen", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			current := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t)).runtime
			profile, err := current.Accounts().Reader().Get(t.Context(), f.actor.Principal(), f.target.ID)
			if err != nil || profile.ID() != f.target.ID || profile.Revision() != f.target.Revision {
				t.Fatal("selected login reopen changed management revision", err)
			}
			value, loaded := profile.View().Get("last_login")
			instant, valid := value.DateTime()
			if !loaded || !valid || !instant.Equal(nativeLoginInstant.Add(2*time.Second)) {
				t.Fatal("selected profile reader lost canonical last_login")
			}
			if _, err := current.Authenticator().Authenticate(t.Context(), "member", password); err != nil {
				t.Fatal(err)
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			if f.owner.hasher.hashes.Load() != 0 || f.owner.policies.Load() != 0 {
				t.Fatal("login ran password creation or strength policy")
			}
		}) {
			return
		}
		t.Run("django_reference", func(t *testing.T) { assertLoginReference(t, authentication, observations) })
	})
}
