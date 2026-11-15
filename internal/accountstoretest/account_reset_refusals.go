package accountstoretest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountResetFormReference(t *testing.T, h *resetAccountHTTP, reopen func(*testing.T) Backend, link resetHTTPLink, csrf string, before userReferenceState, entropy int) {
	t.Helper()
	first, second := resetHTTPReference(t, "sqlite"), resetHTTPReference(t, "postgres")
	if len(first.Observations.PasswordErrors) != 4 || !reflect.DeepEqual(first.Observations.PasswordErrors, second.Observations.PasswordErrors) {
		t.Fatal("independent reset form observations are incomplete or disagree")
	}
	for _, item := range []struct{ name, raw, confirmation string }{{"mismatch", selfPassword, "different"}, {"required", "", ""}, {"weak", "short", "short"}, {"weak_mismatch", "short", "other"}} {
		t.Run(item.name, func(t *testing.T) {
			response, body := h.send(t, h.client, "POST", link.form, resetAccountForm(item.raw, item.confirmation, csrf), "application/x-www-form-urlencoded", nil)
			resetHTTPHeaders(t, response)
			expected, found := first.Observations.PasswordErrors[item.name]
			if !found || expected.Status != 200 || len(expected.Errors) == 0 || response.StatusCode != expected.Status || !reflect.DeepEqual(accountErrors(body), expected.Errors) {
				t.Fatal("selected reset form differs from independent Django diagnostics", item.name)
			}
			if strings.Contains(body, selfPassword) || len(response.Cookies()) != 0 || len(h.reports) != 0 || h.f.owner.hasher.hashes.Load() != 0 || h.f.owner.hasher.verifies.Load() != 0 || h.f.owner.boundary.calls != 0 || h.f.entropy.count() != entropy {
				t.Fatal("Django form comparison changed state or disclosed password")
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), h.f.actor.Principal())
		})
	}
}

func accountResetRefusals(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			memory := selectedResetMemory(t)
			h := newResetAccountHTTP(t, f, backend, memory, nil)
			link, proof, token := resetAccountSetup(t, h, reopen, memory, surface, sessions.Record{})
			confirmation, apiPath := link.form, link.json
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			f.owner.boundary.calls = 0
			modes := []string{"csrf", "origin", "query", "empty_query", "unknown_member", "duplicate", "null", "oversize_string", "oversize_body", "media", "wrong_target", "missing_proof", "malformed_uid", "padded_uid", "missing_password", "weak", "method", "missing_route"}
			if surface == "json" {
				modes = append(modes, "accept")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					path, body, media := confirmation, resetAccountForm(selfPassword, selfPassword, token), "application/x-www-form-urlencoded"
					if surface == "json" {
						encoded, _ := json.Marshal(map[string]string{"new_password": selfPassword})
						path, body, media = apiPath, string(encoded), "application/json"
					}
					method := "POST"
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					client := h.client
					want := 400
					switch mode {
					case "method":
						method = "PUT"
						want = 405
					case "missing_route":
						path += "extra/other/"
						want = 404
					case "accept":
						headers.Set("Accept", "application/xml")
						want = 406
					case "csrf":
						headers.Del(sessionauth.DefaultCSRFHeader)
						body = strings.ReplaceAll(body, token, "bad")
						want = 403
					case "origin":
						headers.Set("Origin", "https://attacker.test")
						want = 403
					case "query":
						path += "?unexpected=1"
					case "empty_query":
						path += "?"
					case "unknown_member":
						if surface == "form" {
							body += "&target=manager"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"target":"manager"}`
						}
					case "duplicate":
						if surface == "form" {
							body += "&new_password1=wrong"
							want = 200
						} else {
							body = strings.TrimSuffix(body, "}") + `,"new_password":"wrong"}`
						}
					case "null":
						if surface == "form" {
							body = resetAccountForm("\x00", "\x00", token)
							want = 200
						} else {
							body = `{"new_password":null}`
						}
					case "oversize_string":
						if surface == "form" {
							body = resetAccountForm(strings.Repeat("x", 4097), selfPassword, token)
							want = 413
						} else {
							body = `{"new_password":"` + strings.Repeat("x", 4097) + `"}`
						}
					case "oversize_body":
						body = strings.Repeat("x", 65537)
						want = 413
					case "media":
						media = "text/plain"
						want = 415
					case "wrong_target", "malformed_uid", "padded_uid":
						parts := strings.Split(path, "/")
						index := 3
						if surface == "json" {
							index = 4
						}
						switch mode {
						case "wrong_target":
							parts[index] = base64.RawURLEncoding.EncodeToString([]byte(f.actor.Principal().ID()))
						case "malformed_uid":
							parts[index] = "not.base64"
						case "padded_uid":
							parts[index] += "="
						}
						path = strings.Join(parts, "/")
						want = 200
						if surface == "json" {
							want = 403
						}
					case "missing_proof":
						client = h.newClient(t)
						base, _ := url.Parse(h.server.URL)
						for _, cookie := range h.client.Jar.Cookies(base) {
							if cookie.Name == sessionauth.DefaultCSRFCookieName {
								client.Jar.SetCookies(base, []*http.Cookie{cookie})
							}
						}
						want = 200
						if surface == "json" {
							want = 403
						}
					case "missing_password":
						if surface == "form" {
							body = url.Values{"csrfmiddlewaretoken": {token}}.Encode()
							want = 200
						} else {
							body = `{}`
						}
					case "weak":
						if surface == "form" {
							body = resetAccountForm("short", "short", token)
							want = 200
						} else {
							body = `{"new_password":"short"}`
						}
					}
					response, body := h.send(t, client, method, path, body, media, headers)
					resetHTTPHeaders(t, response)
					if response.StatusCode != want {
						t.Fatal("wrong reset refusal", mode, response.StatusCode, body)
					}
					if surface == "json" && (mode == "wrong_target" || mode == "missing_proof" || mode == "malformed_uid" || mode == "padded_uid") {
						var payload struct{ Code string }
						if err := json.Unmarshal([]byte(body), &payload); err != nil || payload.Code != "invalid_reset_link" {
							t.Fatal("JSON reset refusal lost its public proof classification", err)
						}
					}
					if strings.Contains(body, selfPassword) || f.owner.hasher.hashes.Load() != 0 || len(h.reports) != 0 {
						t.Fatal("refusal leaked password, hashed or reported input as execution failure")
					}
					if f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy || len(response.Cookies()) != 0 {
						t.Fatal("reset refusal performed password/persistence work or published a cookie")
					}
					assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				})
			}
			if surface == "form" {
				t.Run("django_reference", func(t *testing.T) { accountResetFormReference(t, h, reopen, link, token, before, entropy) })
				t.Run("raw_token_post", func(t *testing.T) {
					response, _ := h.send(t, h.client, "POST", link.raw, resetAccountForm(selfPassword, selfPassword, token), "application/x-www-form-urlencoded", nil)
					resetHTTPHeaders(t, response)
					if response.StatusCode != 302 || response.Header.Get("Location") != link.form || len(response.Cookies()) != 1 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || len(h.reports) != 0 || f.owner.boundary.calls != 1 {
						t.Fatal("raw-token POST failed hidden exchange or reset the account")
					}
					after := userReferenceSnapshot(t, reopen(t))
					expected, wanted := resetEntryExpected(t, f, before, after, proof, f.entropy.one(t, entropy), link.token)
					if response.Cookies()[0].Name != sessionauth.DefaultSessionCookieName || response.Cookies()[0].Value != wanted.ID().Encoded() || !reflect.DeepEqual(h.current(t, h.client).Snapshot(), wanted.Snapshot()) {
						t.Fatal("raw-token POST published a different browser proof")
					}
					assertUserReferenceState(t, expected, after, f.actor.Principal())
				})
			}
		})
	}
}
