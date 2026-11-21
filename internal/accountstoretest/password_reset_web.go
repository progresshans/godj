package accountstoretest

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

// These are host-authored calls to the common Web API. Account's Form/JSON
// token routes and mail acknowledgement are verified by their own consumers.
func resetWebHost(t *testing.T, f *loginFixture, backend Backend) *accountHTTP {
	t.Helper()
	f.owner = newUserReferenceOwner(t, backend, f.userReferenceFixture, f.config(t))
	f.runtime = f.owner.runtime
	h := &accountHTTP{f: f, runtime: f.runtime, manager: f.runtime.Sessions(), now: &f.instant}
	owner := resetNativeOwner(t, f)
	runtime, err := sessionauth.New(sessionauth.Config{Sessions: h.manager, Authenticator: h.runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{}, PasswordResetPersistence: owner, Clock: f.clock,
		SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}, LoginPath: "/direct-reset-csrf/", FallbackPath: "/", AllowedNextPaths: []string{"/"}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "selected_direct_reset", InstalledApps: append(hostproject.InstalledApps(), apps.Config{Name: "github.com/progresshans/godj/internal/accountstoretest", Label: "accountprobe"})})
	if err != nil {
		t.Fatal(err)
	}
	routes := []web.Route{
		{Name: "accountprobe:reset_csrf", Method: "GET", Path: "/direct-reset-csrf/", Handler: func(request *web.Request) (web.Response, error) {
			csrf, err := runtime.CSRFToken(request)
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(200, nil, []byte(csrf.Value()))
			if err != nil {
				return web.Response{}, err
			}
			return csrf.Apply(response)
		}},
		{Name: "accountprobe:reset_entry", Method: "GET", Path: "/direct-reset-entry/", Handler: func(request *web.Request) (web.Response, error) {
			result, err := runtime.StartPasswordReset(request, f.target.PrincipalID, request.HTTP().Header.Get("X-Test-Reset-Token"))
			if err == auth.ErrInvalidResetProof {
				return web.NewResponse(403, nil, nil)
			}
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(302, http.Header{"Location": []string{"/direct-reset-confirm/"}, "Cache-Control": []string{"no-store"}, "Referrer-Policy": []string{"no-referrer"}}, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "accountprobe:reset_check", Method: "GET", Path: "/direct-reset-confirm/", Handler: func(request *web.Request) (web.Response, error) {
			err := runtime.CheckPasswordReset(request, f.target.PrincipalID, nil)
			if err == auth.ErrInvalidResetProof {
				return web.NewResponse(403, nil, nil)
			}
			if err != nil {
				return web.Response{}, err
			}
			return web.NewResponse(200, nil, []byte("confirmation"))
		}},
		{Name: "accountprobe:reset_finish", Method: "POST", Path: "/direct-reset-confirm/", Handler: func(request *web.Request) (web.Response, error) {
			if err := runtime.VerifyCSRF(request, nil); err != nil {
				return web.NewResponse(403, nil, nil)
			}
			raw, err := base64.RawURLEncoding.DecodeString(request.HTTP().Header.Get("X-Test-Reset-Password"))
			if err != nil {
				return web.NewResponse(400, nil, nil)
			}
			result, err := runtime.ResetPassword(request, f.target.PrincipalID, string(raw))
			if err == auth.ErrInvalidResetProof {
				return web.NewResponse(403, nil, nil)
			}
			if _, rejected := validation.Rejected(err); rejected {
				return web.NewResponse(400, nil, nil)
			}
			if err != nil {
				return web.Response{}, err
			}
			response, err := web.NewResponse(302, http.Header{"Location": []string{"/direct-reset-done/"}}, nil)
			if err != nil {
				return web.Response{}, err
			}
			return result.Apply(response)
		}},
		{Name: "accountprobe:reset_who", Method: "GET", Path: "/direct-reset-who/", Handler: func(request *web.Request) (web.Response, error) {
			principal, err := runtime.InspectPrincipal(request)
			if err != nil {
				return web.Response{}, err
			}
			value := "anonymous"
			if principal.Authenticated() {
				value = principal.ID()
			}
			return web.NewResponse(200, nil, []byte(value))
		}},
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
	if err != nil {
		t.Fatal(err)
	}
	h.serve(t, application)
	return h
}

func passwordResetWeb(t *testing.T, open Open) {
	for _, mode := range []string{"missing", "anonymous", "self", "other", "rollback", "unknown", "entry_rollback", "entry_unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			fault := errors.New("private direct reset storage failure")
			boundary := &profileFaultBackend{Backend: backend, fault: fault}
			armed := false
			faults := 0
			var effects []string
			boundary.decorate = func(scope db.Session) db.Session {
				if armed && (mode == "rollback" || mode == "entry_rollback") {
					point := "after_password"
					if mode == "entry_rollback" {
						point = "after_session"
					}
					return &passwordWriteFaultScope{Session: scope, mode: point, fault: fault, effects: &effects, faults: &faults}
				}
				return scope
			}
			h := resetWebHost(t, f, boundary)
			kind := mode
			if strings.Contains(mode, "rollback") || strings.Contains(mode, "unknown") {
				kind = "anonymous"
			}
			previous := resetBrowserPrevious(t, f, kind)
			address, err := url.Parse(h.server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if previous.ID().Valid() {
				h.client.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
			}
			response, csrf := h.send(t, h.client, "GET", "/direct-reset-csrf/", "", "", nil)
			if response.StatusCode != 200 || csrf == "" {
				t.Fatal("direct reset did not bootstrap independent CSRF")
			}
			csrfCookies := h.client.Jar.Cookies(address)
			var csrfCookie string
			for _, cookie := range csrfCookies {
				if cookie.Name == sessionauth.DefaultCSRFCookieName {
					csrfCookie = cookie.Value
				}
			}
			if csrfCookie == "" {
				t.Fatal("direct reset lacks its independent CSRF cookie")
			}
			owner := resetNativeOwner(t, f)
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			before := userReferenceSnapshot(t, reopen(t))
			f.instant.Add(int64(time.Minute))
			f.owner.boundary.calls, boundary.calls = 0, 0
			entropy := f.entropy.count()
			entryFailure := strings.HasPrefix(mode, "entry_")
			if entryFailure {
				armed = true
				if mode == "entry_unknown" {
					boundary.mode = "unknown_commit"
				}
			}
			response, body := h.send(t, h.client, "GET", "/direct-reset-entry/", "", "", http.Header{"X-Test-Reset-Token": []string{token.Encoded()}})
			proofID := f.entropy.one(t, entropy)
			afterEntry := userReferenceSnapshot(t, reopen(t))
			if f.owner.boundary.calls != 1 || boundary.calls != 1 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 {
				t.Fatal("direct Web entry repeated native/password work")
			}
			if strings.Contains(body+response.Header.Get("Location"), token.Encoded()) {
				t.Fatal("direct Web entry disclosed recipient token")
			}
			if entryFailure && (response.StatusCode != 500 || len(response.Cookies()) != 0) {
				t.Fatal("unconfirmed direct entry published a cookie", response.StatusCode)
			}
			if mode == "entry_rollback" {
				if faults != 1 || !reflect.DeepEqual(effects, []string{"rotation", "session"}) {
					t.Fatal("direct entry did not reach its physical failure")
				}
				assertUserReferenceState(t, before, afterEntry, f.actor.Principal())
				return
			}
			expected, proof := resetEntryExpected(t, f, before, afterEntry, previous, proofID, token.Encoded())
			assertUserReferenceState(t, expected, afterEntry, f.actor.Principal())
			if mode == "entry_unknown" {
				return
			}
			cookies := response.Cookies()
			if response.StatusCode != 302 || response.Header.Get("Location") != "/direct-reset-confirm/" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" || len(cookies) != 1 || cookies[0].Name != sessionauth.DefaultSessionCookieName || cookies[0].Value != proofID.Encoded() || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("direct Web entry lost its exact opaque cookie/redirect")
			}
			if !reflect.DeepEqual(h.current(t, h.client).Snapshot(), proof.Snapshot()) {
				t.Fatal("direct Web entry cookie differs from native proof")
			}
			if mode == "self" {
				f.targetSessions[0] = proof
			}
			entropy = f.entropy.count()
			f.owner.boundary.calls, boundary.calls = 0, 0
			response, _ = h.send(t, h.newClient(t), "GET", "/direct-reset-confirm/", "", "", nil)
			if response.StatusCode != 403 {
				t.Fatal("fresh browser borrowed a proof")
			}
			response, _ = h.send(t, h.client, "GET", "/direct-reset-confirm/", "", "", nil)
			if response.StatusCode != 200 {
				t.Fatal("current browser lost its proof")
			}
			headers := http.Header{"X-Test-Reset-Password": []string{base64.RawURLEncoding.EncodeToString([]byte(selfPassword))}}
			response, _ = h.send(t, h.client, "POST", "/direct-reset-confirm/", "", "", headers)
			if response.StatusCode != 403 {
				t.Fatal("direct Web reset accepted missing CSRF")
			}
			headers.Set(sessionauth.DefaultCSRFHeader, csrf)
			headers.Set("X-Test-Reset-Password", base64.RawURLEncoding.EncodeToString([]byte("short")))
			response, _ = h.send(t, h.client, "POST", "/direct-reset-confirm/", "", "", headers)
			if response.StatusCode != 400 {
				t.Fatal("direct Web reset lost typed password refusal")
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || boundary.calls != 0 || f.entropy.count() != entropy || len(response.Cookies()) != 0 {
				t.Fatal("direct proof/CSRF/policy refusal published work")
			}
			assertUserReferenceState(t, afterEntry, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			f.owner.policies.Store(0)
			headers.Set("X-Test-Reset-Password", base64.RawURLEncoding.EncodeToString([]byte(selfPassword)))
			if mode == "rollback" || mode == "unknown" {
				f.owner.hasher.beforeHash = func(context.Context) error {
					armed = true
					if mode == "unknown" {
						boundary.mode = "unknown_commit"
					}
					return nil
				}
			}
			f.instant.Add(int64(time.Minute))
			response, body = h.send(t, h.client, "POST", "/direct-reset-confirm/", "", "", headers)
			if f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 2 || f.owner.boundary.calls != 1 || boundary.calls != 1 {
				t.Fatal("direct Web reset repeated preparation or native commit")
			}
			var id sessions.ID
			if mode == "self" {
				if f.entropy.count() != entropy {
					t.Fatal("self Web reset allocated a session")
				}
			} else {
				id = f.entropy.one(t, entropy)
			}
			for _, secret := range []string{selfPassword, token.Encoded(), proofID.Encoded(), f.target.EncodedPassword, fault.Error()} {
				if strings.Contains(body, secret) {
					t.Fatal("direct Web reset disclosed private data")
				}
			}
			after := userReferenceSnapshot(t, reopen(t))
			if mode == "rollback" || mode == "unknown" {
				if response.StatusCode != 500 || len(response.Cookies()) != 0 {
					t.Fatal("unconfirmed direct Web reset published cookie/success")
				}
			}
			if mode == "rollback" {
				if faults != 1 || !reflect.DeepEqual(effects, []string{"rotation", "session", "password"}) {
					t.Fatal("direct Web reset missed its actual write fault")
				}
				assertUserReferenceState(t, afterEntry, after, f.actor.Principal())
				return
			}
			expected, envelope := resetFinishExpected(t, f, f.owner, afterEntry, after, proof, id)
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			if mode != "unknown" {
				cookies = response.Cookies()
				if response.StatusCode != 302 || response.Header.Get("Location") != "/direct-reset-done/" || len(cookies) != 1 || cookies[0].Name != sessionauth.DefaultSessionCookieName {
					t.Fatal("confirmed direct Web reset lost its cookie outcome")
				}
				if envelope.ClearSession {
					if cookies[0].Value != "" || cookies[0].MaxAge >= 0 {
						t.Fatal("self reset failed to delete its browser cookie")
					}
				} else if cookies[0].Value != id.Encoded() || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || !reflect.DeepEqual(h.current(t, h.client).Snapshot(), envelope.Record.Snapshot()) {
					t.Fatal("direct Web reset published a different durable browser")
				}
			}
			// Inspect reads current authority without deleting a stale cookie after
			// an uncertain commit or sliding unrelated session expiry.
			boundary.mode = ""
			armed = false
			response, who := h.send(t, h.client, "GET", "/direct-reset-who/", "", "", nil)
			want := "anonymous"
			if mode == "other" {
				want = f.actor.Principal().ID()
			}
			if response.StatusCode != 200 || who != want {
				t.Fatal("direct Web reset changed browser authentication", mode, who)
			}
			retained := false
			for _, cookie := range h.client.Jar.Cookies(address) {
				if cookie.Name == sessionauth.DefaultCSRFCookieName && cookie.Value == csrfCookie {
					retained = true
				}
			}
			if !retained {
				t.Fatal("reset changed the independent CSRF cookie")
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		})
	}
}
