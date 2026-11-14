package accountstoretest

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web/sessionauth"
)

func accountResetFailureModes() []string {
	modes := []string{"password_write", "after_password", "after_session", "after_rotation_delete", "audit_insert", "revoke", "unknown_rollback", "unknown_commit", "cancel", "validation", "validation_cleanup", "policy_failure", "self_unknown_commit", "entry_unknown_commit", "entry_after_session", "check_read", "check_proof_error", "check_diagnostics_error", "finish_read", "finish_proof_error", "finish_diagnostics_error"}
	for _, point := range []string{"before_password", "after_password"} {
		for _, kind := range []string{"entropy", "proof", "diagnostics"} {
			modes = append(modes, point+"_"+kind)
		}
	}
	return modes
}

func accountResetFailures(t *testing.T, open Open) {
	for _, surface := range []string{"form", "json"} {
		t.Run(surface, func(t *testing.T) {
			for _, mode := range accountResetFailureModes() {
				t.Run(mode, func(t *testing.T) {
					backend, reopen := open(t)
					f := provisionSelfPassword(t, backend)
					private := errors.New("private selected reset HTTP failure")
					var fault error = private
					point := map[string]string{"password_write": "before_password", "after_rotation_delete": "after_rotation", "audit_insert": "before_audit", "revoke": "after_revoke", "cancel": "callback_cancel", "validation_cleanup": "cleanup_failure", "self_unknown_commit": "unknown_commit", "entry_unknown_commit": "unknown_commit", "entry_after_session": "after_session"}[mode]
					if point == "" {
						point = mode
					}
					for _, kind := range []string{"entropy", "proof", "diagnostics"} {
						if strings.HasSuffix(mode, "_"+kind) {
							point = strings.TrimSuffix(mode, "_"+kind)
							switch kind {
							case "entropy":
								fault = &sessions.Error{Code: sessions.CodeEntropy}
							case "proof":
								fault = auth.ErrInvalidResetProof
							case "diagnostics":
								fault = validation.Reject(validation.NewErrors(validation.New("password", "native_failure")), nil)
							}
						}
					}
					if strings.HasSuffix(mode, "_proof_error") {
						fault = auth.ErrInvalidResetProof
					}
					if strings.HasSuffix(mode, "_diagnostics_error") {
						fault = validation.Reject(validation.NewErrors(validation.New("password", "native_read_failure")), nil)
					}
					readFailure := strings.HasPrefix(mode, "check_") || strings.HasPrefix(mode, "finish_")
					readOwner := &resetAccountReadFault{Backend: backend, fault: fault}
					boundary := &profileFaultBackend{Backend: readOwner, fault: fault}
					armed := false
					var effects []string
					faults := 0
					boundary.decorate = func(scope db.Session) db.Session {
						if armed {
							return &passwordWriteFaultScope{Session: scope, mode: point, fault: fault, effects: &effects, faults: &faults}
						}
						return scope
					}
					policy := func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
						if armed && (mode == "validation" || mode == "validation_cleanup" || mode == "policy_failure") {
							diagnostics := validation.NewErrors(validation.New("password", "password_too_similar"))
							if mode == "policy_failure" {
								return diagnostics, private
							}
							return diagnostics, nil
						}
						return validation.Errors{}, nil
					}
					memory := selectedResetMemory(t)
					h := newResetAccountHTTP(t, f, boundary, memory, policy)
					kind := "anonymous"
					if mode == "self_unknown_commit" {
						kind = "self"
					}
					if mode == "entry_unknown_commit" {
						kind = "missing"
					}
					previous := resetBrowserPrevious(t, f, kind)
					entry := strings.HasPrefix(mode, "entry_")
					var link resetHTTPLink
					var proof sessions.Record
					var csrf string
					if entry {
						resetClientSession(t, h.accountHTTP, h.client, previous)
						response, _ := h.requestMail(t, h.client, surface, f.target.Email)
						resetHTTPHeaders(t, response)
						want := 302
						if surface == "json" {
							want = 204
						}
						if response.StatusCode != want || len(h.reports) != 0 {
							t.Fatal("entry failure fixture did not request mail")
						}
						link = resetDeliveredLink(t, memory)
					} else {
						link, proof, csrf = resetAccountSetup(t, h, reopen, memory, surface, previous)
					}
					before := userReferenceSnapshot(t, reopen(t))
					entropy := f.entropy.count()
					f.owner.boundary.calls, boundary.calls = 0, 0
					f.owner.hasher.hashes.Store(0)
					f.owner.hasher.verifies.Store(0)
					f.owner.policies.Store(0)
					oldCookie := resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultSessionCookieName)
					csrfCookie := resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultCSRFCookieName)
					f.instant.Add(int64(time.Minute))
					readOwner.active = readFailure
					if entry {
						armed = true
						boundary.mode = point
					} else if !readFailure {
						f.owner.hasher.beforeHash = func(context.Context) error { armed = true; boundary.mode = point; return nil }
					}
					method, path, input, media, headers := "POST", "", "", "", http.Header(nil)
					if entry {
						method, path = "GET", link.raw
					} else if strings.HasPrefix(mode, "check_") {
						method, path = "GET", link.form
						if surface == "json" {
							path = link.json
						}
					} else {
						path, input, media, headers = resetCompleteRequest(t, surface, link, selfPassword, csrf)
					}
					response, body := h.send(t, h.client, method, path, input, media, headers)
					resetHTTPHeaders(t, response)
					unknown := strings.Contains(mode, "unknown_")
					wantStatus := 500
					if unknown {
						wantStatus = 503
					}
					if mode == "validation" {
						wantStatus = 200
						if surface == "json" {
							wantStatus = 400
						}
					}
					wantReports := 1
					if mode == "validation" {
						wantReports = 0
					}
					if response.StatusCode != wantStatus || len(response.Cookies()) != 0 || response.Header.Get("Retry-After") != "" || len(h.reports) != wantReports {
						t.Fatal("reset HTTP failure was exposed as input, retried or published", mode, response.StatusCode, wantStatus)
					}
					if surface == "json" && unknown && !entry && !strings.Contains(body, "\"code\":\"outcome_unknown\"") {
						t.Fatal("reset JSON lost uncertain outcome code")
					}
					if mode == "validation" && !strings.Contains(body, "password_too_similar") {
						t.Fatal("confirmed reset policy refusal lost field diagnostics")
					}
					hashes, policies := int64(1), int64(2)
					writes := 1
					entropyDelta := 1
					if entry {
						hashes, policies = 0, 0
					}
					if readFailure {
						hashes, policies, writes, entropyDelta = 0, 0, 0, 0
					}
					if mode == "self_unknown_commit" {
						entropyDelta = 0
					}
					if f.owner.hasher.hashes.Load() != hashes || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != policies || f.owner.boundary.calls != writes || boundary.calls != writes || f.entropy.count() != entropy+entropyDelta {
						t.Fatal("reset HTTP failure repeated hash, policy, entropy or native writes", mode)
					}
					if readFailure && readOwner.reads != 1 {
						t.Fatal("reset HTTP retried a failed read")
					}
					var id sessions.ID
					if entropyDelta == 1 {
						id = f.entropy.one(t, entropy)
					}
					material := []string{selfPassword, f.target.EncodedPassword, link.token, proof.ID().Encoded(), id.Encoded(), private.Error()}
					if hashes == 1 {
						material = append(material, f.owner.encoded.Load().(string))
					}
					for _, secret := range material {
						if secret != "" && strings.Contains(body, secret) {
							t.Fatal("reset HTTP failure disclosed private material")
						}
					}
					if wantReports == 1 {
						reported := h.reports[0]
						selfPasswordPrivate(t, reported, material...)
						if mode == "cancel" {
							if !errors.Is(reported, context.Canceled) {
								t.Fatal("reset report lost cancellation")
							}
						} else if unknown {
							if !errors.Is(reported, &query.Error{Code: query.CodeCommitOutcomeUnknown}) && !errors.Is(reported, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
								t.Fatal("reset report lost unknown outcome")
							}
						} else if !errors.Is(reported, fault) {
							t.Fatal("reset report lost its exact native cause", mode)
						}
					}
					var expectedEffects []string
					if !readFailure && mode != "validation" && mode != "validation_cleanup" && mode != "policy_failure" {
						if entry {
							if previous.ID().Valid() {
								expectedEffects = append(expectedEffects, "rotation")
							}
							expectedEffects = append(expectedEffects, "session")
						} else {
							if mode != "self_unknown_commit" {
								expectedEffects = append(expectedEffects, "rotation", "session")
							}
							expectedEffects = append(expectedEffects, "password")
							for range len(f.targetSessions) {
								expectedEffects = append(expectedEffects, "revoke")
							}
							expectedEffects = append(expectedEffects, "audit")
						}
					}
					if strings.HasPrefix(point, "before_") || strings.HasPrefix(point, "after_") {
						name := strings.TrimPrefix(strings.TrimPrefix(point, "before_"), "after_")
						index := slices.Index(expectedEffects, name)
						if index < 0 {
							t.Fatal("unknown reset HTTP write prefix", point)
						}
						if strings.HasPrefix(point, "after_") {
							index++
						}
						expectedEffects = expectedEffects[:index]
						if faults != 1 {
							t.Fatal("reset HTTP fault did not execute once")
						}
					} else if faults != 0 {
						t.Fatal("reset HTTP injected an unexpected DML failure")
					}
					if !slices.Equal(effects, expectedEffects) {
						t.Fatal("reset HTTP missed its exact native write prefix", effects, expectedEffects)
					}
					after := userReferenceSnapshot(t, reopen(t))
					committed := mode == "unknown_commit" || mode == "self_unknown_commit" || mode == "entry_unknown_commit"
					if committed {
						if entry {
							expected, _ := resetEntryExpected(t, f, before, after, previous, id, link.token)
							assertUserReferenceState(t, expected, after, f.actor.Principal())
						} else {
							expected, _ := resetFinishExpected(t, f, f.owner, before, after, proof, id)
							assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
						}
					} else if !reflect.DeepEqual(before, after) {
						t.Fatal("failed reset HTTP request changed native/host state")
					}
					if resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultSessionCookieName) != oldCookie || resetClientCookie(t, h.accountHTTP, h.client, sessionauth.DefaultCSRFCookieName) != csrfCookie {
						t.Fatal("failed or uncertain reset HTTP changed browser cookies")
					}
				})
			}
		})
	}
}
