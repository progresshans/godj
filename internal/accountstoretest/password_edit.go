package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func passwordEditConfig() accountmodel.PasswordEditConfig[models.User] {
	return accountmodel.PasswordEditConfig[models.User]{ChangePermission: "account.team", AdditionalPermissions: []auth.Permission{"account.direct"}, Now: func() time.Time { return joined }}
}

func passwordEditOwner(t *testing.T, configured passwordFixture, config accountmodel.PasswordEditConfig[models.User]) *accountmodel.PasswordEditor[models.User] {
	t.Helper()
	owner, err := project.NewAccountPasswordEditor(configured.store, configured.authenticator, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func passwordEditNoResult(t *testing.T, result accountmodel.PasswordEditResult, err error) {
	t.Helper()
	if err == nil || result != (accountmodel.PasswordEditResult{}) {
		t.Fatal("failed administrative password edit published a result", err)
	}
}

func passwordAdminLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	policyScopes := []int64{}
	config := passwordEditConfig()
	config.Now = func() time.Time { return now }
	config.ValidatePassword = func(_ context.Context, _ string, row models.User) (validation.Errors, error) {
		if row.EncodedPassword != "" || row.PrincipalID == "" {
			return validation.Errors{}, errors.New("password policy did not receive private typed profile")
		}
		policyScopes = append(policyScopes, tracked.scopes.Load())
		return validation.Errors{}, nil
	}
	owner := passwordEditOwner(t, configured, config)
	target := roleTarget(t, backend, f, "admin-password-target")
	if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithFirstName("preserved").WithLastLogin(joined.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(target.ID, f.grants[1].ID)); err != nil {
		t.Fatal(err)
	}
	anonymous, err := configured.manager.Create(t.Context(), map[string]string{"anonymous": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	other := passwordSession(t, configured.manager, passwordCredential(t, f, f.user), "actor-browser")
	database, err := sessiondb.New(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	history, err := database.Store(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"new_password", "same_password", "disable", "repeat_disable", "enable", "inactive_target", "self"} {
		t.Run(mode, func(t *testing.T) {
			id := target.ID
			if mode == "self" {
				id = f.user.ID
			}
			if mode == "inactive_target" {
				if _, err := models.UserObjects.Patch(t.Context(), backend, target, models.UserPatch{}.WithActive(false)); err != nil {
					t.Fatal(err)
				}
			}
			before := profileRow(t, backend, id)
			credential, present, err := f.store.CredentialByID(t.Context(), before.PrincipalID)
			if err != nil || !present {
				t.Fatal("target credential missing", err)
			}
			previous := []sessions.Record{passwordSession(t, configured.manager, credential, "first"), passwordSession(t, configured.manager, credential, "second")}
			if created, err := history.Create(t.Context(), previous[0]); !created || err != nil {
				t.Fatal("historical fixture", err)
			}
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			policyScopes = nil
			now = now.Add(time.Minute)
			unusable := mode == "disable" || mode == "repeat_disable"
			var result accountmodel.PasswordEditResult
			const raw = "  administrator new λ password  "
			if unusable {
				result, err = owner.SetUnusablePassword(t.Context(), actor, id, before.Revision)
			} else {
				result, err = owner.SetPassword(t.Context(), actor, id, before.Revision, raw)
			}
			wantRevoked := 2
			if mode == "self" {
				wantRevoked++
			}
			if err != nil || result.ID != id || result.Revision != before.Revision+1 || result.RevokedSessions != wantRevoked {
				t.Fatal("administrative password result", result, err)
			}
			if tracked.writes.Load() != 1 || hasher.verifies.Load() != 0 {
				t.Fatal("administrative change verified old password or repeated write")
			}
			if unusable {
				if hasher.hashes.Load() != 0 || len(policyScopes) != 0 {
					t.Fatal("unusable password ran hashing or policy")
				}
			} else if hasher.hashes.Load() != 1 || !reflect.DeepEqual(policyScopes, []int64{0, 1}) {
				t.Fatal("password policy/hash scopes differ", policyScopes)
			}
			after := profileRow(t, reopen(t), id)
			preserved := after
			preserved.EncodedPassword, preserved.Revision = before.EncodedPassword, before.Revision
			if after.EncodedPassword == before.EncodedPassword || after.Revision != before.Revision+1 || !reflect.DeepEqual(preserved, before) {
				t.Fatal("password edit lost unrelated typed fields")
			}
			stored, present, err := f.store.CredentialByID(t.Context(), after.PrincipalID)
			if err != nil || !present {
				t.Fatal("stored target credential missing", err)
			}
			if stored.HasUsablePassword() == unusable || !reflect.DeepEqual(stored.Principal().Permissions(), credential.Principal().Permissions()) {
				t.Fatal("password usability or grants changed unexpectedly")
			}
			if !unusable {
				if valid, err := f.hasher.Verify(t.Context(), raw, after.EncodedPassword); err != nil || !valid {
					t.Fatal("password whitespace was changed", err)
				}
			}
			for _, old := range previous {
				accountSessionRecord(t, configured.store, old, false)
			}
			accountSessionRecord(t, configured.store, anonymous, true)
			accountSessionRecord(t, configured.store, other, mode != "self")
			if got, present, err := history.Load(t.Context(), previous[0].ID()); !present || err != nil || !reflect.DeepEqual(got.Snapshot(), previous[0].Snapshot()) {
				t.Fatal("historical namespace changed", err)
			}
			entries := profileHistory(t, reopen(t), actor, id)
			last := entries[len(entries)-1]
			if len(entries) != int(after.Revision-1) || last.ActorID() != actor.ID() || !last.At().Equal(now) || !reflect.DeepEqual(last.ChangedFields(), []string{"encoded_password"}) {
				t.Fatal("administrative password audit differs")
			}
			if strings.Contains(fmt.Sprintf("%#v", result), raw) || strings.Contains(fmt.Sprintf("%#v", owner), after.EncodedPassword) {
				t.Fatal("administrative password diagnostics leaked")
			}
		})
	}
}

func passwordAdminAdmission(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{})
	fault := errors.New("private administrative password failure")
	for _, mode := range []string{"missing_permission", "missing_additional", "missing_target", "stale", "empty", "byte_limit", "policy_rejection", "policy_failure", "caused_rejection", "foreign_field", "hash_failure", "hash_cancel", "hash_invalid_input", "hash_wrapped_input", "invalid_encoding", "unusable_hash", "unchanged_hash"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			config := passwordEditConfig()
			config.MaxPasswordBytes = 64
			config.ValidatePassword = func(context.Context, string, models.User) (validation.Errors, error) {
				diagnostics := validation.NewErrors(validation.New("password", "weak"))
				switch mode {
				case "policy_rejection":
					return diagnostics, nil
				case "policy_failure":
					return diagnostics, fault
				case "caused_rejection":
					return validation.Errors{}, validation.Reject(diagnostics, fault)
				case "foreign_field":
					return validation.NewErrors(validation.New("encoded_password", "bad")), nil
				}
				return validation.Errors{}, nil
			}
			id, revision, raw := f.user.ID, f.user.Revision, password+"-new"
			code := accountmodel.CodeInvalidInput
			switch mode {
			case "missing_permission":
				config.ChangePermission = "account.absent"
				code = accountmodel.CodePermission
			case "missing_additional":
				config.AdditionalPermissions = []auth.Permission{"account.absent"}
				code = accountmodel.CodePermission
			case "missing_target":
				id = 99999
				code = accountmodel.CodeNotFound
			case "stale":
				revision++
				code = accountmodel.CodeConflict
			case "empty":
				raw = ""
			case "byte_limit":
				raw = strings.Repeat("界", 22)
			case "policy_failure", "caused_rejection":
				code = accountmodel.CodePersistence
			case "foreign_field":
				code = accountmodel.CodeInvalidConfig
			case "invalid_encoding", "unusable_hash", "unchanged_hash":
				code = accountmodel.CodeInvalidConfig
			}
			hasher.beforeHash = nil
			hasher.encode = nil
			if mode == "hash_failure" {
				hasher.beforeHash = func(context.Context) error { return fault }
			}
			if mode == "hash_cancel" {
				hasher.beforeHash = func(context.Context) error { cancel(); return nil }
			}
			if mode == "hash_invalid_input" || mode == "hash_wrapped_input" || mode == "invalid_encoding" || mode == "unusable_hash" || mode == "unchanged_hash" {
				hasher.encode = func(context.Context, string) (string, error) {
					switch mode {
					case "hash_invalid_input":
						return "", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"}
					case "hash_wrapped_input":
						return "", fmt.Errorf("private hasher: %w", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"})
					case "invalid_encoding":
						return "invalid-envelope", nil
					case "unusable_hash":
						return "!unusable", nil
					default:
						return f.user.EncodedPassword, nil
					}
				}
			}
			owner := passwordEditOwner(t, configured, config)
			hasher.hashes.Store(0)
			tracked.writes.Store(0)
			before := passwordState(t, backend)
			result, err := owner.SetPassword(ctx, actor, id, revision, raw)
			passwordEditNoResult(t, result, err)
			if mode == "empty" || mode == "byte_limit" || mode == "policy_rejection" || mode == "hash_invalid_input" {
				if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil {
					t.Fatal("safe password diagnostics lost", err)
				}
			} else {
				requireProfileCode(t, err, code)
				if _, rejected := validation.Rejected(err); rejected {
					t.Fatal("execution failure became input rejection")
				}
			}
			wantHash := int64(0)
			if strings.HasPrefix(mode, "hash_") || mode == "invalid_encoding" || mode == "unusable_hash" || mode == "unchanged_hash" {
				wantHash = 1
			}
			if hasher.hashes.Load() != wantHash || tracked.writes.Load() != 0 {
				t.Fatal("rejected password operation progressed")
			}
			if mode == "hash_cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("hash cancellation lost")
			}
			if (mode == "hash_failure" || mode == "policy_failure" || mode == "caused_rejection") && !errors.Is(err, fault) {
				t.Fatal("private cause lost")
			}
			passwordUnchanged(t, reopen(t), before)
		})
	}
}
