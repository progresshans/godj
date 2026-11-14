package accountstoretest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountPasswordFailures(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			modes := []string{"password_write", "after_password", "audit_insert", "revoke", "unknown_rollback", "unknown_commit", "cancel", "validation_cleanup"}
			for _, point := range []string{"before_password", "after_password"} {
				for _, kind := range []string{"entropy", "authentication", "diagnostics"} {
					modes = append(modes, point+"_"+kind)
				}
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					backend, reopen := open(t)
					f := provisionSelfPassword(t, backend)
					point := map[string]string{"password_write": "before_password", "audit_insert": "before_audit", "revoke": "after_revoke", "cancel": "callback_cancel", "validation_cleanup": "cleanup_failure"}[mode]
					if point == "" {
						point = mode
					}
					var fault error = errors.New("private selected HTTP password fault")
					for _, kind := range []string{"entropy", "authentication", "diagnostics"} {
						if strings.HasSuffix(mode, "_"+kind) {
							point = strings.TrimSuffix(mode, "_"+kind)
							switch kind {
							case "entropy":
								fault = &sessions.Error{Code: sessions.CodeEntropy}
							case "authentication":
								fault = auth.ErrInvalidCredentials
							case "diagnostics":
								fault = validation.Reject(validation.NewErrors(validation.New("password", "native_failure")), nil)
							}
						}
					}
					boundary := &profileFaultBackend{Backend: backend, fault: fault}
					armed := false
					var effects []string
					faults, policies := 0, 0
					boundary.decorate = func(scope db.Session) db.Session {
						if !armed {
							return scope
						}
						return &selfPasswordFaultScope{scope, point, fault, &effects, &faults}
					}
					policy := func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
						policies++
						if mode == "validation_cleanup" && policies == 2 {
							return validation.NewErrors(validation.New("password", "password_too_similar")), nil
						}
						return validation.Errors{}, nil
					}
					h := newAccountHTTP(t, f, boundary, policy)
					previous := h.login(t, h.client, password)
					response, _ := h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
					if response.StatusCode != 204 {
						t.Fatal("failure fixture lacks current HTTP session")
					}
					token := response.Header.Get(sessionauth.DefaultCSRFHeader)
					before := userReferenceSnapshot(t, reopen(t))
					entropy := f.entropy.count()
					boundary.calls, f.owner.boundary.calls = 0, 0
					f.owner.hasher.hashes.Store(0)
					f.owner.hasher.verifies.Store(0)
					f.owner.hasher.beforeHash = func(context.Context) error { armed = true; boundary.mode = point; return nil }
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					var body string
					if surface == "form" {
						response, body = h.send(t, h.client, "POST", "/account/password/", accountForm(password, selfPassword, selfPassword, token).Encode(), "application/x-www-form-urlencoded", nil)
					} else {
						payload, err := json.Marshal(map[string]string{"old_password": password, "new_password": selfPassword})
						if err != nil {
							t.Fatal(err)
						}
						response, body = h.send(t, h.client, "POST", "/api/account/password/", string(payload), "application/json", headers)
					}
					id := f.entropy.one(t, entropy)
					wantStatus := 500
					if strings.HasPrefix(mode, "unknown_") {
						wantStatus = 503
					}
					if response.StatusCode != wantStatus || len(response.Cookies()) != 0 || response.Header.Get("Retry-After") != "" || boundary.calls != 1 || f.owner.boundary.calls != 1 || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 1 || policies != 2 {
						t.Fatal("HTTP self password failure was retried, rendered as input, or published", mode, response.StatusCode, boundary.calls, policies)
					}
					for _, secret := range []string{password, selfPassword, f.target.EncodedPassword, f.owner.encoded.Load().(string), previous.ID().Encoded(), id.Encoded(), "private selected HTTP password fault"} {
						if strings.Contains(body, secret) {
							t.Fatal("HTTP password failure exposed private material")
						}
					}
					if surface == "json" && wantStatus == 503 && !strings.Contains(body, `"code":"outcome_unknown"`) {
						t.Fatal("HTTP password outcome classification was lost")
					}
					want := []string{"rotation", "session", "password"}
					for range len(f.targetSessions) - 1 {
						want = append(want, "revoke")
					}
					want = append(want, "audit")
					if mode == "validation_cleanup" {
						want = nil
					}
					if strings.HasPrefix(point, "before_") || strings.HasPrefix(point, "after_") {
						name := strings.TrimPrefix(strings.TrimPrefix(point, "before_"), "after_")
						index := slices.Index(want, name)
						if index < 0 {
							t.Fatal("unknown HTTP password fault point")
						}
						if strings.HasPrefix(point, "after_") {
							index++
						}
						want = want[:index]
						if faults != 1 {
							t.Fatal("HTTP password fault was not injected once")
						}
					}
					if !slices.Equal(effects, want) {
						t.Fatal("HTTP password fault missed its exact native prefix", effects, want)
					}
					after := userReferenceSnapshot(t, reopen(t))
					if mode == "unknown_commit" {
						expected, _ := selfPasswordExpected(t, f, f.owner, before, after, previous, id, f.clock())
						assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
					} else if !reflect.DeepEqual(before, after) {
						t.Fatal("refused HTTP password request changed native/host state")
					}
					if h.client.Jar == nil {
						t.Fatal("password failure lost its browser jar")
					}
					// A failed or uncertain HTTP result must retain the old browser
					// cookie even when reconciliation finds the native commit.
					found := false
					for _, cookie := range h.client.Jar.Cookies(response.Request.URL) {
						if cookie.Name == sessionauth.DefaultSessionCookieName {
							found = cookie.Value == previous.ID().Encoded()
						}
					}
					if !found {
						t.Fatal("failed password response changed the browser bearer")
					}
				})
			}
		})
	}
}
