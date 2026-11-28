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

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

// Exercise a host's direct ChangePassword/Apply call separately from Account's
// Form/JSON handlers. Encoded header inputs are exclusively this test protocol.
func selfPasswordWeb(t *testing.T, open Open) {
	for _, mode := range []string{"success", "wrong_old", "weak", "csrf", "anonymous", "duplicate_cookie", "rollback", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			fault := errors.New("private direct password write fault")
			boundary := &profileFaultBackend{Backend: backend, fault: fault}
			armed := false
			var effects []string
			faults := 0
			boundary.decorate = func(scope db.Session) db.Session {
				if armed && mode == "rollback" {
					return &passwordWriteFaultScope{Session: scope, mode: "after_password", fault: fault, effects: &effects, faults: &faults}
				}
				return scope
			}
			h := newAccountHTTP(t, f, boundary, nil, func(runtime *sessionauth.Runtime) []web.Route {
				return []web.Route{
					{Name: "accountprobe:direct_password", Method: "POST", Path: "/direct-password/", Handler: func(request *web.Request) (web.Response, error) {
						if err := runtime.VerifyCSRF(request, nil); err != nil {
							return web.NewResponse(403, nil, nil)
						}
						old, err := base64.RawURLEncoding.DecodeString(request.HTTP().Header.Get("X-Test-Old"))
						if err != nil {
							return web.NewResponse(400, nil, nil)
						}
						next, err := base64.RawURLEncoding.DecodeString(request.HTTP().Header.Get("X-Test-New"))
						if err != nil {
							return web.NewResponse(400, nil, nil)
						}
						result, err := runtime.ChangePassword(request, string(old), string(next))
						if err == auth.ErrInvalidCredentials {
							return web.NewResponse(401, nil, nil)
						}
						if _, rejected := validation.Rejected(err); rejected {
							return web.NewResponse(400, nil, nil)
						}
						if err != nil {
							return web.Response{}, err
						}
						response, err := web.NewResponse(200, nil, []byte(result.Principal().ID()))
						if err != nil {
							return web.Response{}, err
						}
						return result.Apply(response)
					}},
					{Name: "accountprobe:direct_view", Method: "GET", Path: "/direct-view/", Handler: func(request *web.Request) (web.Response, error) {
						principal, err := runtime.Principal(request)
						if err != nil {
							return web.Response{}, err
						}
						if !principal.Authenticated() {
							return web.NewResponse(403, nil, nil)
						}
						return web.NewResponse(200, nil, nil)
					}},
				}
			})
			current := h.login(t, h.client, password)
			otherClient := h.newClient(t)
			other := h.login(t, otherClient, password)
			_, body := h.send(t, h.client, "GET", "/account/password/", "", "", nil)
			token := accountCSRF(t, body)
			old, next := password, selfPassword
			if mode == "wrong_old" {
				old = "wrong"
			}
			if mode == "weak" {
				next = "short"
			}
			headers := make(http.Header)
			headers.Set(sessionauth.DefaultCSRFHeader, token)
			headers.Set("X-Test-Old", base64.RawURLEncoding.EncodeToString([]byte(old)))
			headers.Set("X-Test-New", base64.RawURLEncoding.EncodeToString([]byte(next)))
			if mode == "csrf" {
				headers.Del(sessionauth.DefaultCSRFHeader)
			}
			client := h.client
			if mode == "anonymous" {
				client = h.newClient(t)
				_, body := h.send(t, client, "GET", "/account/login/", "", "", nil)
				headers.Set(sessionauth.DefaultCSRFHeader, accountCSRF(t, body))
			}
			if mode == "duplicate_cookie" {
				headers.Set("Cookie", sessionauth.DefaultSessionCookieName+"="+current.ID().Encoded())
			}
			if mode == "rollback" || mode == "unknown" {
				f.owner.hasher.beforeHash = func(context.Context) error {
					armed = true
					if mode == "unknown" {
						boundary.mode = "unknown_commit"
					}
					return nil
				}
			}
			f.owner.hasher.hashes.Store(0)
			f.owner.hasher.verifies.Store(0)
			f.owner.boundary.calls, boundary.calls = 0, 0
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			response, body := h.send(t, client, "POST", "/direct-password/", "", "", headers)
			want := map[string]int{"success": 200, "wrong_old": 400, "weak": 400, "csrf": 403, "anonymous": 401, "duplicate_cookie": 401, "rollback": 500, "unknown": 500}[mode]
			if response.StatusCode != want {
				t.Fatal("direct selected Web password status", mode, response.StatusCode, want)
			}
			for _, secret := range []string{old, next, f.target.EncodedPassword, current.ID().Encoded(), fault.Error()} {
				if strings.Contains(body, secret) {
					t.Fatal("direct Web password response disclosed private material")
				}
			}
			after := userReferenceSnapshot(t, reopen(t))
			prepared := mode == "success" || mode == "rollback" || mode == "unknown"
			if !prepared {
				if f.owner.hasher.hashes.Load() != 0 || boundary.calls != 0 || f.entropy.count() != entropy || len(response.Cookies()) != 0 || !reflect.DeepEqual(before, after) {
					t.Fatal("direct Web refusal published a mutation or cookie")
				}
				return
			}
			id := f.entropy.one(t, entropy)
			if f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 1 || boundary.calls != 1 || f.owner.boundary.calls != 1 {
				t.Fatal("direct Web password request replayed preparation or persistence")
			}
			if mode == "rollback" {
				if len(response.Cookies()) != 0 || !reflect.DeepEqual(before, after) || faults != 1 || !reflect.DeepEqual(effects, []string{"rotation", "session", "password"}) {
					t.Fatal("direct Web failure missed its real password write or rollback")
				}
				return
			}
			expected, envelope := selfPasswordExpected(t, f, f.owner, before, after, current, id, f.clock())
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			if mode == "unknown" {
				if len(response.Cookies()) != 0 {
					t.Fatal("unconfirmed direct Web result published a cookie")
				}
				return
			}
			cookies := response.Cookies()
			if body != f.target.PrincipalID || len(cookies) != 1 || cookies[0].Name != sessionauth.DefaultSessionCookieName || cookies[0].Value != id.Encoded() || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("direct Web Apply lost its exact committed cookie")
			}
			if !reflect.DeepEqual(h.current(t, client).Snapshot(), envelope.Record.Snapshot()) {
				t.Fatal("direct Web cookie does not resolve to its expected native state")
			}
			accountSessionRecord(t, h.runtime.SessionStore(), other, false)
			f.target.EncodedPassword, f.target.Revision = f.owner.encoded.Load().(string), f.target.Revision+1
			f.credential, f.targetSessions = envelope.Credential, []sessions.Record{envelope.Record}
			// The same independent CSRF secret authorizes a second password
			// change, including the same raw password with a fresh hash.
			headers.Set("X-Test-Old", base64.RawURLEncoding.EncodeToString([]byte(selfPassword)))
			headers.Set("X-Test-New", base64.RawURLEncoding.EncodeToString([]byte(selfPassword)))
			entropy = f.entropy.count()
			second, _ := h.send(t, client, "POST", "/direct-password/", "", "", headers)
			if second.StatusCode != 200 || f.owner.hasher.hashes.Load() != 2 || boundary.calls != 2 {
				t.Fatal("direct Web same password or CSRF continuation changed")
			}
			final := userReferenceSnapshot(t, reopen(t))
			expected, nextEnvelope := selfPasswordExpected(t, f, f.owner, after, final, envelope.Record, f.entropy.one(t, entropy), f.clock())
			assertUserReferenceStateAt(t, expected, final, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			if !reflect.DeepEqual(h.current(t, client).Snapshot(), nextEnvelope.Record.Snapshot()) {
				t.Fatal("second direct Web result lost its current bearer")
			}
			address, _ := url.Parse(h.server.URL)
			stale := h.newClient(t)
			stale.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: envelope.Record.ID().Encoded(), Path: "/"}})
			for _, check := range []struct {
				client *http.Client
				status int
			}{{stale, 403}, {client, 200}, {otherClient, 403}} {
				response, _ := h.send(t, check.client, "GET", "/direct-view/", "", "", nil)
				if response.StatusCode != check.status {
					t.Fatal("direct Web current/other/stale authority changed", response.StatusCode, check.status)
				}
			}
			assertUserReferenceState(t, final, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		})
	}
}
