package identitytest

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	identityadmin "github.com/progresshans/godj/identity/admin"
	identityapi "github.com/progresshans/godj/identity/api"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

func RunPasswordValidators(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	for _, channel := range []string{"service", "admin", "api"} {
		for _, operation := range []string{"create", "password"} {
			t.Run(channel+"/"+operation, func(t *testing.T) {
				backend, other := open(t)
				f := newManagementFixture(t, backend, 3)
				validators, err := identity.DefaultPasswordValidators()
				if err != nil {
					t.Fatal(err)
				}
				manager, err := identity.NewManager(f.runtime, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(validators...))
				if err != nil {
					t.Fatal(err)
				}
				var adminHTTP *identityAdminHTTP
				var apiHTTP *identityHTTP
				switch channel {
				case "admin":
					adminHTTP = newIdentityAdminHTTP(t, f, f.runtime, func(config identityadmin.Config) identityadmin.Config {
						return config.WithPasswordValidators(validators...)
					})
					adminHTTP.loginAdmin(t, adminHTTP.client, "manager", managementOldPassword)
				case "api":
					apiHTTP, _ = newManagementHTTP(t, f, f.runtime, validators...)
				}
				// Both adapters must own their policy slice after registration.
				for i := range validators {
					validators[i] = nil
				}
				similar := "candidate12"
				if operation == "password" {
					similar = "member12"
				}
				for _, probe := range []struct {
					name, password string
					codes          []string
				}{
					{"short", "Ａ９", []string{"password_too_short"}},
					{"common", " PASSWORD ", []string{"password_too_common"}},
					{"numeric", "¹²³⁴⁵⁶⁷⁸", []string{"password_entirely_numeric"}},
					{"similarity", similar, []string{"password_too_similar"}},
					{"combined", "123", []string{"password_too_short", "password_too_common", "password_entirely_numeric"}},
				} {
					t.Run(probe.name, func(t *testing.T) {
						var codes []string
						switch channel {
						case "service":
							var id int64
							if operation == "create" {
								value, failure := manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("candidate", "Candidate"), probe.password)
								id, err = value.ID, failure
							} else {
								value, failure := manager.SetPassword(t.Context(), f.actor, f.user.ID, 1, probe.password)
								id, err = value.ID, failure
							}
							failures, rejected := validation.Rejected(err)
							if !rejected || id != 0 {
								t.Fatal("policy rejection lost", err)
							}
							for _, failure := range failures.All() {
								if failure.Field() != "password" {
									t.Fatal("wrong policy field")
								}
								codes = append(codes, string(failure.Code()))
							}
						case "admin":
							path := adminObjectPath("users", "command/password", f.user.ID)
							if operation == "create" {
								path = "/admin/users/add/"
							}
							values := url.Values{"password1": {probe.password}, "password2": {probe.password}}
							if operation == "create" {
								values.Set("username", "Candidate")
							}
							response := adminHTTP.postForm(t, path, values, 200)
							for _, match := range regexp.MustCompile(`data-error-field="password2" data-error-code="([^"]+)"`).FindAllStringSubmatch(response.body, -1) {
								codes = append(codes, match[1])
							}
							fields := regexp.MustCompile(`<input\b[^>]*\bname="(?:password1|password2)"[^>]*>`).FindAllString(response.body, -1)
							if len(fields) != 2 {
								t.Fatal("private password widgets missing")
							}
							for _, field := range fields {
								if strings.Contains(field, `value="`) && !strings.Contains(field, `value=""`) {
									t.Fatal("password was redisplayed")
								}
							}
						case "api":
							payload := map[string]any{"password": probe.password}
							path, revision := apiPath("users", f.user.ID)+"password/", "1"
							if operation == "create" {
								path, revision = identityapi.BasePath+"users/", ""
								payload["username"] = "Candidate"
							}
							data, _ := json.Marshal(payload)
							_, body := managementCall(t, apiHTTP, apiHTTP.client, "POST", path, string(data), revision, true, 400)
							var failures []struct{ Field, Code string }
							if err := json.Unmarshal(body["errors"], &failures); err != nil {
								t.Fatal(err)
							}
							for _, failure := range failures {
								if failure.Field != "password" {
									t.Fatal("API policy field changed")
								}
								codes = append(codes, failure.Code)
							}
							encoded, _ := json.Marshal(body)
							if strings.Contains(string(encoded), probe.password) {
								t.Fatal("API response leaked rejected password")
							}
						}
						if !slices.Equal(codes, probe.codes) || f.hasher.calls.Load() != 0 {
							t.Fatal("policy order/hash boundary", codes, f.hasher.calls.Load())
						}
						if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
							t.Fatal("rejected policy wrote user", err)
						}
						if !reflect.DeepEqual(f.stored(t), f.user) {
							t.Fatal("rejected policy changed profile")
						}
						f.assertOutcome(t, false)
					})
				}
				// Successful policies leave the exact secret intact, including
				// spaces, and enter the ordinary atomic credential lifecycle.
				username := "Candidate"
				if operation == "password" {
					username = f.user.Username
				}
				switch channel {
				case "service":
					if operation == "create" {
						_, err = manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("candidate", "Candidate"), managementNewPassword)
					} else {
						_, err = manager.SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
					}
					if err != nil {
						t.Fatal(err)
					}
				case "admin":
					path := adminObjectPath("users", "command/password", f.user.ID)
					values := url.Values{"password1": {managementNewPassword}, "password2": {managementNewPassword}}
					if operation == "create" {
						path = "/admin/users/add/"
						values.Set("username", "Candidate")
					}
					adminHTTP.postForm(t, path, values, 302)
				case "api":
					path, revision, status := apiPath("users", f.user.ID)+"password/", "1", 200
					payload := map[string]any{"password": managementNewPassword}
					if operation == "create" {
						path, revision, status = identityapi.BasePath+"users/", "", 201
						payload["username"] = "Candidate"
					}
					data, _ := json.Marshal(payload)
					managementCall(t, apiHTTP, apiHTTP.client, "POST", path, string(data), revision, true, status)
				}
				if f.hasher.calls.Load() != 1 {
					t.Fatal("success did not hash exactly once")
				}
				f.assertOutcome(t, operation == "password")
				reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
				if err != nil {
					t.Fatal(err)
				}
				h := newIdentityHTTPWithRuntime(t, reopened)
				h.login(t, h.client, username, managementNewPassword, 200)
				if trimmed := strings.TrimSpace(managementNewPassword); trimmed != managementNewPassword {
					h.login(t, h.newClient(t), username, trimmed, 401)
				}
			})
		}
	}
	t.Run("policy_is_explicit_and_authorization_precedes_rejection", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		defaults, err := identity.DefaultPasswordValidators()
		if err != nil {
			t.Fatal(err)
		}
		manager, err := identity.NewManager(f.runtime, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(defaults...))
		if err != nil {
			t.Fatal(err)
		}
		account, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = manager.CreateUser(t.Context(), account.Principal(), identity.NewUserCreate("forbidden", "Forbidden"), "123"); !errors.Is(err, &identity.Error{Code: identity.CodePermission}) {
			t.Fatal("weak input hid current permission denial", err)
		}
		if _, err = manager.SetPassword(t.Context(), f.actor, f.user.ID, 2, "123"); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) {
			t.Fatal("weak input hid stale revision", err)
		}
		if f.hasher.calls.Load() != 0 {
			t.Fatal("denied request hashed")
		}
		value, err := f.manager(t, f.runtime).CreateUser(t.Context(), f.actor, identity.NewUserCreate("weak-but-explicitly-allowed", "WeakCandidate"), "1")
		if err != nil || value.ID == 0 || f.hasher.calls.Load() != 1 {
			t.Fatal("omitted policy silently added strength validation", err)
		}
		if _, err := f.runtime.Authenticator().Authenticate(t.Context(), "WeakCandidate", "1"); err != nil {
			t.Fatal("policy choice changed login", err)
		}
	})
	for _, mode := range []string{"final", "cleanup", "swallowed", "unknown"} {
		t.Run("current_profile/"+mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 3)
			validators, err := identity.DefaultPasswordValidators()
			if err != nil {
				t.Fatal(err)
			}
			boundary := &policyRollbackBoundary{Runtime: f.runtime, mode: mode}
			manager, err := identity.NewManager(boundary, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(validators...))
			if err != nil {
				t.Fatal(err)
			}
			const password = "Zebracascade"
			// Deliberate out-of-band profile edit without a revision increment:
			// the final policy must observe the current row even in this case.
			f.hasher.hook = func(ctx context.Context) error {
				_, err := models.UserObjects.Patch(ctx, other, f.user, models.UserPatch{}.WithFirstName(password))
				return err
			}
			beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
			profile, err := manager.SetPassword(t.Context(), f.actor, f.user.ID, 1, password)
			failures, rejected := validation.Rejected(err)
			if err == nil || profile.ID != 0 || f.hasher.calls.Load() != 1 || boundary.calls != 1 {
				t.Fatal("final built-in policy not enforced", err)
			}
			if mode == "final" {
				first, ok := failures.At(0)
				if !rejected || !ok || first.Code() != "password_too_similar" {
					t.Fatal("final profile rejection lost", err)
				}
			} else {
				if rejected {
					t.Fatal("failed rollback rendered as policy error")
				}
				code := identity.CodePersistence
				if mode == "unknown" {
					code = identity.CodeOutcomeUnknown
				}
				if !errors.Is(err, &identity.Error{Code: code}) {
					t.Fatal("rollback classification lost", err)
				}
			}
			afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
				t.Fatal("rejected policy affected sessions/audit")
			}
			if f.stored(t).FirstName != password {
				t.Fatal("foreign committed edit was rolled back")
			}
			f.assertOutcome(t, false)
		})
	}
}
