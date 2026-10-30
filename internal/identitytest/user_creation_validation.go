package identitytest

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	identityadmin "github.com/progresshans/godj/identity/admin"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

//go:embed testdata/user-creation-*.json
var userCreationReference embed.FS

type creationCase struct{ Name, Username, Password1, Password2 string }
type creationObservation struct {
	Valid     bool
	Codes     map[string][]string
	Username  *string   `json:"cleaned_username"`
	Profiles  []*string `json:"policy_profiles"`
	UserDelta int       `json:"user_delta"`
}

func RunUserCreationValidation(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	input, err := userCreationReference.ReadFile("testdata/user-creation-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []creationCase
	if err := json.Unmarshal(input, &cases); err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Unicode string
		InputSHA                string `json:"input_sha256"`
		Observations            map[string]map[string]creationObservation
	}
	var first []byte
	for _, backend := range []string{"sqlite", "postgres"} {
		payload, err := userCreationReference.ReadFile("testdata/user-creation-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(payload, &reference); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(input)
		if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Unicode != "16.0.0" || reference.InputSHA != hex.EncodeToString(sum[:]) {
			t.Fatal("unbound creation reference")
		}
		observations, err := json.Marshal(reference.Observations)
		if err != nil {
			t.Fatal(err)
		}
		if first != nil && string(first) != string(observations) {
			t.Fatal("native backend results differ")
		}
		first = observations
	}
	for _, mode := range []string{"admin_enabled", "admin_disabled"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			if _, err := f.manager(t, f.runtime).CreateUserWithUnusablePassword(t.Context(), f.actor, identity.NewUserCreate("invalid-name-seed", "bad name")); err != nil {
				t.Fatal(err)
			}
			minimum, err := identity.NewMinimumLengthValidator(8)
			if err != nil {
				t.Fatal(err)
			}
			similarity, err := identity.NewUserAttributeSimilarityValidator(identity.SimilarityConfig{Attributes: []string{"username"}})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var profiles []string
			policy := identity.PasswordValidatorFunc(func(_ string, profile identity.Profile) validation.Errors {
				mu.Lock()
				defer mu.Unlock()
				profiles = append(profiles, profile.Username)
				return validation.Errors{}
			})
			var ids atomic.Int64
			h := newIdentityAdminHTTP(t, f, f.runtime, func(config identityadmin.Config) identityadmin.Config {
				return config.WithPasswordValidators(policy, minimum, similarity).WithPrincipalIDs(func(context.Context) (string, error) { return fmt.Sprintf("creation-%d", ids.Add(1)), nil })
			})
			h.loginAdmin(t, h.client, "manager", managementOldPassword)
			if len(reference.Observations[mode]) != len(cases) {
				t.Fatal("reference omitted a case")
			}
			for _, input := range cases {
				t.Run(input.Name, func(t *testing.T) {
					want, found := reference.Observations[mode][input.Name]
					if !found || want.UserDelta != 0 {
						t.Fatal("native validation performed a write or omitted input")
					}
					mu.Lock()
					profiles = nil
					mu.Unlock()
					beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
					hashes, allocated := f.hasher.calls.Load(), ids.Load()
					count, err := models.UserObjects.Using(backend).Count(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					data := url.Values{"username": {input.Username}, "password1": {input.Password1}, "password2": {input.Password2}}
					if mode == "admin_disabled" {
						data.Set("unusable_password", "on")
					}
					status := 200
					if want.Valid {
						status = 302
					}
					response := h.postForm(t, "/admin/users/add/", data, status)
					codes := map[string][]string{}
					for _, match := range regexp.MustCompile(`data-error-field="([^"]+)" data-error-code="([^"]+)"`).FindAllStringSubmatch(response.body, -1) {
						codes[match[1]] = append(codes[match[1]], match[2])
					}
					if !reflect.DeepEqual(codes, want.Codes) {
						t.Fatalf("error phases differ: got %v, native %v", codes, want.Codes)
					}
					mu.Lock()
					gotProfiles := append([]string{}, profiles...)
					mu.Unlock()
					wantProfiles := []string{}
					for _, value := range want.Profiles {
						text := ""
						if value != nil {
							text = *value
						}
						wantProfiles = append(wantProfiles, text)
					}
					if want.Valid && mode == "admin_enabled" {
						// Go repeats pure policy at management preflight and the final
						// write fence; the form's first profile must match native.
						wantProfiles = append(wantProfiles, *want.Username, *want.Username)
					}
					if !reflect.DeepEqual(gotProfiles, wantProfiles) {
						t.Fatalf("candidate profile/phase differs: %q != %q", gotProfiles, wantProfiles)
					}
					wantHash, wantIDs := int64(0), int64(0)
					if want.Valid {
						wantIDs = 1
						count++
						if mode == "admin_enabled" {
							wantHash = 1
						}
						row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.Username.Exact(*want.Username)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
						if err != nil || !found || row.Revision != 1 || !row.Active || row.Staff || row.Superuser {
							t.Fatal("creation defaults/persistence", err)
						}
						_, authErr := f.runtime.Authenticator().Authenticate(t.Context(), row.Username, input.Password1)
						if mode == "admin_enabled" && authErr != nil || mode == "admin_disabled" && !errors.Is(authErr, auth.ErrInvalidCredentials) {
							t.Fatal("password outcome", authErr)
						}
					} else {
						afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
						if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
							t.Fatal("invalid form changed sessions/audit")
						}
					}
					if f.hasher.calls.Load()-hashes != wantHash || ids.Load()-allocated != wantIDs {
						t.Fatal("invalid form allocated identity or performed password work")
					}
					if got, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || got != count {
						t.Fatal("unexpected user write", got, count, err)
					}
					response.excludes(t, `type="password" name="password1" value=`, `type="password" name="password2" value=`)
				})
			}
		})
	}
	runCreationReadBoundaries(t, open)
}

func runCreationReadBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("current_authority_precedes_diagnostics", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		if _, err := models.UserObjects.Patch(t.Context(), backend, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2)); err != nil {
			t.Fatal(err)
		}
		calls := 0
		policy := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
			calls++
			return validation.NewErrors(validation.New("password", "private_policy"))
		})
		manager, err := identity.NewManager(f.runtime, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(policy))
		if err != nil {
			t.Fatal(err)
		}
		username, password := "MEMBER", "weak"
		err = manager.CheckUserCreation(t.Context(), f.actor, &username, &password)
		if !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || calls != 0 || f.hasher.calls.Load() != 0 {
			t.Fatal("stale actor obtained input diagnostics", err, calls)
		}
	})
	t.Run("read_failures_are_not_input", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
		for _, mode := range []string{"read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure", "canceled"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boundary := &adminSelectionBoundary{Runtime: f.runtime, mode: mode}
				if mode == "canceled" {
					boundary.cancel = cancel
				}
				policy := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
					return validation.NewErrors(validation.New("password", "policy_rejected"))
				})
				manager, err := identity.NewManager(boundary, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(policy))
				if err != nil {
					t.Fatal(err)
				}
				username, password := "MEMBER", "weak"
				err = manager.CheckUserCreation(ctx, f.actor, &username, &password)
				if _, rejected := validation.Rejected(err); err == nil || rejected {
					t.Fatal("failed snapshot became input", err)
				}
				if mode == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation lost", err)
				}
			})
		}
		afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
		if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
			t.Fatal("read-only checks mutated state")
		}
	})
	for _, mode := range []string{"revoked_authority", "duplicate_after_hash"} {
		t.Run(mode, func(t *testing.T) {
			backend, second := open(t)
			f := newManagementFixture(t, backend, 0)
			h := newIdentityAdminHTTP(t, f, f.runtime)
			h.loginAdmin(t, h.client, "manager", managementOldPassword)
			f.hasher.hook = func(ctx context.Context) error {
				return second.CoordinatedAtomic(ctx, func(session db.Session) error {
					if mode == "revoked_authority" {
						_, err := models.UserObjects.Patch(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
						return err
					}
					_, err := models.UserObjects.Create(ctx, session, models.NewUserCreate("competing-user", "CANDIDATE", f.user.EncodedPassword, f.user.DateJoined))
					return err
				})
			}
			status := 200
			if mode == "revoked_authority" {
				status = 403
			}
			response := h.postForm(t, "/admin/users/add/", url.Values{"username": {"Candidate"}, "password1": {managementNewPassword}, "password2": {managementNewPassword}}, status)
			if mode == "duplicate_after_hash" {
				response.contains(t, `data-error-field="username" data-error-code="unique"`)
			}
			if f.hasher.calls.Load() != 1 {
				t.Fatal("final failure retried or skipped hash")
			}
			if exists, err := models.UserObjects.Using(backend).Filter(models.UserFields.Username.Exact("Candidate")).Exists(t.Context()); err != nil || exists {
				t.Fatal("stale form authorization persisted", err)
			}
		})
	}
}
