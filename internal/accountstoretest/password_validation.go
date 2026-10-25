package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordValidation(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	mode, calls := "", 0
	fault := errors.New("private password policy failure")
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{MaxPasswordBytes: 64, ValidatePassword: func(_ context.Context, raw string, row models.User) (validation.Errors, error) {
		calls++
		if row.EncodedPassword != "" || row.PrincipalID != f.user.PrincipalID {
			t.Fatal("password policy did not receive detached selected account")
		}
		diagnostics := validation.NewErrors(validation.New("password", "too_short"))
		switch mode {
		case "combined", "new_only":
			return diagnostics, nil
		case "policy_error":
			return diagnostics, fault
		case "wrapped_rejection":
			return validation.Errors{}, fmt.Errorf("wrapped: %w", validation.Reject(diagnostics, nil))
		case "caused_rejection":
			return validation.Errors{}, validation.Reject(diagnostics, fault)
		case "foreign_field":
			return validation.NewErrors(validation.New("encoded_password", "invalid")), nil
		}
		return validation.Errors{}, nil
	}})
	current := passwordSession(t, configured.manager, profileActor(t, f), "validation")
	for _, name := range []string{"combined", "new_only", "old_only", "nil_fields", "valid", "empty", "oversized", "policy_error", "wrapped_rejection", "caused_rejection", "foreign_field"} {
		t.Run(name, func(t *testing.T) {
			mode, calls = name, 0
			before := passwordState(t, backend)
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			old, next := password, password+"-new"
			oldField, newField := &old, &next
			switch name {
			case "combined":
				old = "wrong-password"
			case "new_only":
				oldField = nil
			case "old_only":
				newField = nil
				old = "wrong-password"
			case "nil_fields":
				oldField, newField = nil, nil
			case "empty":
				next = ""
			case "oversized":
				next = strings.Repeat("x", 65)
			}
			err := configured.owner.CheckPasswordChange(t.Context(), current.ID(), oldField, newField)
			switch name {
			case "nil_fields", "valid":
				if err != nil {
					t.Fatal("valid selected-field check", err)
				}
			case "policy_error", "wrapped_rejection", "caused_rejection", "foreign_field":
				requireProfileCode(t, err, accountmodel.CodePersistence)
				if _, rejected := validation.Rejected(err); rejected {
					t.Fatal("password policy execution failure became input")
				}
				if (name == "policy_error" || name == "caused_rejection") && !errors.Is(err, fault) {
					t.Fatal("policy cause lost", err)
				}
			default:
				diagnostics, rejected := validation.Rejected(err)
				if !rejected || errors.Unwrap(err) != nil {
					t.Fatal("password input diagnostics absent", err)
				}
				oldWant := name == "combined" || name == "old_only"
				newWant := name != "old_only"
				if diagnostics.ByField("old_password").Empty() == oldWant || diagnostics.ByField("password").Empty() == newWant {
					t.Fatal("selected-field diagnostics were not combined correctly")
				}
			}
			if oldField == nil && hasher.verifies.Load() != 0 || newField == nil && calls != 0 || hasher.hashes.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("password check performed a skipped or mutating operation")
			}
			for _, text := range []string{fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(text, fault.Error()) || strings.Contains(text, password) || strings.Contains(text, f.user.EncodedPassword) {
					t.Fatal("password check diagnostics leaked")
				}
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}

func passwordHasherFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	current := passwordSession(t, configured.manager, profileActor(t, f), "hash-failure")
	fault := errors.New("private password hash failure")
	for _, name := range []string{"failure", "invalid_input", "wrapped_invalid_input", "invalid_encoding", "unusable", "unchanged_hash", "canceled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			before := passwordState(t, backend)
			hasher.hashes.Store(0)
			tracked.writes.Store(0)
			hasher.encode = func(context.Context, string) (string, error) {
				switch name {
				case "failure":
					return "", fault
				case "invalid_input":
					return "", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
				case "wrapped_invalid_input":
					return "", fmt.Errorf("wrapped: %w", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"})
				case "invalid_encoding":
					return "invalid-password-encoding", nil
				case "unusable":
					return "!unusable", nil
				case "unchanged_hash":
					return f.user.EncodedPassword, nil
				case "canceled":
					cancel()
					return f.user.EncodedPassword, nil
				}
				return "", fault
			}
			result, err := configured.owner.ChangePassword(ctx, current.ID(), password, password+"-next")
			passwordNoResult(t, result, err)
			_, rejected := validation.Rejected(err)
			if rejected != (name == "invalid_input") {
				t.Fatal("hasher failure misclassified as input", err)
			}
			if name == "failure" && !errors.Is(err, fault) || name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("hash failure cause lost", err)
			}
			if hasher.hashes.Load() != 1 || tracked.writes.Load() != 0 {
				t.Fatal("hash failure retried or reached a write")
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}

func passwordSessionAdmission(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	for _, mode := range []string{"absent", "anonymous", "missing_stamp", "wrong_stamp", "inactive", "unusable", "missing_account", "idle_expired", "absolute_expired"} {
		t.Run(mode, func(t *testing.T) {
			now := joined
			target := roleTarget(t, backend, f, "password-admission-"+mode)
			credential := passwordCredential(t, f, target)
			tracked := &passwordScopeOwner{Backend: backend}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			values := map[string]string{auth.SessionPrincipalIDKey: credential.Principal().ID(), auth.SessionCredentialStampKey: credential.SessionStamp()}
			switch mode {
			case "anonymous":
				values = map[string]string{"anonymous": "no-auth"}
			case "missing_stamp":
				delete(values, auth.SessionCredentialStampKey)
			case "wrong_stamp":
				values[auth.SessionCredentialStampKey] = "wrong-stamp"
			}
			current, err := configured.manager.Create(t.Context(), values)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "absent":
				err = configured.store.Delete(t.Context(), current.ID())
			case "inactive":
				_, err = models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithActive(false))
			case "unusable":
				_, err = models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithEncodedPassword("!unusable"))
			case "missing_account":
				_, err = models.UserObjects.Delete(t.Context(), backend, &target)
			case "idle_expired":
				now = current.IdleExpiresAt()
			case "absolute_expired":
				now = current.AbsoluteExpiresAt()
			}
			if err != nil {
				t.Fatal(err)
			}
			before := passwordState(t, backend)
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			result, err := configured.owner.ChangePassword(t.Context(), current.ID(), password, password+"-new")
			passwordNoResult(t, result, err)
			if err != auth.ErrInvalidCredentials || hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("invalid session reached credential work or independent expiry cleanup", err)
			}
			passwordUnchanged(t, reopen(t), before)
			if mode == "idle_expired" || mode == "absolute_expired" {
				accountSessionRecord(t, configured.store, current, true)
			}
			if current.ID() == (sessions.ID{}) {
				t.Fatal("admission fixture did not use a valid bearer")
			}
		})
	}
}
