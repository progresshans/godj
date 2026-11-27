package accountstoretest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

// Keep ordinary self-service separate from administrative password changes.
// The fixture includes host FKs, other users, anonymous sessions, an old stamp
// in the current namespace and an identical bearer in a historical namespace.
func provisionSelfPassword(t *testing.T, backend Backend) *loginFixture {
	t.Helper()
	f := provisionLogin(t, backend, false)
	at := nativeLoginInstant.Add(-time.Hour)
	if _, err := models.UserObjects.Patch(t.Context(), backend, f.target, models.UserPatch{}.WithLastLogin(at)); err != nil {
		t.Fatal(err)
	}
	f.target.LastLogin = &at
	if _, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("self password host", f.target.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(f.target.ID)); err != nil {
		t.Fatal(err)
	}
	stale, err := f.runtime.Sessions().Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.target.PrincipalID, auth.SessionCredentialStampKey: "previous-credential-stamp", "payload": "stale-target"})
	if err != nil {
		t.Fatal(err)
	}
	f.targetSessions = append(f.targetSessions, stale)
	database, err := sessiondb.New(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	historical, err := database.Store(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if created, err := historical.Create(t.Context(), f.targetSessions[0]); err != nil || !created {
		t.Fatal("self password history fixture", err)
	}
	return f
}

// The only generated database values accepted from after are fresh row keys.
// Password bytes come from the hasher before persistence, the bearer from the
// entropy source, and every remaining value from the request and before-state.
func selfPasswordExpected(t *testing.T, f *loginFixture, owner *userReferenceOwner, before, after userReferenceState, previous sessions.Record, id sessions.ID, at time.Time) (userReferenceState, auth.PasswordChangeResult) {
	t.Helper()
	encoded, ok := owner.encoded.Load().(string)
	if !ok || encoded == "" || encoded == f.target.EncodedPassword {
		t.Fatal("self password observation lacks its fresh upstream hash")
	}
	domain, ok := f.credential.Principal().CredentialDomain()
	if !ok {
		t.Fatal("self password observation lacks the selected domain")
	}
	credential, err := auth.NewCredentialForDomain(domain, f.target.Username, encoded, f.credential.Principal())
	if err != nil {
		t.Fatal(err)
	}
	record := loginExpectedRecord(t, credential, previous, "rotate", id, at)
	row := f.target
	row.EncodedPassword, row.Revision = encoded, row.Revision+1
	userReferenceReplace(&before.account, row)
	namespace := loginNamespace(t, credential)
	for _, old := range f.targetSessions {
		loginRemoveRecord(t, &before.account, namespace, old.ID())
	}
	loginPutRecord(t, &before.account, after.account, namespace, record, false)
	return before, auth.PasswordChangeResult{Credential: credential, Record: record}
}

func assertSelfPasswordResult(t *testing.T, result, expected auth.PasswordChangeResult) {
	t.Helper()
	if !reflect.DeepEqual(result.Record.Snapshot(), expected.Record.Snapshot()) || !result.Credential.MatchesSessionStamp(expected.Credential.SessionStamp()) ||
		!reflect.DeepEqual(result.Credential.Principal(), expected.Credential.Principal()) {
		t.Fatal("self password result differs from its independently prepared request")
	}
}

func selfPasswordPrivate(t *testing.T, value any, material ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal("password diagnostic serialization failed", err)
	}
	diagnostic := fmt.Sprintf("%v %+v %#v %s %q %d %x", value, value, value, value, value, value, value) + string(encoded)
	for _, secret := range material {
		if secret != "" && strings.Contains(diagnostic, secret) {
			t.Fatal("password material appeared in diagnostics")
		}
	}
}

func selfPasswordLifecycle(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	// Cross the native inventory batch boundary with interleaved target,
	// unrelated authenticated and anonymous rows, including late target rows.
	for index := range 260 {
		switch index % 3 {
		case 0:
			f.targetSessions = append(f.targetSessions, passwordSession(t, f.runtime.Sessions(), f.credential, fmt.Sprintf("self-batch-target-%d", index)))
		case 1:
			passwordSession(t, f.runtime.Sessions(), f.actor, fmt.Sprintf("self-batch-control-%d", index))
		case 2:
			if _, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": fmt.Sprintf("self-batch-%d", index)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	old := password
	for _, mode := range []string{"changed", "same_password_new_salt"} {
		if !t.Run(mode, func(t *testing.T) {
			previous := f.targetSessions[0]
			next := password + "-self-changed"
			f.instant.Store(f.clock().Add(time.Minute + 999*time.Nanosecond).UnixNano())
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			f.owner.hasher.hashes.Store(0)
			f.owner.hasher.verifies.Store(0)
			f.owner.boundary.calls = 0
			f.owner.policies.Store(0)
			result, err := f.runtime.PasswordChangePersistence().ChangePassword(t.Context(), previous.ID(), old, next)
			if err != nil {
				t.Fatal("ordinary selected self password change", err)
			}
			after := userReferenceSnapshot(t, reopen(t))
			expected, envelope := selfPasswordExpected(t, f, f.owner, before, after, previous, f.entropy.one(t, entropy), f.clock())
			assertSelfPasswordResult(t, result, envelope)
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			if f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 1 || f.owner.boundary.calls != 1 || f.owner.policies.Load() != 2 {
				t.Fatal("self password repeated or skipped hash, verification, policy or persistence")
			}
			for _, value := range []any{result, f.runtime.PasswordChangePersistence()} {
				selfPasswordPrivate(t, value, old, next, f.target.EncodedPassword, f.owner.encoded.Load().(string), previous.ID().Encoded(), result.Record.ID().Encoded(), f.credential.SessionStamp(), result.Credential.SessionStamp())
			}
			reopened := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
			credential, err := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, next)
			if err != nil || !credential.MatchesSessionStamp(envelope.Credential.SessionStamp()) {
				t.Fatal("self password credential did not survive reconnect", err)
			}
			if mode == "changed" {
				if _, err := reopened.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, old); err != auth.ErrInvalidCredentials {
					t.Fatal("previous password remained usable", err)
				}
			}
			if record, present, err := reopened.runtime.Sessions().Peek(t.Context(), result.Record.ID()); err != nil || !present || !reflect.DeepEqual(record.Snapshot(), envelope.Record.Snapshot()) {
				t.Fatal("self password session did not survive reconnect", err)
			}
			f.target.EncodedPassword, f.target.Revision = f.owner.encoded.Load().(string), f.target.Revision+1
			f.credential, f.targetSessions, old = envelope.Credential, []sessions.Record{envelope.Record}, next
		}) {
			return
		}
	}
	assertPasswordReferenceDeviations(t)
}

func selfPasswordWriteFailures(t *testing.T, open Open) {
	modes := []string{"skipped", "nil_session", "root_session", "repeated", "query", "opaque", "swallowed_failure", "before_rotation", "after_rotation", "before_session", "after_session", "before_password", "after_password", "before_revoke", "after_revoke", "before_audit", "after_audit", "validator_rejection", "validator_failure", "validator_cleanup", "swallowed_rejection", "callback_cancel", "cancel_before_commit", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"}
	for _, point := range []string{"before_password", "after_password"} {
		for _, kind := range []string{"entropy", "authentication", "diagnostics"} {
			modes = append(modes, point+"_"+kind)
		}
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			previous := f.targetSessions[0]
			f.instant.Store(f.clock().Add(time.Minute + 999*time.Nanosecond).UnixNano())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			point, kind := mode, ""
			for _, candidate := range []string{"entropy", "authentication", "diagnostics"} {
				if strings.HasSuffix(mode, "_"+candidate) {
					point, kind = strings.TrimSuffix(mode, "_"+candidate), candidate
				}
			}
			var fault error = errors.New("private self password native fault")
			if mode == "opaque" {
				point, fault = "query", passwordWriteOpaqueError{"private opaque material"}
			}
			if mode == "swallowed_failure" {
				point = "query"
			}
			switch kind {
			case "entropy":
				fault = &sessions.Error{Code: sessions.CodeEntropy}
			case "authentication":
				fault = auth.ErrInvalidCredentials
			case "diagnostics":
				fault = validation.Reject(validation.NewErrors(validation.New("password", "native_failure")), nil)
			}
			boundary := &profileFaultBackend{Backend: backend, mode: point, fault: fault, cancel: cancel}
			if mode == "swallowed_failure" {
				boundary.mode = "swallowed_rejection"
			}
			if mode == "validator_cleanup" {
				boundary.mode = "cleanup_failure"
			}
			var effects []string
			faults := 0
			boundary.decorate = func(scope db.Session) db.Session {
				return &passwordWriteFaultScope{Session: scope, mode: point, fault: fault, effects: &effects, faults: &faults}
			}
			config := f.config(t)
			final := false
			config.Accounts.ValidatePassword = func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
				if final && (mode == "validator_rejection" || mode == "validator_failure" || mode == "validator_cleanup" || mode == "swallowed_rejection") {
					diagnostics := validation.NewErrors(validation.New("password", "current_policy"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
				return validation.Errors{}, nil
			}
			owner := newUserReferenceOwner(t, boundary, f.userReferenceFixture, config)
			owner.hasher.beforeHash = func(context.Context) error { final = true; return nil }
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			result, err := owner.runtime.PasswordChangePersistence().ChangePassword(ctx, previous.ID(), password, password+"-self-changed")
			id := f.entropy.one(t, entropy)
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || ctx.Err() != context.Canceled {
					t.Fatal("late cancellation erased confirmed self password change", err)
				}
			} else {
				passwordNoResult(t, result, err)
				if mode == "validator_rejection" {
					if diagnostics, ok := validation.Rejected(err); !ok || diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil {
						t.Fatal("confirmed current policy refusal was not retained", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected || err == auth.ErrInvalidCredentials {
						t.Fatal("native self password failure became renderable refusal", err)
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "swallowed_failure" || mode == "unknown_commit" || mode == "unknown_rollback" || errors.Is(boundary.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(boundary.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
			}
			if boundary.calls != 1 || owner.boundary.calls != 1 || owner.hasher.hashes.Load() != 1 || owner.hasher.verifies.Load() != 1 {
				t.Fatal("self password failure replayed persistence or password work")
			}
			want := []string{"rotation", "session", "password"}
			for range len(f.targetSessions) - 1 {
				want = append(want, "revoke")
			}
			want = append(want, "audit")
			if point == "skipped" || point == "nil_session" || point == "root_session" || point == "query" || strings.HasPrefix(point, "validator_") || point == "swallowed_rejection" {
				want = nil
			}
			if strings.HasPrefix(point, "before_") || strings.HasPrefix(point, "after_") {
				name := strings.TrimPrefix(strings.TrimPrefix(point, "before_"), "after_")
				index := slices.Index(want, name)
				if index < 0 {
					t.Fatal("unknown self password fault point")
				}
				if strings.HasPrefix(point, "after_") {
					index++
				}
				want = want[:index]
			}
			if !slices.Equal(effects, want) {
				t.Fatal("self password fault missed its native write prefix", mode, effects, want)
			}
			if point == "query" || strings.HasPrefix(point, "before_") || strings.HasPrefix(point, "after_") {
				matches := errors.Is(err, fault)
				if mode == "opaque" {
					var observed passwordWriteOpaqueError
					matches = errors.As(err, &observed) && reflect.DeepEqual(observed, fault)
				}
				if faults != 1 || !matches {
					t.Fatal("self password native error lost its unique cause", err)
				}
			}
			if mode == "callback_cancel" || mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" {
				if !errors.Is(err, context.Canceled) || boundary.nativeError == nil || !errors.Is(err, boundary.nativeError) {
					t.Fatal("self password cancellation lost its native outcome", err)
				}
				if mode == "cancel_confirmed_rollback" && (errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
					t.Fatal("confirmed self password rollback became uncertain", err)
				}
			}
			after := userReferenceSnapshot(t, reopen(t))
			if committed {
				expected, envelope := selfPasswordExpected(t, f, owner, before, after, previous, id, f.clock())
				assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				if mode == "post_commit_cancel" {
					assertSelfPasswordResult(t, result, envelope)
				}
			} else if !reflect.DeepEqual(before, after) {
				t.Fatal("refused self password change altered account, catalog, host, session or history state")
			}
			for _, material := range []string{password, owner.encoded.Load().(string), id.Encoded()} {
				if strings.Contains(fmt.Sprintf("%+v", err), material) || strings.Contains(fmt.Sprintf("%#v", result), material) {
					t.Fatal("self password failure disclosed private material")
				}
			}
			selfPasswordPrivate(t, err, password, owner.encoded.Load().(string), id.Encoded(), "private self password native fault", "private password opaque fault", "private opaque material")
		})
	}
}

func runSelfPasswordNative(add registerCase, open Open) {
	add("self_password_lifecycle", func(t *testing.T) { selfPasswordLifecycle(t, open) })
	add("self_password_write_failures", func(t *testing.T) { selfPasswordWriteFailures(t, open) })
	add("self_password_configuration", func(t *testing.T) { selfPasswordConfiguration(t, open) })
	add("self_password_refusals", func(t *testing.T) { selfPasswordRefusals(t, open) })
	add("self_password_current", func(t *testing.T) { selfPasswordCurrent(t, open) })
	add("self_password_web", func(t *testing.T) { selfPasswordWeb(t, open) })
}
