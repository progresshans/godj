package identitytest

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

//go:embed testdata/password-reset-django61-*.json
var passwordResetReferences embed.FS

func resetConfig(t *testing.T, now *time.Time, validators ...identity.PasswordValidator) identity.PasswordResetConfig {
	t.Helper()
	keys, err := identity.NewPasswordResetKeyRing(bytes.Repeat([]byte{173}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return identity.PasswordResetConfig{Keys: keys, Timeout: time.Hour, Clock: func() time.Time { return *now }, Validators: validators}
}
func resetService(t *testing.T, backend identity.ManagementBackend, hasher auth.PasswordHasher, config identity.PasswordResetConfig) *identity.PasswordResetter {
	t.Helper()
	service, err := identity.NewPasswordResetter(backend, hasher, config)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func resetToken(t *testing.T, resetter *identity.PasswordResetter, id string) identity.PasswordResetToken {
	t.Helper()
	token, err := resetter.IssueToken(t.Context(), id)
	if err != nil || token.PrincipalID() != id || token.Encoded() == "" {
		t.Fatal("reset issue failed", err)
	}
	return token
}
func resetBindingReference(t *testing.T, mode string) bool {
	t.Helper()
	var result bool
	for _, backend := range []string{"sqlite", "postgres"} {
		data, err := passwordResetReferences.ReadFile("testdata/password-reset-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django       string
			Observations struct {
				Bindings map[string]struct{ Valid bool }
			}
		}
		if err := json.Unmarshal(data, &reference); err != nil || reference.Django != "6.1" {
			t.Fatal("invalid reset reference", err)
		}
		value, ok := reference.Observations.Bindings[mode]
		if !ok {
			t.Fatal("missing native binding observation", mode)
		}
		if backend == "postgres" && result != value.Valid {
			t.Fatal("native reset backends differ")
		}
		result = value.Valid
	}
	return result
}
func assertResetCommit(t *testing.T, f *managementFixture, before models.User, password string, revision int64) {
	t.Helper()
	stored := f.stored(t)
	verified, err := f.hasher.Verify(t.Context(), password, stored.EncodedPassword)
	if err != nil || !verified || stored.EncodedPassword == before.EncodedPassword || stored.Revision != revision {
		t.Fatal("reset credential/revision failed", err)
	}
	comparison := stored
	comparison.EncodedPassword = before.EncodedPassword
	comparison.Revision = before.Revision
	if !reflect.DeepEqual(comparison, before) {
		t.Fatal("reset overwrote current profile or last_login")
	}
	for index, id := range f.ids {
		_, found, err := f.runtime.SessionStore().Load(t.Context(), id)
		if err != nil || found != (index%3 != 0) {
			t.Fatal("reset revoked another identity or retained target session", index, err)
		}
	}
	history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
	if err != nil || len(history) == 0 {
		t.Fatal("reset audit missing", err)
	}
	last := history[len(history)-1]
	if last.ActorID != f.user.PrincipalID || last.Action != admin.ActionChange || last.DisplayLabel != "" || !reflect.DeepEqual(last.ChangedFields, []string{"password"}) {
		t.Fatal("reset audit actor or disclosure changed")
	}
}

func RunPasswordReset(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("atomic_reset_reopens_and_same_password_invalidates", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 260)
		now := loginInstant
		user, err := models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithLastLogin(now))
		if err != nil {
			t.Fatal(err)
		}
		f.user = user
		minimum, err := identity.NewMinimumLengthValidator(8)
		if err != nil {
			t.Fatal(err)
		}
		validators := []identity.PasswordValidator{minimum}
		config := resetConfig(t, &now, validators...)
		resetter := resetService(t, f.runtime, f.hasher, config)
		validators[0] = nil // Constructor must own the validator list.
		rows, audit := snapshotIdentitySystemRows(t, backend)
		before := f.stored(t)
		token := resetToken(t, resetter, f.user.PrincipalID)
		again := resetToken(t, resetter, f.user.PrincipalID)
		if again.Encoded() != token.Encoded() {
			t.Fatal("same state and second were not deterministic")
		}
		if err := resetter.CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), nil); err != nil {
			t.Fatal(err)
		}
		for _, password := range []string{"", "short"} {
			if err := resetter.CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), &password); err == nil {
				t.Fatal("weak password check accepted")
			}
			if err := resetter.ResetPassword(t.Context(), token.PrincipalID(), token.Encoded(), password); err == nil {
				t.Fatal("weak reset accepted")
			}
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("preflight had hash or write effects")
		}
		requirePasswordPrivate(t, token, token.Encoded(), before.EncodedPassword)
		requirePasswordPrivate(t, resetter, token.Encoded(), before.EncodedPassword)
		if err := resetter.ResetPassword(t.Context(), token.PrincipalID(), token.Encoded(), selfPassword); err != nil {
			t.Fatal(err)
		}
		assertResetCommit(t, f, before, selfPassword, 2)
		if f.hasher.calls.Load() != 1 {
			t.Fatal("reset hashed more than once")
		}
		if err := resetter.ResetPassword(t.Context(), token.PrincipalID(), token.Encoded(), "replayed password"); err != identity.ErrInvalidResetToken || f.hasher.calls.Load() != 1 {
			t.Fatal("used token repeated hash or write", err)
		}
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reopened.Authenticator().Authenticate(t.Context(), before.Username, selfPassword); err != nil {
			t.Fatal("reopened runtime cannot authenticate reset", err)
		}
		if _, err := reopened.Authenticator().Authenticate(t.Context(), before.Username, strings.TrimSpace(selfPassword)); err != auth.ErrInvalidCredentials {
			t.Fatal("reset trimmed password", err)
		}
		second := resetService(t, reopened, f.hasher, resetConfig(t, &now, minimum))
		if err := second.CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), nil); err != identity.ErrInvalidResetToken {
			t.Fatal("restart revived consumed token", err)
		}
		next := resetToken(t, second, f.user.PrincipalID)
		before = f.stored(t)
		if err := second.ResetPassword(t.Context(), next.PrincipalID(), next.Encoded(), selfPassword); err != nil {
			t.Fatal(err)
		}
		assertResetCommit(t, f, before, selfPassword, 3)
		if f.hasher.calls.Load() != 2 || second.CheckPassword(t.Context(), next.PrincipalID(), next.Encoded(), nil) != identity.ErrInvalidResetToken {
			t.Fatal("same password did not get a new binding")
		}
	})
	for _, mode := range []string{"unchanged", "password", "same_password", "last_login_second", "last_login_microsecond", "email", "email_case", "username", "profile", "inactive", "unusable"} {
		t.Run("bindings/"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			now := loginInstant
			row, err := models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithLastLogin(now))
			if err != nil {
				t.Fatal(err)
			}
			f.user = row
			resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
			token := resetToken(t, resetter, f.user.PrincipalID)
			patch := models.UserPatch{}
			switch mode {
			case "password", "same_password":
				password := managementNewPassword
				if mode == "same_password" {
					password = managementOldPassword
				}
				value, err := f.hasher.PasswordHasher.Hash(t.Context(), password)
				if err != nil {
					t.Fatal(err)
				}
				patch = patch.WithEncodedPassword(value)
			case "last_login_second":
				patch = patch.WithLastLogin(now.Add(time.Second))
			case "last_login_microsecond":
				patch = patch.WithLastLogin(now.Add(time.Microsecond))
			case "email":
				patch = patch.WithEmail("new@example.test")
			case "email_case":
				patch = patch.WithEmail(strings.ToUpper(f.user.Email))
			case "username":
				patch = patch.WithUsername("renamed")
			case "profile":
				patch = patch.WithFirstName("Changed").WithStaff(false).WithRevision(2)
			case "inactive":
				patch = patch.WithActive(false)
			case "unusable":
				value, err := auth.MakeUnusablePassword(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				patch = patch.WithEncodedPassword(value)
			}
			if mode != "unchanged" {
				if _, err := models.UserObjects.Update(t.Context(), backend, f.user, patch); err != nil {
					t.Fatal(err)
				}
			}
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			want := resetBindingReference(t, mode)
			if mode == "inactive" || mode == "last_login_microsecond" {
				if !want {
					t.Fatal("native deviation disappeared")
				}
				want = false
			}
			err = resetter.CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), nil)
			if (err == nil) != want || err != nil && err != identity.ErrInvalidResetToken {
				t.Fatal("wrong reset state binding", mode, err)
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if f.hasher.calls.Load() != 0 || !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("token check changed durable state")
			}
			if mode == "inactive" || mode == "unusable" {
				if token, err := resetter.IssueToken(t.Context(), f.user.PrincipalID); err != identity.ErrInvalidResetToken || token.Encoded() != "" {
					t.Fatal("ineligible token issued", err)
				}
			}
		})
	}
	t.Run("configuration_admission_and_read_contract", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		now := loginInstant
		config := resetConfig(t, &now)
		resetter := resetService(t, f.runtime, f.hasher, config)
		token := resetToken(t, resetter, f.user.PrincipalID)
		for _, mode := range []string{"read_zero", "read_nil", "read_twice", "read_swallowed_failure", "read_end_failure"} {
			t.Run(mode, func(t *testing.T) {
				boundary := &managementBoundary{ManagementBackend: f.runtime, mode: mode}
				broken := resetService(t, boundary, f.hasher, config)
				if result, err := broken.IssueToken(t.Context(), f.user.PrincipalID); err == nil || err == identity.ErrInvalidResetToken || result.Encoded() != "" {
					t.Fatal("read failure published token or ordinary refusal", err)
				}
				if err := broken.ResetPassword(t.Context(), token.PrincipalID(), token.Encoded(), selfPassword); err == nil || err == identity.ErrInvalidResetToken || boundary.calls != 0 || f.hasher.calls.Load() != 0 {
					t.Fatal("read failure permitted reset", err)
				}
			})
		}
		for _, id := range []string{"", "unknown-reset-user", "\x00invalid"} {
			if value, err := resetter.IssueToken(t.Context(), id); err != identity.ErrInvalidResetToken || value.Encoded() != "" {
				t.Fatal("unknown identity issued token", err)
			}
			if err := resetter.CheckPassword(t.Context(), id, token.Encoded(), nil); err != identity.ErrInvalidResetToken {
				t.Fatal("unknown identity checked token", err)
			}
		}
		broken := resetService(t, &managementBoundary{ManagementBackend: f.runtime, mode: "read_zero"}, f.hasher, config)
		for _, malformed := range []string{"", strings.Repeat("x", 10000), token.Encoded() + "\n", token.Encoded() + "="} {
			if err := broken.ResetPassword(t.Context(), f.user.PrincipalID, malformed, selfPassword); err != identity.ErrInvalidResetToken {
				t.Fatal("malformed token reached database", err)
			}
		}
		if err := resetter.CheckPassword(t.Context(), f.root.PrincipalID, token.Encoded(), nil); err != identity.ErrInvalidResetToken {
			t.Fatal("token applied to another user", err)
		}
		for _, timeout := range []time.Duration{-1, time.Nanosecond, time.Second + time.Nanosecond} {
			bad := config
			bad.Timeout = timeout
			if _, err := identity.NewPasswordResetter(f.runtime, f.hasher, bad); err == nil {
				t.Fatal("invalid reset timeout")
			}
		}
		for _, mode := range []string{"keys", "validator", "typed_nil_validator", "nil_backend", "nil_hasher"} {
			bad := config
			var store identity.ManagementBackend = f.runtime
			var hasher auth.PasswordHasher = f.hasher
			switch mode {
			case "keys":
				bad.Keys = identity.PasswordResetKeyRing{}
			case "validator":
				bad.Validators = []identity.PasswordValidator{nil}
			case "typed_nil_validator":
				var v identity.PasswordValidatorFunc
				bad.Validators = []identity.PasswordValidator{v}
			case "nil_backend":
				var r *systemstate.Runtime
				store = r
			case "nil_hasher":
				var h *managementHasher
				hasher = h
			}
			if _, err := identity.NewPasswordResetter(store, hasher, bad); err == nil {
				t.Fatal("invalid reset configuration", mode)
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := resetter.ResetPassword(ctx, token.PrincipalID(), token.Encoded(), selfPassword); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
		if _, err := resetter.IssueToken(nil, token.PrincipalID()); err == nil {
			t.Fatal("nil context accepted")
		}
		var missing *identity.PasswordResetter
		if _, err := missing.IssueToken(t.Context(), token.PrincipalID()); err == nil {
			t.Fatal("nil reset service accepted")
		}
		if err := (&identity.PasswordResetter{}).ResetPassword(t.Context(), token.PrincipalID(), token.Encoded(), selfPassword); err == nil {
			t.Fatal("zero reset service accepted")
		}
		for _, offset := range []time.Duration{-time.Second, time.Hour, time.Hour + time.Second} {
			now = loginInstant.Add(offset)
			err := resetter.CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), nil)
			if (err == nil) != (offset == time.Hour) || err != nil && err != identity.ErrInvalidResetToken {
				t.Fatal("wrong current expiry check", err)
			}
		}
		now = time.Time{}
		if value, err := resetter.IssueToken(t.Context(), f.user.PrincipalID); err == nil || value.Encoded() != "" {
			t.Fatal("invalid clock issued token", err)
		}
		if f.hasher.calls.Load() != 0 {
			t.Fatal("invalid admission hashed")
		}
	})
}

func RunPasswordResetBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"zero", "nil", "twice", "swallow", "opaque", "password_write", "after_password", "audit_insert", "after_audit", "revoke", "cancel", "unknown_rollback", "unknown_commit", "late_cancel", "validation", "validation_cleanup", "token_rejected", "token_cleanup", "same_encoding", "hash_failure", "hash_cancel"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 4)
			now := loginInstant
			boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: mode}
			if mode == "token_cleanup" {
				boundary.mode = "validation_cleanup"
			}
			runtime, err := systemstate.OpenIdentity(t.Context(), boundary, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			validations := 0
			validator := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
				validations++
				if validations == 2 && (mode == "validation" || mode == "validation_cleanup") {
					return validation.NewErrors(validation.New("password", "password_too_similar"))
				}
				return validation.Errors{}
			})
			resetter := resetService(t, runtime, f.hasher, resetConfig(t, &now, validator))
			token := resetToken(t, resetter, f.user.PrincipalID)
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			boundary.afterCommit = cancel
			f.hasher.hook = func(context.Context) error {
				boundary.armed = true
				if mode == "token_rejected" || mode == "token_cleanup" {
					now = now.Add(time.Hour + time.Second)
				}
				if mode == "hash_failure" {
					return errors.New("private-reset-hash")
				}
				if mode == "hash_cancel" {
					cancel()
				}
				return nil
			}
			if mode == "same_encoding" {
				f.hasher.fixed = before.EncodedPassword
			}
			err = resetter.ResetPassword(ctx, token.PrincipalID(), token.Encoded(), selfPassword)
			boundary.armed = false
			f.hasher.hook = nil
			f.hasher.fixed = ""
			preflightOnly := mode == "same_encoding" || mode == "hash_failure" || mode == "hash_cancel"
			wantCalls := 1
			if preflightOnly {
				wantCalls = 0
			}
			if boundary.calls != wantCalls || f.hasher.calls.Load() != 1 {
				t.Fatal("reset hash or write retried", boundary.calls, f.hasher.calls.Load())
			}
			committed := mode == "unknown_commit" || mode == "late_cancel"
			if mode == "token_rejected" && err != identity.ErrInvalidResetToken || mode == "token_cleanup" && (err == identity.ErrInvalidResetToken || !errors.Is(err, identity.ErrInvalidResetToken)) {
				t.Fatal("token rollback classification changed", mode, err)
			}
			if mode == "late_cancel" {
				if err != nil || ctx.Err() != context.Canceled {
					t.Fatal("confirmed commit changed into failure", err)
				}
			} else if err == nil {
				t.Fatal("unconfirmed reset returned success", mode)
			}
			if _, rejected := validation.Rejected(err); rejected != (mode == "validation") {
				t.Fatal("validation/cleanup classification changed", mode, err)
			}
			if mode == "unknown_commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || mode == "unknown_rollback" && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || (mode == "cancel" || mode == "hash_cancel") && !errors.Is(err, context.Canceled) {
				t.Fatal("reset failure cause lost", mode, err)
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !committed && (!reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit)) {
				t.Fatal("reset rollback lost state", mode)
			}
			if committed {
				assertResetCommit(t, f, before, selfPassword, 2)
				if len(afterRows) != 2 || len(afterAudit) != 1 {
					t.Fatal("reset commit not atomic")
				}
			}
			switch mode {
			case "swallow", "opaque", "password_write", "after_password", "audit_insert", "after_audit", "revoke":
				if boundary.faults != 1 {
					t.Fatal("required reset fault did not execute", mode, boundary.faults)
				}
			}
			requirePasswordPrivate(t, err, "private-password-fault", "private-reset-hash", selfPassword, before.EncodedPassword, token.Encoded())
		})
	}
}

func RunPasswordResetConcurrency(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"profile", "policy", "password", "email", "last_login", "inactive", "unusable", "deleted", "expired", "future"} {
		t.Run("current_fence/"+mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 4)
			now := loginInstant
			validations := 0
			validator := identity.PasswordValidatorFunc(func(_ string, p identity.Profile) validation.Errors {
				validations++
				if p.FirstName == "refuse-current" {
					return validation.NewErrors(validation.New("password", "password_too_similar"))
				}
				return validation.Errors{}
			})
			resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now, validator))
			token := resetToken(t, resetter, f.user.PrincipalID)
			otherRuntime, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			var edited models.User
			var afterRows, afterAudit [][]string
			f.hasher.hook = func(ctx context.Context) error {
				if mode == "deleted" {
					policy := managementHost(t, other)
					_, err := f.manager(t, otherRuntime).DeleteUser(ctx, f.actor, f.user.ID, 1, policy.AccountsUser)
					if err != nil {
						return err
					}
				} else if mode == "expired" || mode == "future" {
					if mode == "expired" {
						now = now.Add(time.Hour + time.Second)
					} else {
						now = now.Add(-time.Second)
					}
					edited = f.stored(t)
				} else {
					err := otherRuntime.CoordinatedAtomic(ctx, func(session db.Session) error {
						patch := models.UserPatch{}.WithRevision(2)
						switch mode {
						case "profile":
							patch = patch.WithUsername("renamed").WithFirstName("current profile").WithStaff(false)
						case "policy":
							patch = patch.WithFirstName("refuse-current")
						case "password":
							encoded, err := f.hasher.PasswordHasher.Hash(ctx, "concurrent password")
							if err != nil {
								return err
							}
							patch = patch.WithEncodedPassword(encoded)
						case "email":
							patch = patch.WithEmail("concurrent@example.test")
						case "last_login":
							patch = patch.WithLastLogin(loginInstant)
						case "inactive":
							patch = patch.WithActive(false)
						case "unusable":
							encoded, err := auth.MakeUnusablePassword(ctx)
							if err != nil {
								return err
							}
							patch = patch.WithEncodedPassword(encoded)
						}
						var err error
						edited, err = models.UserObjects.Update(ctx, session, f.user, patch)
						return err
					})
					if err != nil {
						return err
					}
				}
				afterRows, afterAudit = snapshotIdentitySystemRows(t, backend)
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			err = resetter.ResetPassword(ctx, token.PrincipalID(), token.Encoded(), selfPassword)
			f.hasher.hook = nil
			if f.hasher.calls.Load() != 1 {
				t.Fatal("reset repeated hash")
			}
			if mode == "profile" {
				if err != nil || validations != 2 {
					t.Fatal("unrelated profile edit rejected or current policy absent", err)
				}
				assertResetCommit(t, f, edited, selfPassword, 3)
			} else {
				if mode == "policy" {
					if _, rejected := validation.Rejected(err); !rejected || validations != 2 {
						t.Fatal("current reset policy not enforced", err)
					}
				} else if err != identity.ErrInvalidResetToken {
					t.Fatal("stale reset admitted", mode, err)
				}
				if mode != "deleted" && !reflect.DeepEqual(edited, f.stored(t)) {
					t.Fatal("reset restored a stale profile", mode)
				}
				rows, audit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
					t.Fatal("refused reset wrote session or audit", mode)
				}
			}
		})
	}
	t.Run("two_connections_one_reset_wins", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 4)
		now := loginInstant
		first := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		token := resetToken(t, first, f.user.PrincipalID)
		otherRuntime, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		second := resetService(t, otherRuntime, f.hasher, resetConfig(t, &now))
		ready, release := make(chan struct{}, 2), make(chan struct{})
		done := make(chan error, 2)
		f.hasher.hook = func(ctx context.Context) error {
			ready <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		before := f.stored(t)
		for _, service := range []*identity.PasswordResetter{first, second} {
			go func() { done <- service.ResetPassword(ctx, token.PrincipalID(), token.Encoded(), selfPassword) }()
		}
		for range 2 {
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("reset hash retained a database scope", ctx.Err())
			}
		}
		close(release)
		wins := 0
		for range 2 {
			err := <-done
			if err == nil {
				wins++
			} else if err != identity.ErrInvalidResetToken {
				t.Fatal("reset loser lost refusal", err)
			}
		}
		f.hasher.hook = nil
		rows, audit := snapshotIdentitySystemRows(t, backend)
		if wins != 1 || f.hasher.calls.Load() != 2 || len(rows) != 2 || len(audit) != 1 {
			t.Fatal("racing reset did not have one atomic winner", wins)
		}
		assertResetCommit(t, f, before, selfPassword, 2)
	})
}
