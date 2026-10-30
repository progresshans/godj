package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func passwordResetValidation(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	base := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
	proof := selectedResetProof(t, base, sessions.Record{}, selectedResetToken(t, base, f.user.PrincipalID))
	fault := errors.New("private selected reset policy or hash failure")
	for _, name := range []string{"empty", "byte_limit", "policy_rejection", "policy_failure", "wrapped_rejection", "caused_rejection", "foreign_field", "hash_failure", "hash_invalid_input", "hash_wrapped_input", "invalid_encoding", "unusable_hash", "unchanged_hash", "hash_cancel"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			config := selectedResetConfig(t, &now)
			config.MaxPasswordBytes = 64
			calls := 0
			config.ValidatePassword = func(_ context.Context, _ string, row models.User) (validation.Errors, error) {
				calls++
				if row.ID != f.user.ID || row.PrincipalID != f.user.PrincipalID || row.EncodedPassword != "" || tracked.scopes.Load() != 0 {
					t.Fatal("reset preflight policy lost selected private profile or retained a DB scope")
				}
				diagnostics := validation.NewErrors(validation.New("password", "weak"))
				switch name {
				case "policy_rejection":
					return diagnostics, nil
				case "policy_failure":
					return diagnostics, fault
				case "wrapped_rejection":
					return validation.Errors{}, fmt.Errorf("private wrapper: %w", validation.Reject(diagnostics, nil))
				case "caused_rejection":
					return validation.Errors{}, validation.Reject(diagnostics, fault)
				case "foreign_field":
					return validation.NewErrors(validation.New("encoded_password", "invalid")), nil
				}
				return validation.Errors{}, nil
			}
			raw := password + "-reset"
			if name == "empty" {
				raw = ""
			} else if name == "byte_limit" {
				raw = strings.Repeat("界", 22)
			}
			hasher.encode = nil
			if strings.HasPrefix(name, "hash_") || name == "invalid_encoding" || name == "unusable_hash" || name == "unchanged_hash" {
				hasher.encode = func(context.Context, string) (string, error) {
					switch name {
					case "hash_failure":
						return "", fault
					case "hash_invalid_input":
						return "", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
					case "hash_wrapped_input":
						return "", fmt.Errorf("private hash: %w", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"})
					case "invalid_encoding":
						return "invalid-encoding", nil
					case "unusable_hash":
						return "!disabled", nil
					case "hash_cancel":
						cancel()
					}
					return f.user.EncodedPassword, nil
				}
			}
			owner := selectedResetOwner(t, configured, config)
			before := passwordState(t, backend)
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			result, err := owner.ResetPassword(ctx, proof.ID(), f.user.PrincipalID, raw)
			selectedResetNoResult(t, result, err)
			isInput := name == "empty" || name == "byte_limit" || name == "policy_rejection" || name == "hash_invalid_input"
			if diagnostics, rejected := validation.Rejected(err); rejected != isInput || rejected && (diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil) {
				t.Fatal("reset validation confused input and execution failure", err)
			}
			if name == "policy_failure" || name == "wrapped_rejection" || name == "caused_rejection" {
				requireProfileCode(t, err, accountmodel.CodePersistence)
			}
			if name == "foreign_field" || name == "invalid_encoding" || name == "unusable_hash" || name == "unchanged_hash" {
				requireProfileCode(t, err, accountmodel.CodeInvalidConfig)
			}
			if (name == "hash_failure" || name == "policy_failure" || name == "caused_rejection") && !errors.Is(err, fault) || name == "hash_cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("reset lost private failure cause", err)
			}
			wantHashes := int64(0)
			if hasher.encode != nil {
				wantHashes = 1
			}
			if hasher.hashes.Load() != wantHashes || hasher.verifies.Load() != 0 || tracked.writes.Load() != 0 || (name == "empty" || name == "byte_limit") && calls != 0 {
				t.Fatal("rejected reset performed excess password/policy/write work")
			}
			for _, text := range []string{fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(text, fault.Error()) || strings.Contains(text, password) || strings.Contains(text, f.user.EncodedPassword) {
					t.Fatal("reset failure exposed secrets")
				}
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}

func passwordResetAdmission(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	for _, name := range []string{"no_proof", "missing_token", "malformed_token", "other_target", "deleted_session", "inactive", "unusable", "deleted_account", "idle_expired", "absolute_expired", "token_expired", "future_token"} {
		t.Run(name, func(t *testing.T) {
			now := joined
			target := roleTarget(t, backend, f, "reset-admission-"+name)
			tracked := &passwordScopeOwner{Backend: backend}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
			configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
			config := selectedResetConfig(t, &now)
			// Token and session clocks are intentionally independent.
			tokenNow := joined
			config.Clock = func() time.Time { return tokenNow }
			owner := selectedResetOwner(t, configured, config)
			token := selectedResetToken(t, owner, target.PrincipalID)
			proof := selectedResetProof(t, owner, sessions.Record{}, token)
			var err error
			switch name {
			case "no_proof":
				proof = proof.WithoutValue(auth.SessionResetPrincipalIDKey).WithoutValue(auth.SessionResetTokenKey)
			case "missing_token":
				proof = proof.WithoutValue(auth.SessionResetTokenKey)
			case "malformed_token":
				proof, err = proof.WithValue(auth.SessionResetTokenKey, "invalid")
			case "other_target":
				proof, err = proof.WithValue(auth.SessionResetPrincipalIDKey, f.user.PrincipalID)
			case "deleted_session":
				err = configured.store.Delete(t.Context(), proof.ID())
			case "inactive":
				_, err = models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithActive(false))
			case "unusable":
				_, err = models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithEncodedPassword("!disabled"))
			case "deleted_account":
				_, err = models.UserObjects.Delete(t.Context(), backend, &target)
			case "idle_expired":
				now = proof.IdleExpiresAt()
			case "absolute_expired":
				now = proof.AbsoluteExpiresAt()
			case "token_expired":
				tokenNow = joined.Add(72*time.Hour + time.Second)
			case "future_token":
				tokenNow = joined.Add(-time.Second)
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "no_proof" || name == "missing_token" || name == "malformed_token" || name == "other_target" {
				if err := configured.store.Delete(t.Context(), proof.ID()); err != nil {
					t.Fatal(err)
				}
				if saved, err := configured.store.Create(t.Context(), proof); err != nil || !saved {
					t.Fatal("invalid proof fixture", err)
				}
			}
			before := passwordState(t, backend)
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			if err := owner.CheckPasswordReset(t.Context(), proof.ID(), target.PrincipalID, nil); err != auth.ErrInvalidResetProof {
				t.Fatal("inadmissible reset check", err)
			}
			result, err := owner.ResetPassword(t.Context(), proof.ID(), target.PrincipalID, password+"-new")
			selectedResetNoResult(t, result, err)
			if err != auth.ErrInvalidResetProof || hasher.hashes.Load() != 0 || hasher.verifies.Load() != 0 || tracked.writes.Load() != 0 {
				t.Fatal("invalid proof reached password work or expiry cleanup", err)
			}
			if name == "inactive" || name == "unusable" || name == "deleted_account" {
				if rejected, err := owner.IssueToken(t.Context(), target.PrincipalID); err != auth.ErrInvalidResetToken || rejected.Encoded() != "" {
					t.Fatal("ineligible reset issuance", err)
				}
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}
