package accountstoretest

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/auth/passwordpolicy"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func resetNativeReference(t *testing.T) identityreference.ResetObservation {
	t.Helper()
	var first identityreference.ResetObservation
	for _, vendor := range []string{"sqlite", "postgres"} {
		reference, err := identityreference.ResetObservations(vendor)
		if err != nil {
			t.Fatal(err)
		}
		if vendor == "sqlite" {
			first = reference
		} else if !reflect.DeepEqual(first.Bindings, reference.Bindings) || !reflect.DeepEqual(first.Clock, reference.Clock) ||
			!reflect.DeepEqual(first.Keys, reference.Keys) || !reflect.DeepEqual(first.Confirmed, reference.Confirmed) {
			t.Fatal("independent reset databases disagree on shared state semantics")
		}
	}
	return first
}

func passwordResetBindings(t *testing.T, open Open) {
	reference := resetNativeReference(t)
	for _, mode := range []string{"unchanged", "password", "same_password", "last_login_second", "last_login_microsecond", "email", "email_case", "username", "profile", "inactive", "unusable"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			owner := resetNativeOwner(t, f)
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			proof := selectedResetProof(t, owner, sessions.Record{}, token)
			original, row := f.target, f.target
			before := userReferenceSnapshot(t, reopen(t))
			patch := models.UserPatch{}
			switch mode {
			case "password", "same_password":
				raw := selfPassword
				if mode == "same_password" {
					raw = password
				}
				encoded, err := f.base.hasher.Hash(t.Context(), raw)
				if err != nil {
					t.Fatal(err)
				}
				patch, row.EncodedPassword = patch.WithEncodedPassword(encoded), encoded
			case "last_login_second", "last_login_microsecond":
				delta := time.Second
				if mode == "last_login_microsecond" {
					delta = time.Microsecond
				}
				at := row.LastLogin.Add(delta)
				patch, row.LastLogin = patch.WithLastLogin(at), &at
			case "email":
				patch, row.Email = patch.WithEmail("changed@example.test"), "changed@example.test"
			case "email_case":
				patch, row.Email = patch.WithEmail(strings.ToUpper(row.Email)), strings.ToUpper(row.Email)
			case "username":
				patch, row.Username = patch.WithUsername("renamed"), "renamed"
			case "profile":
				patch = patch.WithFirstName("Changed").WithStaff(true).WithRevision(row.Revision + 1)
				row.FirstName, row.Staff, row.Revision = "Changed", true, row.Revision+1
			case "inactive":
				patch, row.Active = patch.WithActive(false), false
			case "unusable":
				encoded, err := auth.MakeUnusablePassword(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				patch, row.EncodedPassword = patch.WithEncodedPassword(encoded), encoded
			}
			if mode != "unchanged" {
				if _, err := models.UserObjects.Patch(t.Context(), reopen(t), original, patch); err != nil {
					t.Fatal(err)
				}
			}
			expected := before
			userReferenceReplace(&expected.account, row)
			checkpoint := userReferenceSnapshot(t, reopen(t))
			assertUserReferenceState(t, expected, checkpoint, f.actor.Principal())
			f.owner.boundary.calls = 0
			entropy := f.entropy.count()
			err := owner.CheckPasswordReset(t.Context(), proof.ID(), row.PrincipalID, nil)
			if err != nil && err != auth.ErrInvalidResetProof {
				t.Fatal("reset state check returned an execution failure", err)
			}
			stored := profileRow(t, reopen(t), row.ID)
			actual := map[string]bool{"active": stored.Active, "usable": auth.IsPasswordUsable(stored.EncodedPassword), "password_changed": stored.EncodedPassword != original.EncodedPassword, "valid": err == nil}
			want := maps.Clone(reference.Bindings[mode])
			if mode == "inactive" || mode == "last_login_microsecond" {
				if !want["valid"] {
					t.Fatal("pinned Django reset deviation disappeared", mode)
				}
				want["valid"] = false
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatal("selected reset state binding differs from independent Django semantics", mode)
			}
			if mode == "inactive" || mode == "unusable" {
				issued, err := owner.IssueToken(t.Context(), row.PrincipalID)
				if err != auth.ErrInvalidResetToken || !reflect.DeepEqual(issued, auth.PasswordResetToken{}) {
					t.Fatal("ineligible selected account received a reset token", err)
				}
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("reset binding observation hashed, validated strength or wrote state")
			}
			assertUserReferenceState(t, checkpoint, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			for _, value := range []any{owner, token, err} {
				selfPasswordPrivate(t, value, token.Encoded(), original.EncodedPassword, row.EncodedPassword, proof.ID().Encoded())
			}
		})
	}
}

func passwordResetClockKeys(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	reference := resetNativeReference(t)
	issuedAt, tokenNow := f.clock(), f.clock()
	config := resetNativeConfig(t, f)
	config.Timeout, config.Clock = time.Hour, func() time.Time { return tokenNow }
	owner, err := f.runtime.PasswordResetter(config)
	if err != nil {
		t.Fatal(err)
	}
	token := selectedResetToken(t, owner, f.target.PrincipalID)
	proof := selectedResetProof(t, owner, sessions.Record{}, token)
	checkpoint := userReferenceSnapshot(t, reopen(t))
	f.owner.boundary.calls = 0
	entropy := f.entropy.count()
	unchanged := func(t *testing.T) {
		t.Helper()
		if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
			t.Fatal("reset time/key admission changed durable or password work")
		}
		assertUserReferenceState(t, checkpoint, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	}
	t.Run("clock", func(t *testing.T) {
		for _, item := range []struct {
			name   string
			offset time.Duration
		}{{"same", 0}, {"within", 30 * time.Minute}, {"boundary", time.Hour}, {"expired", time.Hour + time.Second}, {"future", -time.Second}} {
			t.Run(item.name, func(t *testing.T) {
				tokenNow = issuedAt.Add(item.offset)
				want := reference.Clock[item.name]
				if item.name == "future" {
					if !want {
						t.Fatal("Django future-token deviation disappeared")
					}
					want = false
				}
				err := owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, nil)
				if (err == nil) != want || err != nil && err != auth.ErrInvalidResetProof {
					t.Fatal("selected token clock differs from its independent boundary", item.name, err)
				}
				unchanged(t)
			})
		}
		t.Run("invalid", func(t *testing.T) {
			tokenNow = time.Time{}
			issued, err := owner.IssueToken(t.Context(), f.target.PrincipalID)
			if !reflect.DeepEqual(issued, auth.PasswordResetToken{}) {
				t.Fatal("invalid token clock published a delivery")
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			requireProfileCode(t, owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, nil), accountmodel.CodePersistence)
			unchanged(t)
		})
	})
	tokenNow = issuedAt
	active := bytes.Repeat([]byte{88}, 32)
	fallback, err := auth.NewPasswordResetKeyRing(active, bytes.Repeat([]byte{66}, 32))
	if err != nil {
		t.Fatal(err)
	}
	only, err := auth.NewPasswordResetKeyRing(active)
	if err != nil {
		t.Fatal(err)
	}
	clear(active)
	nextConfig := config
	nextConfig.Keys = fallback
	rotated, err := f.runtime.PasswordResetter(nextConfig)
	if err != nil {
		t.Fatal(err)
	}
	nextConfig.Keys = only
	activeOnly, err := f.runtime.PasswordResetter(nextConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("keys", func(t *testing.T) {
		for _, mode := range []string{"same_state_same_second", "old_with_fallback", "old_without_fallback"} {
			t.Run(mode, func(t *testing.T) {
				actual := false
				if mode == "same_state_same_second" {
					actual = selectedResetToken(t, owner, f.target.PrincipalID).Encoded() == token.Encoded()
				} else {
					check := rotated
					if mode == "old_without_fallback" {
						check = activeOnly
					}
					err := check.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, nil)
					if err != nil && err != auth.ErrInvalidResetProof {
						t.Fatal(err)
					}
					actual = err == nil
				}
				if actual != reference.Keys[mode] {
					t.Fatal("native reset key observation differs from Django", mode)
				}
				unchanged(t)
			})
		}
		t.Run("active_key_issuance", func(t *testing.T) {
			token := selectedResetToken(t, rotated, f.target.PrincipalID)
			refused, err := owner.StartPasswordReset(t.Context(), sessions.ID{}, f.target.PrincipalID, token.Encoded())
			if err != auth.ErrInvalidResetProof || !reflect.DeepEqual(refused, sessions.Record{}) {
				t.Fatal("new token was issued using a retired fallback", err)
			}
			unchanged(t)
			record := selectedResetProof(t, activeOnly, sessions.Record{}, token)
			after := userReferenceSnapshot(t, reopen(t))
			expected, wanted := resetEntryExpected(t, f, checkpoint, after, sessions.Record{}, f.entropy.one(t, entropy), token.Encoded())
			if !reflect.DeepEqual(record.Snapshot(), wanted.Snapshot()) || f.owner.boundary.calls != 1 {
				t.Fatal("active key did not produce exactly one new native proof")
			}
			assertUserReferenceState(t, expected, after, f.actor.Principal())
		})
	})
	for _, value := range []any{owner, rotated, activeOnly, config, token} {
		selfPasswordPrivate(t, value, token.Encoded(), proof.ID().Encoded(), f.target.EncodedPassword, strings.Repeat("B", 32), strings.Repeat("X", 32))
	}
}

func passwordResetConfiguration(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	reference := resetNativeReference(t)
	secret := bytes.Repeat([]byte{66}, 32)
	keys, err := auth.NewPasswordResetKeyRing(secret)
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := passwordpolicy.NewMinimumLengthValidator(8)
	if err != nil {
		t.Fatal(err)
	}
	var policyScopes []int64
	clocks := 0
	config := accountmodel.PasswordResetConfig[models.User]{Keys: keys, Timeout: time.Hour, MaxPasswordBytes: 64, Clock: func() time.Time {
		clocks++
		return f.clock()
	}, ValidatePassword: func(_ context.Context, raw string, profile orm.ModelView[models.User]) (validation.Errors, error) {
		if passwordViewInteger(t, profile, "id") != f.target.ID || passwordViewString(t, profile, "first_name") != f.target.FirstName || passwordViewString(t, profile, "encoded_password") != "" {
			return validation.Errors{}, errors.New("reset configuration lost its selected private profile")
		}
		policyScopes = append(policyScopes, f.owner.hasher.scopes.Load())
		return minimum.ValidatePassword(raw, nil), nil
	}}
	before := userReferenceSnapshot(t, reopen(t))
	f.owner.boundary.calls = 0
	entropy := f.entropy.count()
	owner, err := hostproject.NewAccountPasswordResetter(f.runtime.SessionStore(), f.runtime.Sessions(), f.runtime.Authenticator(), config)
	if err != nil || clocks != 0 || len(policyScopes) != 0 || owner.Sessions() != f.runtime.Sessions() || owner.MaxPasswordBytes() != 64 {
		t.Fatal("reset composition did not own its exact source/configuration", err)
	}
	if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
		t.Fatal("reset constructor performed password/session work")
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
	issued := selectedResetToken(t, owner, f.target.PrincipalID)
	clear(secret)
	config.Keys, config.Timeout, config.MaxPasswordBytes = auth.PasswordResetKeyRing{}, time.Second, 1
	config.Clock = func() time.Time { return time.Time{} }
	config.ValidatePassword = func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
		return validation.NewErrors(validation.New("password", "caller_mutation")), nil
	}
	if selectedResetToken(t, owner, f.target.PrincipalID).Encoded() != issued.Encoded() {
		t.Fatal("reset owner borrowed mutable key/configuration state")
	}
	previous, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"cart": "configuration browser", "csrf": "independent"})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"changed", "same_password"} {
		if !t.Run(mode, func(t *testing.T) {
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			// The caller's replacement timeout was one second; this owner
			// retains its original one-hour policy.
			f.instant.Add(int64(2 * time.Second))
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			proof := selectedResetProof(t, owner, previous, token)
			afterEntry := userReferenceSnapshot(t, reopen(t))
			expected, wantedProof := resetEntryExpected(t, f, before, afterEntry, previous, f.entropy.one(t, entropy), token.Encoded())
			if !reflect.DeepEqual(proof.Snapshot(), wantedProof.Snapshot()) {
				t.Fatal("configured reset entry changed its requested proof")
			}
			assertUserReferenceState(t, expected, afterEntry, f.actor.Principal())
			entropy = f.entropy.count()
			f.owner.hasher.hashes.Store(0)
			f.owner.hasher.verifies.Store(0)
			f.owner.boundary.calls = 0
			policyScopes = nil
			for _, raw := range []string{"", "short"} {
				checkErr := owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, &raw)
				refused, resetErr := owner.ResetPassword(t.Context(), proof.ID(), f.target.PrincipalID, raw)
				selectedResetNoResult(t, refused, resetErr)
				for _, err := range []error{checkErr, resetErr} {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil {
						t.Fatal("configured reset did not preserve direct preflight refusal", err)
					}
				}
			}
			if !reflect.DeepEqual(policyScopes, []int64{0, 0}) || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("rejected reset bypassed its owned policy or performed password/write work")
			}
			assertUserReferenceState(t, afterEntry, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			policyScopes = nil
			raw := selfPassword
			if err := owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, nil); err != nil || len(policyScopes) != 0 {
				t.Fatal("nil password reset check ran strength policy", err)
			}
			if err := owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, &raw); err != nil {
				t.Fatal("caller mutation weakened or replaced the owned reset policy", err)
			}
			if !reflect.DeepEqual(policyScopes, []int64{0}) || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("advisory reset check performed work or retained a native scope")
			}
			assertUserReferenceState(t, afterEntry, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			f.instant.Add(int64(time.Minute))
			result, err := owner.ResetPassword(t.Context(), proof.ID(), f.target.PrincipalID, raw)
			if err != nil || !reflect.DeepEqual(policyScopes, []int64{0, 0, 1}) || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 1 {
				t.Fatal("configured reset did not perform exactly one current native transition", err, policyScopes)
			}
			after := userReferenceSnapshot(t, reopen(t))
			expected, envelope := resetFinishExpected(t, f, f.owner, afterEntry, after, proof, f.entropy.one(t, entropy))
			if result.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), envelope.Record.Snapshot()) {
				t.Fatal("configured reset published a different browser outcome")
			}
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			entropy = f.entropy.count()
			reused, reusedErr := owner.ResetPassword(t.Context(), proof.ID(), f.target.PrincipalID, raw)
			selectedResetNoResult(t, reused, reusedErr)
			if reusedErr != auth.ErrInvalidResetProof || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 1 || f.entropy.count() != entropy || !reflect.DeepEqual(policyScopes, []int64{0, 0, 1}) {
				t.Fatal("consumed reset proof repeated password or native work", reusedErr)
			}
			reopened := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
			credential, err := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, raw)
			domain, _ := f.credential.Principal().CredentialDomain()
			expectedCredential, expectedErr := auth.NewCredentialForDomain(domain, f.target.Username, f.owner.encoded.Load().(string), f.credential.Principal())
			if err != nil || expectedErr != nil || !credential.MatchesSessionStamp(expectedCredential.SessionStamp()) || !reflect.DeepEqual(credential.Principal(), expectedCredential.Principal()) {
				t.Fatal("reset password did not survive reconnect", err)
			}
			_, trimmedErr := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, strings.TrimSpace(raw))
			if trimmedErr != auth.ErrInvalidCredentials {
				t.Fatal("selected reset trimmed the raw password", trimmedErr)
			}
			reopenedReset, err := reopened.runtime.PasswordResetter(resetNativeConfig(t, f))
			if err != nil {
				t.Fatal(err)
			}
			fresh := selectedResetToken(t, reopenedReset, f.target.PrincipalID)
			if fresh.Encoded() == token.Encoded() || fresh.Encoded() != selectedResetToken(t, owner, f.target.PrincipalID).Encoded() {
				t.Fatal("reconnected reset owner lost the current key, clock or selected binding")
			}
			replay, replayErr := reopenedReset.StartPasswordReset(t.Context(), sessions.ID{}, f.target.PrincipalID, token.Encoded())
			if replayErr != auth.ErrInvalidResetProof || !reflect.DeepEqual(replay, sessions.Record{}) {
				t.Fatal("reset replay was revived by reconnect", replayErr)
			}
			stored := profileRow(t, reopen(t), f.target.ID)
			actual := map[string]bool{"last_login_unchanged": stored.LastLogin != nil && f.target.LastLogin != nil && stored.LastLogin.Equal(*f.target.LastLogin), "new_password_valid": credential.Principal().Authenticated(), "old_token_valid": replayErr == nil, "trimmed_password_valid": trimmedErr == nil}
			if !reflect.DeepEqual(actual, reference.Confirmed) {
				t.Fatal("native reset completion differs from independent Django observations")
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			for _, value := range []any{owner, config, token, result, replayErr} {
				selfPasswordPrivate(t, value, raw, token.Encoded(), f.target.EncodedPassword, stored.EncodedPassword, proof.ID().Encoded(), result.Record.ID().Encoded())
			}
			f.target.EncodedPassword, f.target.Revision = f.owner.encoded.Load().(string), f.target.Revision+1
			f.credential, f.targetSessions, previous = credential, nil, envelope.Record
		}) {
			return
		}
	}
}

func runResetNativeReferences(add registerCase, open Open) {
	add("password_reset_browser_current", func(t *testing.T) { passwordResetBrowserCurrent(t, open) })
	add("password_reset_browser_admission", func(t *testing.T) { passwordResetBrowserAdmission(t, open) })
	add("password_reset_session_fence", func(t *testing.T) { passwordResetSessionFence(t, open) })
	add("password_reset_entropy", func(t *testing.T) { passwordResetEntropy(t, open) })
	add("password_reset_web", func(t *testing.T) { passwordResetWeb(t, open) })
	add("password_reset_lifecycle", func(t *testing.T) { passwordResetLifecycle(t, open) })
	add("password_reset_bindings", func(t *testing.T) { passwordResetBindings(t, open) })
	add("password_reset_clock_keys", func(t *testing.T) { passwordResetClockKeys(t, open) })
	add("password_reset_configuration", func(t *testing.T) { passwordResetConfiguration(t, open) })
	add("password_reset_current", func(t *testing.T) { passwordResetCurrent(t, open) })
	add("password_reset_concurrent", func(t *testing.T) { passwordResetConcurrent(t, open) })
}
