package identitytest

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

// This consumer exercises the public Web runtime over native storage and real
// HTTP/cookie jars. Header inputs are test-only; product Form/JSON parsing and
// OpenAPI consumers are separately owned and must not be inferred from it.
func RunPasswordChangeHTTP(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"success", "wrong_old", "weak", "csrf", "anonymous", "duplicate_cookie", "rollback", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithStaff(false)); err != nil {
				t.Fatal(err)
			}
			boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: "after_password"}
			if mode == "unknown" {
				boundary.mode = "unknown_commit"
			}
			runtime, err := systemstate.OpenIdentity(t.Context(), boundary, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			minimum, err := identity.NewMinimumLengthValidator(8)
			if err != nil {
				t.Fatal(err)
			}
			h := newIdentityHTTPConfigured(t, runtime.Authenticator(), runtime.SessionStore(), runtime.LoginPersistence, func(manager *sessions.Manager) (auth.PasswordChangePersistence, error) {
				return runtime.PasswordChangePersistence(manager, minimum)
			}, func() time.Time { return loginInstant }, func(authRuntime *sessionauth.Runtime) ([]web.Route, []web.Middleware, error) {
				return []web.Route{{Name: "identityprobe:password", Method: "POST", Path: "/password/", Handler: func(request *web.Request) (web.Response, error) {
					if err := authRuntime.VerifyCSRF(request, nil); err != nil {
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
					result, err := authRuntime.ChangePassword(request, string(old), string(next))
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
				}}}, nil, nil
			})
			current := h.login(t, h.client, "member", managementOldPassword, 200)
			otherClient := h.newClient(t)
			other := h.login(t, otherClient, "member", managementOldPassword, 200)
			_, token := h.request(t, h.client, "GET", "/login/", nil)
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			old, next := managementOldPassword, selfPassword
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
				_, token = h.request(t, client, "GET", "/login/", nil)
				headers.Set(sessionauth.DefaultCSRFHeader, token)
			}
			if mode == "duplicate_cookie" {
				headers.Set("Cookie", sessionauth.DefaultSessionCookieName+"="+current.Encoded())
			}
			if mode == "rollback" || mode == "unknown" {
				f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
			}
			f.hasher.calls.Store(0)
			response, body := h.request(t, client, "POST", "/password/", headers)
			boundary.armed = false
			f.hasher.hook = nil
			want := map[string]int{"success": 200, "wrong_old": 400, "weak": 400, "csrf": 403, "anonymous": 401, "duplicate_cookie": 401, "rollback": 500, "unknown": 500}[mode]
			if response.StatusCode != want {
				t.Fatal("password HTTP status", mode, response.StatusCode, want)
			}
			for _, secret := range []string{old, next, before.EncodedPassword, current.Encoded(), "private-password-fault"} {
				if strings.Contains(body, secret) {
					t.Fatal("password HTTP response exposed private material")
				}
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if mode != "success" {
				if len(response.Cookies()) != 0 {
					t.Fatal("unconfirmed change published a cookie")
				}
				if mode != "unknown" && (!reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit)) {
					t.Fatal("HTTP refusal/rollback changed persistent state", mode)
				}
				if mode == "unknown" && (boundary.calls != 1 || f.hasher.calls.Load() != 1 || f.stored(t).Revision != 2 || len(afterAudit) != 1) {
					t.Fatal("HTTP unknown outcome was retried or lost", len(afterAudit))
				}
				return
			}
			cookies := response.Cookies()
			if len(cookies) != 1 || cookies[0].Name != sessionauth.DefaultSessionCookieName || cookies[0].Value == current.Encoded() || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
				t.Fatal("password success cookie policy changed")
			}
			if _, found, err := runtime.SessionStore().Load(t.Context(), other); err != nil || found {
				t.Fatal("other session not eagerly revoked", err)
			}
			newID, err := sessions.ParseID(cookies[0].Value)
			if err != nil {
				t.Fatal(err)
			}
			newRecord, found, err := runtime.SessionStore().Load(t.Context(), newID)
			if err != nil || !found {
				t.Fatal(err)
			}
			oldCSRF := token
			// The existing CSRF token still authenticates the next request after
			// password change; only the session ID/stamp is replaced.
			headers.Set(sessionauth.DefaultCSRFHeader, oldCSRF)
			headers.Set("X-Test-Old", base64.RawURLEncoding.EncodeToString([]byte(selfPassword)))
			headers.Set("X-Test-New", base64.RawURLEncoding.EncodeToString([]byte(selfPassword)))
			second, _ := h.request(t, client, "POST", "/password/", headers)
			if second.StatusCode != 200 || f.stored(t).Revision != 3 || !reflect.DeepEqual(before.LastLogin, f.stored(t).LastLogin) {
				t.Fatal("same password/CSRF/last_login HTTP behavior changed")
			}
			urlValue, _ := url.Parse(h.server.URL)
			staleClient := h.newClient(t)
			staleClient.Jar.SetCookies(urlValue, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: newRecord.ID().Encoded(), Path: "/"}})
			stale, _ := h.request(t, staleClient, "GET", "/view/", nil)
			active, _ := h.request(t, client, "GET", "/view/", nil)
			denied, _ := h.request(t, otherClient, "GET", "/view/", nil)
			if stale.StatusCode != 403 || active.StatusCode != 200 || denied.StatusCode != 403 {
				t.Fatal("current/other/stale bearer result changed")
			}
		})
	}
}
