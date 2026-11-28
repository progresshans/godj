package accountstoretest

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountPasswordRefusals(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			h := newAccountHTTP(t, f, backend, nil)
			original := h.login(t, h.client, password)
			response, _ := h.send(t, h.client, "GET", "/api/account/csrf/", "", "", nil)
			token := response.Header.Get(sessionauth.DefaultCSRFHeader)
			h.now.Add(int64(time.Minute))
			before := userReferenceSnapshot(t, reopen(t))
			cases := []string{"csrf", "origin", "query", "empty_query", "unknown", "duplicate", "null", "wrong_type", "oversize_string", "oversize_body", "media", "wrong_old_weak", "duplicate_cookie", "stale", "expired", "anonymous"}
			for _, mode := range cases {
				t.Run(mode, func(t *testing.T) {
					headers := make(http.Header)
					headers.Set(sessionauth.DefaultCSRFHeader, token)
					path, body, media := "/api/account/password/", `{"old_password":"`+password+`","new_password":"`+selfPassword+`"}`, "application/json"
					if surface == "form" {
						path = "/account/password/"
						media = "application/x-www-form-urlencoded"
						body = accountForm(password, selfPassword, selfPassword, token).Encode()
					}
					want := 400
					client := h.client
					switch mode {
					case "csrf":
						headers.Del(sessionauth.DefaultCSRFHeader)
						body = strings.ReplaceAll(body, token, "bad")
						want = 403
					case "origin":
						headers.Set("Origin", "https://attacker.example")
						want = 403
					case "query":
						path += "?unexpected=1"
					case "empty_query":
						path += "?"
					case "unknown":
						if surface == "form" {
							body += "&target=manager"
						} else {
							body = strings.TrimSuffix(body, "}") + `,"target":"manager"}`
						}
					case "duplicate":
						if surface == "form" {
							body += "&old_password=wrong"
							want = 200
						} else {
							body = strings.TrimSuffix(body, "}") + `,"old_password":"wrong"}`
						}
					case "null":
						if surface == "form" {
							body += "&old_password=%00"
							want = 200
						} else {
							body = `{"old_password":null,"new_password":"next"}`
						}
					case "wrong_type":
						if surface == "form" {
							body += "&new_password1=duplicate"
							want = 200
						} else {
							body = `{"old_password":17,"new_password":"next"}`
						}
					case "oversize_string":
						if surface == "form" {
							body = accountForm(password, strings.Repeat("x", 4097), selfPassword, token).Encode()
							want = 413
						} else {
							body = `{"old_password":"` + strings.Repeat("x", 4097) + `","new_password":"next"}`
						}
					case "oversize_body":
						body = strings.Repeat("x", 65537)
						want = 413
					case "media":
						media = "text/plain"
						want = 415
					case "wrong_old_weak":
						if surface == "form" {
							body = accountForm("wrong", "short", "short", token).Encode()
							want = 200
						} else {
							body = `{"old_password":"wrong","new_password":"short"}`
						}
					case "duplicate_cookie":
						headers.Add("Cookie", sessionauth.DefaultSessionCookieName+"="+original.ID().Encoded())
						want = 403
						if surface == "form" {
							want = 302
						}
					case "stale", "expired", "anonymous":
						client = h.newClient(t)
						base, _ := url.Parse(h.server.URL)
						if mode != "anonymous" {
							id := original.ID()
							if mode == "stale" {
								record, err := h.manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.target.PrincipalID, auth.SessionCredentialStampKey: "stale"})
								if err != nil {
									t.Fatal(err)
								}
								id = record.ID()
								defer func() {
									if err := h.manager.Delete(t.Context(), id); err != nil {
										t.Fatal(err)
									}
								}()
							}
							client.Jar.SetCookies(base, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: id.Encoded(), Path: "/"}})
						}
						if mode == "expired" {
							h.now.Add(int64(400 * 24 * time.Hour))
							defer h.now.Add(-int64(400 * 24 * time.Hour))
						}
						want = 403
						if surface == "form" {
							want = 302
						}
					}
					// The stale-session fixture itself is an explicit setup write.
					expected := userReferenceSnapshot(t, reopen(t))
					response, body = h.send(t, client, "POST", path, body, media, headers)
					if response.StatusCode != want || len(response.Cookies()) != 0 {
						t.Fatal("refusal response", surface, mode, response.StatusCode, want, body)
					}
					if f.owner.hasher.hashes.Load() != 0 {
						t.Fatal("preflight hashed", mode)
					}
					if !reflect.DeepEqual(expected, userReferenceSnapshot(t, reopen(t))) {
						t.Fatal("refusal mutated durable state", mode)
					}
				})
			}
			if !reflect.DeepEqual(before, userReferenceSnapshot(t, reopen(t))) {
				t.Fatal("fixture cleanup changed baseline")
			}
		})
	}
}
