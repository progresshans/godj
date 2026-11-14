package accountstoretest

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountResetHTTP(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			for _, mode := range []string{"missing", "anonymous", "self", "other"} {
				t.Run(mode, func(t *testing.T) {
					backend, reopen := open(t)
					f := provisionSelfPassword(t, backend)
					memory := selectedResetMemory(t)
					h := newResetAccountHTTP(t, f, backend, memory, nil)
					previous := resetBrowserPrevious(t, f, mode)
					link, proof, _ := resetAccountSetup(t, h, reopen, memory, surface, previous)
					beforeRestart := userReferenceSnapshot(t, reopen(t))
					response, body := h.send(t, h.newClient(t), "GET", link.form, "", "", nil)
					resetHTTPHeaders(t, response)
					if response.StatusCode != 200 || !strings.Contains(body, "data-account-view=\"reset-invalid\"") {
						t.Fatal("another browser borrowed reset authority")
					}
					csrfCookie := resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultCSRFCookieName)
					if csrfCookie == "" {
						t.Fatal("reset lifecycle lacks independent CSRF")
					}
					old := h
					h = newResetAccountHTTP(t, f, reopen(t), memory, nil)
					h.client = old.client
					old.server.Close()
					csrf := h.confirmToken(t, h.client, surface, link)
					if !reflect.DeepEqual(h.current(t, h.client).Snapshot(), proof.Snapshot()) {
						t.Fatal("selected proof changed while rebuilding its HTTP owner")
					}
					assertUserReferenceState(t, beforeRestart, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
					entropy := f.entropy.count()
					path, input, media, headers := resetCompleteRequest(t, surface, link, "short", csrf)
					response, body = h.send(t, h.client, "POST", path, input, media, headers)
					resetHTTPHeaders(t, response)
					want := 200
					if surface == "json" {
						want = 400
					}
					if response.StatusCode != want || !strings.Contains(body, "password_too_short") || len(response.Cookies()) != 0 || len(h.reports) != 0 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
						t.Fatal("reset preflight weakened policy or changed work", response.StatusCode)
					}
					assertUserReferenceState(t, beforeRestart, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
					f.owner.policies.Store(0)
					f.instant.Add(int64(time.Minute))
					path, input, media, headers = resetCompleteRequest(t, surface, link, selfPassword, csrf)
					response, body = h.send(t, h.client, "POST", path, input, media, headers)
					resetHTTPHeaders(t, response)
					want = 302
					if surface == "json" {
						want = 204
					}
					if response.StatusCode != want || len(response.Cookies()) != 1 || len(h.reports) != 0 || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 2 || f.owner.boundary.calls != 1 {
						t.Fatal("reset completion did not publish one selected native transition", response.StatusCode)
					}
					if surface == "form" && response.Header.Get("Location") != "/account/reset/complete/" {
						t.Fatal("reset completion redirect escaped its configured route")
					}
					if strings.Contains(body, selfPassword) || strings.Contains(body, link.token) {
						t.Fatal("reset completion disclosed recipient material")
					}
					var id sessions.ID
					if mode == "self" {
						if f.entropy.count() != entropy {
							t.Fatal("self HTTP reset allocated a bearer")
						}
					} else {
						id = f.entropy.one(t, entropy)
					}
					consumedEntropy := f.entropy.count()
					after := userReferenceSnapshot(t, reopen(t))
					expected, envelope := resetFinishExpected(t, f, f.owner, beforeRestart, after, proof, id)
					assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
					cookie := response.Cookies()[0]
					if cookie.Name != sessionauth.DefaultSessionCookieName || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
						t.Fatal("reset lost its selected session cookie policy")
					}
					if envelope.ClearSession {
						if cookie.MaxAge >= 0 || cookie.Value != "" || resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultSessionCookieName) != "" {
							t.Fatal("self reset retained target authentication")
						}
					} else {
						if cookie.Value != id.Encoded() || !reflect.DeepEqual(h.current(t, h.client).Snapshot(), envelope.Record.Snapshot()) {
							t.Fatal("reset cookie differs from expected durable browser")
						}
						principalID, _ := envelope.Record.Value(auth.SessionPrincipalIDKey)
						wantID := ""
						if mode == "other" {
							wantID = f.actor.Principal().ID()
						}
						if principalID != wantID {
							t.Fatal("reset changed unrelated browser authority")
						}
						if mode == "other" {
							credential, err := h.runtime.Authenticator().Resolve(t.Context(), principalID)
							stamp, _ := envelope.Record.Value(auth.SessionCredentialStampKey)
							if err != nil || !credential.MatchesSessionStamp(stamp) || !reflect.DeepEqual(credential.Principal(), f.actor.Principal()) {
								t.Fatal("other browser lost current independent authority", err)
							}
						}
					}
					response, body = h.send(t, h.client, "POST", path, input, media, headers)
					resetHTTPHeaders(t, response)
					want = 200
					if surface == "json" {
						want = 403
					}
					if response.StatusCode != want || len(response.Cookies()) != 0 || len(h.reports) != 0 || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 2 || f.owner.boundary.calls != 1 || f.entropy.count() != consumedEntropy {
						t.Fatal("consumed HTTP proof repeated work", response.StatusCode)
					}
					response, body = h.send(t, h.client, "GET", link.raw, "", "", nil)
					resetHTTPHeaders(t, response)
					if response.StatusCode != 200 || !strings.Contains(body, "data-account-view=\"reset-invalid\"") || len(response.Cookies()) != 0 {
						t.Fatal("used email link regained proof authority")
					}
					if resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultCSRFCookieName) != csrfCookie {
						t.Fatal("reset changed the independent CSRF secret")
					}
					assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
					checker := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
					current, err := checker.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, selfPassword)
					domain, _ := f.credential.Principal().CredentialDomain()
					wanted, expectedErr := auth.NewCredentialForDomain(domain, f.target.Username, f.owner.encoded.Load().(string), f.credential.Principal())
					if err != nil || expectedErr != nil || !current.MatchesSessionStamp(wanted.SessionStamp()) || !reflect.DeepEqual(current.Principal(), wanted.Principal()) {
						t.Fatal("HTTP reset lost its exact credential on reconnect", err)
					}
					for _, raw := range []string{password, strings.TrimSpace(selfPassword)} {
						if _, err := checker.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, raw); err != auth.ErrInvalidCredentials {
							t.Fatal("HTTP reset retained old or trimmed raw password", err)
						}
					}
					assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				})
			}
		})
	}
}
