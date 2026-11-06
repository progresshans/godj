package identitytest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

// The remaining built-in validator consumer changes a profile during hashing
// and observes the real rollback's reported completion. Keep this boundary
// with that consumer until its current-profile checks move to the selected
// owner as well.
type validatorRollbackBoundary struct {
	*systemstate.Runtime
	mode  string
	calls int
}

func (boundary *validatorRollbackBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	boundary.calls++
	err := boundary.Runtime.CoordinatedAtomic(ctx, callback)
	if _, rejected := validation.Rejected(err); !rejected {
		return err
	}
	switch boundary.mode {
	case "cleanup":
		return errors.Join(err, errors.New("private-validator-fault"))
	case "swallowed":
		return nil
	case "unknown":
		return errors.Join(err, &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-validator-fault"})
	default:
		return err
	}
}

func RunPasswordValidators(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	for _, operation := range []string{"create", "password"} {
		t.Run("service/"+operation, func(t *testing.T) {
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
			// The manager owns its policy slice after construction.
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
			if operation == "create" {
				_, err = manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("candidate", "Candidate"), managementNewPassword)
			} else {
				_, err = manager.SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
			}
			if err != nil {
				t.Fatal(err)
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
			boundary := &validatorRollbackBoundary{Runtime: f.runtime, mode: mode}
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
