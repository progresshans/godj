package identitytest

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

//go:embed testdata/password-change-django61-*.json
var passwordChangeReferences embed.FS

const selfPassword = "  replacement private password  "

func passwordChangeBinding(t *testing.T, f *managementFixture, backend TransitionBackend, validators ...identity.PasswordValidator) (*systemstate.Runtime, *sessions.Manager, auth.PasswordChangePersistence, sessions.Record) {
	t.Helper()
	runtime, err := systemstate.OpenIdentity(t.Context(), backend, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return loginInstant.Add(time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := runtime.PasswordChangePersistence(manager, validators...)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: credential.Principal().ID(), auth.SessionCredentialStampKey: credential.SessionStamp(), "payload": "preserved private data"})
	if err != nil {
		t.Fatal(err)
	}
	f.hasher.calls.Store(0)
	return runtime, manager, provider, record
}

func assertSelfPasswordCommit(t *testing.T, f *managementFixture, runtime *systemstate.Runtime, before models.User, previous sessions.Record, result auth.PasswordChangeResult, password string, revision int64) {
	t.Helper()
	stored := f.stored(t)
	if stored.Revision != revision || stored.EncodedPassword == before.EncodedPassword || !reflect.DeepEqual(stored.LastLogin, before.LastLogin) {
		t.Fatal("self password change lost credential/revision/last_login semantics")
	}
	verified, err := f.hasher.Verify(t.Context(), password, stored.EncodedPassword)
	if err != nil || !verified {
		t.Fatal("new raw password does not authenticate", err)
	}
	current := result.Record
	if !current.ID().Valid() || current.ID() == previous.ID() || !current.CreatedAt().Equal(previous.CreatedAt()) || !current.AbsoluteExpiresAt().Equal(previous.AbsoluteExpiresAt()) {
		t.Fatal("self password change did not rotate the existing lifetime")
	}
	if value, ok := current.Value("payload"); !ok || value != "preserved private data" {
		t.Fatal("current payload was lost")
	}
	stamp, _ := current.Value(auth.SessionCredentialStampKey)
	if !result.Credential.MatchesSessionStamp(stamp) || result.Credential.Principal().ID() != f.user.PrincipalID {
		t.Fatal("new session is not bound to the changed credential")
	}
	if _, found, err := runtime.SessionStore().Load(t.Context(), previous.ID()); err != nil || found {
		t.Fatal("old current session survived", err)
	}
	for index, id := range f.ids {
		_, found, err := runtime.SessionStore().Load(t.Context(), id)
		if err != nil || found != (index%3 != 0) {
			t.Fatal("wrong other session retained or revoked", index, err)
		}
	}
	history, err := runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
	if err != nil || len(history) == 0 {
		t.Fatal("missing self password audit", err)
	}
	event := history[len(history)-1]
	if event.ActorID != f.user.PrincipalID || event.Action != admin.ActionChange || event.DisplayLabel != "" || !reflect.DeepEqual(event.ChangedFields, []string{"password"}) {
		t.Fatal("self password audit lost actor or disclosed values")
	}
}

func RunPasswordChange(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("prepared_change_ownership_and_secret_privacy", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		runtime, _, _, previous := passwordChangeBinding(t, f, backend)
		confirmer, ok := runtime.Authenticator().(auth.PasswordConfirmer)
		if !ok {
			t.Fatal("stored authenticator has no confirmation capability")
		}
		validators := []identity.PasswordValidator{identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors { return validation.Errors{} })}
		changer, err := identity.NewPasswordChanger(runtime, confirmer, f.hasher, validators...)
		if err != nil {
			t.Fatal(err)
		}
		validators[0] = identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
			return validation.NewErrors(validation.New("password", "forbidden"))
		})
		rows, audit := snapshotIdentitySystemRows(t, backend)
		before := f.stored(t)
		prepared, err := changer.Prepare(t.Context(), previous, managementOldPassword, selfPassword)
		if err != nil || f.hasher.calls.Load() != 1 {
			t.Fatal("prepared policy did not own its validators or password work", err)
		}
		stamp, _ := previous.Value(auth.SessionCredentialStampKey)
		requirePasswordPrivate(t, prepared, managementOldPassword, selfPassword, before.EncodedPassword, previous.ID().Encoded(), stamp)
		bound, err := prepared.BindSession(previous)
		if err != nil || bound.ID() != previous.ID() {
			t.Fatal("binding unexpectedly rotated or rejected", err)
		}
		newStamp, _ := bound.Value(auth.SessionCredentialStampKey)
		oldStamp, _ := previous.Value(auth.SessionCredentialStampKey)
		if newStamp == oldStamp || stamp != oldStamp {
			t.Fatal("binding failed to derive an independent stamp")
		}
		for _, mode := range []string{"identity", "stamp", "missing", "zero"} {
			invalid := previous
			switch mode {
			case "identity":
				invalid, err = invalid.WithValue(auth.SessionPrincipalIDKey, "someone-else")
			case "stamp":
				invalid, err = invalid.WithValue(auth.SessionCredentialStampKey, "stale-stamp")
			case "missing":
				invalid = invalid.WithoutValue(auth.SessionCredentialStampKey)
			case "zero":
				invalid = sessions.Record{}
			}
			if err != nil {
				t.Fatal(err)
			}
			if result, err := prepared.BindSession(invalid); err != auth.ErrInvalidCredentials || result.ID().Valid() {
				t.Fatal("prepared change accepted another session binding", mode, err)
			}
		}
		other, err := identity.NewPasswordChanger(runtime, confirmer, f.hasher)
		if err != nil {
			t.Fatal(err)
		}
		err = runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			result, err := other.ApplyIn(t.Context(), session, prepared)
			if result.Principal().ID() != "" {
				t.Fatal("foreign prepared owner published credential")
			}
			return err
		})
		if !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
			t.Fatal("prepared change crossed owner", err)
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
			t.Fatal("preparation or rejected owner published effects")
		}
	})
	t.Run("ordinary_user_preserves_current_revokes_others_and_reopens", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 260)
		if _, err := models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithStaff(false).WithLastLogin(loginInstant)); err != nil {
			t.Fatal(err)
		}
		runtime, _, provider, previous := passwordChangeBinding(t, f, backend)
		before := f.stored(t)
		rows, _ := snapshotIdentitySystemRows(t, backend)
		result, err := provider.ChangePassword(t.Context(), previous.ID(), managementOldPassword, selfPassword)
		if err != nil || result.Credential.Principal().Has(identity.ChangeUser) || result.Credential.Principal().Staff() || result.Credential.Principal().Superuser() {
			t.Fatal("ordinary user's password change failed", err)
		}
		assertSelfPasswordCommit(t, f, runtime, before, previous, result, selfPassword, 2)
		if f.hasher.calls.Load() != 1 {
			t.Fatal("new password hash work repeated")
		}
		if _, err := runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword); err != auth.ErrInvalidCredentials {
			t.Fatal("old password still authenticates", err)
		}
		if _, err := runtime.Authenticator().Authenticate(t.Context(), "member", strings.TrimSpace(selfPassword)); err != auth.ErrInvalidCredentials {
			t.Fatal("raw password whitespace was lost", err)
		}
		afterRows, _ := snapshotIdentitySystemRows(t, backend)
		retained := 0
		for _, row := range afterRows {
			if containsStoredRow(rows, row) {
				retained++
			}
		}
		if retained != 173 || len(afterRows) != 174 {
			t.Fatal("foreign and anonymous session bytes changed", retained, len(afterRows))
		}
		// Native Django keeps the other row until access. GoDj revokes it in
		// the password transaction; both make the old session unauthenticated.
		for _, database := range []string{"sqlite", "postgres"} {
			payload, err := passwordChangeReferences.ReadFile("testdata/password-change-django61-" + database + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var reference struct {
				Django       string
				Observations struct {
					Success      map[string]any
					SamePassword map[string]any `json:"same_password"`
					Failures     map[string]map[string]any
					Races        map[string]map[string]any `json:"post_validation_change"`
				}
			}
			if err := json.Unmarshal(payload, &reference); err != nil || reference.Django != "6.1" {
				t.Fatal("invalid self password reference", err)
			}
			want := map[string]any{"session_rotated": true, "old_session_retained": false, "old_password_valid": false, "new_password_valid": true, "trimmed_password_valid": false, "last_login": loginInstant.Format("2006-01-02T15:04:05.999999-07:00"), "active": true, "staff": false, "superuser": false}
			for key, value := range want {
				if !reflect.DeepEqual(reference.Observations.Success[key], value) {
					t.Fatal("native common password behavior drifted", database, key)
				}
			}
			if reference.Observations.Success["other_session_before_access"] != true || reference.Observations.Failures["session_cycle"]["password_changed"] != true || reference.Observations.Races["inactive"]["active"] != true || reference.Observations.SamePassword["hash_changed"] != true {
				t.Fatal("native intentional differences were hidden")
			}
		}
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		current, err := reopened.Authenticator().Authenticate(t.Context(), "member", selfPassword)
		if err != nil || !current.MatchesSessionStamp(result.Credential.SessionStamp()) {
			t.Fatal("reopened password binding changed", err)
		}
		loaded, found, err := reopened.SessionStore().Load(t.Context(), result.Record.ID())
		if err != nil || !found || !reflect.DeepEqual(loaded, result.Record) {
			t.Fatal("reopened session changed", err)
		}
		before = f.stored(t)
		repeated, err := provider.ChangePassword(t.Context(), result.Record.ID(), selfPassword, selfPassword)
		if err != nil {
			t.Fatal("same raw password replacement failed", err)
		}
		assertSelfPasswordCommit(t, f, runtime, before, result.Record, repeated, selfPassword, 3)
		if f.hasher.calls.Load() != 2 {
			t.Fatal("same password work repeated")
		}
	})
	for _, mode := range []string{"wrong_old", "trimmed_old", "empty_new", "weak", "missing", "anonymous", "partial", "stale", "inactive", "unusable", "canceled", "nil_context", "same_hash"} {
		t.Run("refusal/"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 4)
			minimum, err := identity.NewMinimumLengthValidator(8)
			if err != nil {
				t.Fatal(err)
			}
			runtime, manager, provider, previous := passwordChangeBinding(t, f, backend, minimum)
			old, next := managementOldPassword, selfPassword
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "wrong_old":
				old = "wrong"
			case "trimmed_old":
				old = " " + managementOldPassword
			case "empty_new":
				next = ""
			case "weak":
				next = "short"
			case "missing":
				if err := manager.Delete(t.Context(), previous.ID()); err != nil {
					t.Fatal(err)
				}
			case "anonymous", "partial", "stale":
				values := map[string]string{"payload": "preserve"}
				if mode != "anonymous" {
					values[auth.SessionPrincipalIDKey] = f.user.PrincipalID
				}
				if mode == "stale" {
					values[auth.SessionCredentialStampKey] = "stale-stamp"
				}
				previous, err = manager.Create(t.Context(), values)
				if err != nil {
					t.Fatal(err)
				}
			case "inactive":
				_, err = models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithActive(false))
			case "unusable":
				_, err = models.UserObjects.Update(t.Context(), backend, f.user, models.UserPatch{}.WithEncodedPassword("!disabled"))
			case "canceled":
				cancel()
			case "nil_context":
				ctx = nil
			case "same_hash":
				f.hasher.fixed = f.user.EncodedPassword
			}
			if err != nil {
				t.Fatal(err)
			}
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			result, err := provider.ChangePassword(ctx, previous.ID(), old, next)
			if err == nil || result.Record.ID().Valid() || result.Credential.Principal().ID() != "" {
				t.Fatal("invalid change published a result", mode)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if diagnostics, rejected := validation.Rejected(err); mode == "wrong_old" || mode == "trimmed_old" || mode == "weak" || mode == "empty_new" {
				if !rejected || diagnostics.Empty() {
					t.Fatal("expected input refusal lost", mode, err)
				}
			} else if rejected {
				t.Fatal("execution/admission failure became validation", mode)
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
				t.Fatal("refused change had side effects", mode)
			}
			wantHashes := int64(0)
			if mode == "same_hash" {
				wantHashes = 1
			}
			if f.hasher.calls.Load() != wantHashes {
				t.Fatal("refusal performed unexpected hash work", f.hasher.calls.Load())
			}
			_ = runtime
		})
	}
	for _, mode := range []string{"other_manager", "typed_nil_validator", "nil_manager", "valid"} {
		t.Run("binding/"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 0)
			runtime, manager, _, _ := passwordChangeBinding(t, f, backend)
			var validators []identity.PasswordValidator
			if mode == "other_manager" {
				manager, _ = sessions.NewManager(f.runtime.SessionStore(), sessions.Config{})
			}
			if mode == "nil_manager" {
				manager = nil
			}
			if mode == "typed_nil_validator" {
				var missing identity.PasswordValidatorFunc
				validators = []identity.PasswordValidator{missing}
			}
			provider, err := runtime.PasswordChangePersistence(manager, validators...)
			if mode == "valid" {
				if err != nil || provider.Sessions() != manager {
					t.Fatal(err)
				}
			} else if err == nil || provider != nil {
				t.Fatal("invalid binding accepted")
			}
		})
	}
}

func requirePasswordPrivate(t *testing.T, value any, material ...string) {
	t.Helper()
	encoded, _ := json.Marshal(value)
	diagnostic := fmt.Sprintf("%v %+v %#v %s %q %d %x", value, value, value, value, value, value, value) + string(encoded)
	for _, secret := range material {
		if secret != "" && strings.Contains(diagnostic, secret) {
			t.Fatal("password material appeared in diagnostics")
		}
	}
}
