package accountstoretest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/web/sessionauth"
)

func accountResetRequestRefusals(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			memory := selectedResetMemory(t)
			h := newResetAccountHTTP(t, f, backend, memory, nil)
			token, _ := h.bootstrap(t, h.client, surface)
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			for _, mode := range []string{"missing_csrf", "duplicate_csrf", "foreign_origin", "invalid", "empty", "missing", "nul", "email_length", "duplicate_email", "unknown", "wrong_media", "body_limit", "input_limit", "query", "empty_query"} {
				t.Run(mode, func(t *testing.T) {
					email := f.target.Email
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					path, media := "/account/reset/", "application/x-www-form-urlencoded"
					want := 200
					if surface == "json" {
						path, media, want = "/api/account/reset/", "application/json", 400
					}
					switch mode {
					case "invalid":
						email = `"><script>alert(1)</script>`
					case "empty":
						email = "\u2002 "
					case "missing":
						email = ""
					case "nul":
						email = "bad\x00@example.test"
					case "email_length":
						email = strings.Repeat("x", 255) + "@example.test"
					case "input_limit":
						email = strings.Repeat("x", 4097)
						if surface == "form" {
							want = 413
						}
					}
					body := url.Values{"email": {email}, "csrfmiddlewaretoken": {token}}.Encode()
					if surface == "json" {
						encoded, _ := json.Marshal(map[string]string{"email": email})
						body = string(encoded)
					}
					switch mode {
					case "missing_csrf":
						headers.Del(sessionauth.DefaultCSRFHeader)
						body = strings.ReplaceAll(body, token, "invalid")
						want = 403
					case "duplicate_csrf":
						headers.Add(sessionauth.DefaultCSRFHeader, token)
						want = 403
					case "foreign_origin":
						headers.Set("Origin", "https://attacker.test")
						want = 403
					case "missing":
						if surface == "form" {
							body = url.Values{"csrfmiddlewaretoken": {token}}.Encode()
						} else {
							body = `{}`
						}
					case "duplicate_email":
						if surface == "form" {
							body += "&email=other%40example.test"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"email":"other@example.test"}`
						}
					case "unknown":
						if surface == "form" {
							body += "&extra=1"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"extra":1}`
						}
						want = 400
					case "wrong_media":
						media = "text/plain"
						want = 415
					case "body_limit":
						body = strings.Repeat("x", 65537)
						want = 413
					case "query":
						path += "?email=other"
						want = 400
					case "empty_query":
						path += "?"
						want = 400
					}
					response, body := h.send(t, h.client, "POST", path, body, media, headers)
					resetHTTPHeaders(t, response)
					if response.StatusCode != want || strings.Contains(body, "<script>") || len(h.reports) != 0 || f.owner.hasher.hashes.Load() != 0 {
						t.Fatal("invalid email request admitted, unsafe or misclassified", mode, response.StatusCode, body)
					}
					messages, err := memory.Snapshot()
					if err != nil || len(messages) != 0 {
						t.Fatal("invalid email request sent mail", err)
					}
					if f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy || len(response.Cookies()) != 0 {
						t.Fatal("invalid reset request performed work or published a cookie")
					}
					assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				})
			}
		})
	}
}
