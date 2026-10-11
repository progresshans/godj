package identitytest

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

//go:embed testdata/password-reset-http-django61-*.json
var resetHTTPReferences embed.FS

func resetSessionBinding(t *testing.T, f *managementFixture, backend TransitionBackend, now *time.Time, config sessions.Config, validators ...identity.PasswordValidator) (*systemstate.Runtime, *systemstate.PasswordResetPersistence) {
	t.Helper()
	runtime, err := systemstate.OpenIdentity(t.Context(), backend, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return *now }
	}
	manager, err := sessions.NewManager(runtime.SessionStore(), config)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := runtime.PasswordResetPersistence(manager, resetConfig(t, now, validators...))
	if err != nil {
		t.Fatal(err)
	}
	// Stored-authenticator startup prepares a dummy hash. Measure only the
	// requested proof/reset operation after initialization has completed.
	f.hasher.calls.Store(0)
	return runtime, provider
}

func resetPrevious(t *testing.T, runtime *systemstate.Runtime, manager *sessions.Manager, mode, target string) sessions.Record {
	t.Helper()
	if mode == "missing" {
		return sessions.Record{}
	}
	values := map[string]string{"payload": "preserved private reset payload"}
	if mode == "self" || mode == "other" {
		id := target
		if mode == "other" {
			id = "manager"
		}
		credential, err := runtime.Authenticator().Resolve(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		values[auth.SessionPrincipalIDKey] = id
		values[auth.SessionCredentialStampKey] = credential.SessionStamp()
	}
	record, err := manager.Create(t.Context(), values)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func startResetProof(t *testing.T, p *systemstate.PasswordResetPersistence, previous sessions.Record, target string) (sessions.Record, identity.PasswordResetToken) {
	t.Helper()
	token := resetToken(t, p.Resetter(), target)
	record, err := p.StartPasswordReset(t.Context(), previous.ID(), target, token.Encoded())
	if err != nil {
		t.Fatal("reset proof start failed", err)
	}
	if record.ID() == previous.ID() || !record.ID().Valid() {
		t.Fatal("reset proof did not get a fresh session ID")
	}
	return record, token
}

func RunPasswordResetSessions(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"missing", "anonymous", "self", "other"} {
		t.Run("lifecycle/"+mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 7)
			now := loginInstant
			minimum, err := identity.NewMinimumLengthValidator(8)
			if err != nil {
				t.Fatal(err)
			}
			runtime, p := resetSessionBinding(t, f, backend, &now, sessions.Config{}, minimum)
			previous := resetPrevious(t, runtime, p.Sessions(), mode, f.user.PrincipalID)
			before := f.stored(t)
			proof, token := startResetProof(t, p, previous, f.user.PrincipalID)
			if !reflect.DeepEqual(before, f.stored(t)) || f.hasher.calls.Load() != 0 {
				t.Fatal("proof start changed user or hashed")
			}
			if previous.ID().Valid() {
				if _, found, err := runtime.SessionStore().Load(t.Context(), previous.ID()); err != nil || found {
					t.Fatal("old proof-entry ID survived", err)
				}
				if value, _ := proof.Value("payload"); value != "preserved private reset payload" || !proof.CreatedAt().Equal(previous.CreatedAt()) || !proof.AbsoluteExpiresAt().Equal(previous.AbsoluteExpiresAt()) {
					t.Fatal("entry lost payload or absolute lifetime")
				}
			}
			if value, _ := proof.Value(auth.SessionResetTokenKey); value != token.Encoded() {
				t.Fatal("wrong server-side token")
			}
			rows, audit := snapshotIdentitySystemRows(t, other)
			if err := p.CheckPasswordReset(t.Context(), proof.ID(), f.user.PrincipalID, nil); err != nil {
				t.Fatal(err)
			}
			weak := "short"
			if _, rejected := validation.Rejected(p.CheckPasswordReset(t.Context(), proof.ID(), f.user.PrincipalID, &weak)); !rejected {
				t.Fatal("reset proof lost policy diagnostics")
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, other)
			if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || f.hasher.calls.Load() != 0 {
				t.Fatal("reset preflight touched session or hashed")
			}
			// Reopen from an independent connection before consumption.
			reopened, consumer := resetSessionBinding(t, f, other, &now, sessions.Config{}, minimum)
			now = now.Add(time.Second)
			result, err := consumer.ResetPassword(t.Context(), proof.ID(), f.user.PrincipalID, selfPassword)
			if err != nil || result.ClearSession != (mode == "self") || f.hasher.calls.Load() != 1 {
				t.Fatal("reset proof completion failed", err)
			}
			assertResetCommit(t, f, before, selfPassword, before.Revision+1)
			if _, found, err := runtime.SessionStore().Load(t.Context(), proof.ID()); err != nil || found {
				t.Fatal("consumed proof ID survived", err)
			}
			authenticated := false
			if mode == "self" {
				if result.Record.ID().Valid() {
					t.Fatal("reset recreated target's authentication session")
				}
			} else {
				record := result.Record
				if !record.ID().Valid() || record.ID() == proof.ID() || !record.CreatedAt().Equal(proof.CreatedAt()) || !record.AbsoluteExpiresAt().Equal(proof.AbsoluteExpiresAt()) {
					t.Fatal("proof removal did not rotate existing lifetime")
				}
				if _, found := record.Value(auth.SessionResetTokenKey); found {
					t.Fatal("reset token survived completion")
				}
				if _, found := record.Value(auth.SessionResetPrincipalIDKey); found {
					t.Fatal("reset target survived completion")
				}
				if mode != "missing" {
					if value, _ := record.Value("payload"); value != "preserved private reset payload" {
						t.Fatal("reset lost unrelated payload")
					}
				}
				stored, found, err := reopened.SessionStore().Load(t.Context(), record.ID())
				if err != nil || !found || !reflect.DeepEqual(stored, record) {
					t.Fatal("published reset session was not durable", err)
				}
				if id, _ := record.Value(auth.SessionPrincipalIDKey); id != "" {
					credential, err := reopened.Authenticator().Resolve(t.Context(), id)
					stamp, _ := record.Value(auth.SessionCredentialStampKey)
					authenticated = err == nil && credential.Principal().Authenticated() && credential.MatchesSessionStamp(stamp)
				}
			}
			for _, backend := range []string{"sqlite", "postgres"} {
				payload, err := resetHTTPReferences.ReadFile("testdata/password-reset-http-django61-" + backend + ".json")
				if err != nil {
					t.Fatal(err)
				}
				var reference struct {
					Observations struct {
						Success            map[string]map[string]any
						SessionSaveFailure map[string]any `json:"session_save_failure"`
					}
				}
				if err := json.Unmarshal(payload, &reference); err != nil {
					t.Fatal(err)
				}
				key := mode
				if key == "missing" {
					key = "anonymous"
				}
				native := reference.Observations.Success[key]
				if native["authenticated"] != authenticated || native["token_removed"] != true || native["last_login_unchanged"] != true || native["trimmed_password_valid"] != false {
					t.Fatal("native common reset session behavior differs", mode)
				}
				if native["old_session_before_access"] != true || reference.Observations.SessionSaveFailure["new_password_valid"] != true {
					t.Fatal("native non-atomic differences were hidden")
				}
			}
			if err := consumer.CheckPasswordReset(t.Context(), proof.ID(), f.user.PrincipalID, nil); err != auth.ErrInvalidResetProof {
				t.Fatal("consumed proof remained admitted", err)
			}
			if _, err := consumer.StartPasswordReset(t.Context(), sessions.ID{}, f.user.PrincipalID, token.Encoded()); err != auth.ErrInvalidResetProof {
				t.Fatal("old token produced another proof", err)
			}
			requirePasswordPrivate(t, p, token.Encoded(), proof.ID().Encoded(), selfPassword)
			requirePasswordPrivate(t, result, token.Encoded(), proof.ID().Encoded(), selfPassword)
		})
	}
	t.Run("replaced_proof_wrong_target_and_invalid_start", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 1)
		now := loginInstant
		_, p := resetSessionBinding(t, f, backend, &now, sessions.Config{})
		proof, token := startResetProof(t, p, sessions.Record{}, f.user.PrincipalID)
		before := f.stored(t)
		rows, audit := snapshotIdentitySystemRows(t, backend)
		for _, target := range []string{f.root.PrincipalID, "missing", ""} {
			if err := p.CheckPasswordReset(t.Context(), proof.ID(), target, nil); err != auth.ErrInvalidResetProof {
				t.Fatal("wrong target admitted", err)
			}
			if _, err := p.ResetPassword(t.Context(), proof.ID(), target, selfPassword); err != auth.ErrInvalidResetProof {
				t.Fatal("wrong target reset", err)
			}
		}
		if _, err := p.StartPasswordReset(t.Context(), proof.ID(), f.user.PrincipalID, "bad-token"); err != auth.ErrInvalidResetProof {
			t.Fatal("invalid entry admitted", err)
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) || f.hasher.calls.Load() != 0 {
			t.Fatal("refused proof changed state")
		}
		replaced, _ := startResetProof(t, p, proof, f.root.PrincipalID)
		if err := p.CheckPasswordReset(t.Context(), proof.ID(), f.user.PrincipalID, nil); err != auth.ErrInvalidResetProof {
			t.Fatal("replaced proof session survived", err)
		}
		if err := p.CheckPasswordReset(t.Context(), replaced.ID(), f.user.PrincipalID, nil); err != auth.ErrInvalidResetProof {
			t.Fatal("new proof was reused for old target", err)
		}
		if err := p.CheckPasswordReset(t.Context(), replaced.ID(), f.root.PrincipalID, nil); err != nil {
			t.Fatal(err)
		}
		if err := p.Resetter().CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), nil); err != nil {
			t.Fatal("proof replacement changed the account token", err)
		}
	})
	t.Run("exact_storage_owner_and_session_limits", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 1)
		now := loginInstant
		runtime, p := resetSessionBinding(t, f, backend, &now, sessions.Config{Limits: sessions.Limits{MaxValues: 2}})
		foreign, err := sessions.NewManager(f.runtime.SessionStore(), sessions.Config{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.PasswordResetPersistence(foreign, resetConfig(t, &now)); err == nil {
			t.Fatal("another runtime's store was accepted")
		}
		previous := resetPrevious(t, runtime, p.Sessions(), "anonymous", f.user.PrincipalID)
		token := resetToken(t, p.Resetter(), f.user.PrincipalID)
		rows, audit := snapshotIdentitySystemRows(t, backend)
		if _, err := p.StartPasswordReset(t.Context(), previous.ID(), f.user.PrincipalID, token.Encoded()); err == nil {
			t.Fatal("configured session value limit accepted oversized proof")
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
			t.Fatal("manager limit failure committed a proof rotation")
		}
	})
}

func RunPasswordResetSessionBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, kind := range []string{"anonymous", "self"} {
		for _, mode := range []string{"zero", "nil", "twice", "swallow", "opaque", "password_write", "after_password", "audit_insert", "after_audit", "revoke", "cancel", "unknown_rollback", "unknown_commit", "late_cancel", "validation", "validation_cleanup", "expired_session", "session_insert", "after_session", "after_rotation_delete"} {
			if kind == "self" && (mode == "session_insert" || mode == "after_session" || mode == "after_rotation_delete") {
				continue
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 7)
				now := loginInstant
				boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: mode}
				validations := 0
				validator := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
					validations++
					if validations == 2 && (mode == "validation" || mode == "validation_cleanup") {
						return validation.NewErrors(validation.New("password", "password_too_similar"))
					}
					return validation.Errors{}
				})
				runtime, p := resetSessionBinding(t, f, boundary, &now, sessions.Config{}, validator)
				previous := resetPrevious(t, runtime, p.Sessions(), kind, f.user.PrincipalID)
				proof, token := startResetProof(t, p, previous, f.user.PrincipalID)
				before := f.stored(t)
				rows, audit := snapshotIdentitySystemRows(t, backend)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boundary.afterCommit = cancel
				f.hasher.hook = func(context.Context) error {
					boundary.armed = true
					if mode == "expired_session" {
						now = now.Add(31 * time.Minute)
					}
					return nil
				}
				result, err := p.ResetPassword(ctx, proof.ID(), f.user.PrincipalID, selfPassword)
				boundary.armed = false
				f.hasher.hook = nil
				if boundary.calls != 1 || f.hasher.calls.Load() != 1 {
					t.Fatal("reset proof repeated hashing or commit", boundary.calls, f.hasher.calls.Load())
				}
				committed := mode == "unknown_commit" || mode == "late_cancel"
				if mode == "late_cancel" {
					if err != nil || ctx.Err() != context.Canceled {
						t.Fatal("late cancellation undid confirmed reset", err)
					}
				} else if err == nil || result.Record.ID().Valid() || result.ClearSession {
					t.Fatal("unconfirmed reset published a result", mode, err)
				}
				if _, rejected := validation.Rejected(err); rejected != (mode == "validation") {
					t.Fatal("reset proof validation/cleanup classification changed", mode, err)
				}
				if mode == "expired_session" && err != auth.ErrInvalidResetProof {
					t.Fatal("expired proof admitted", err)
				}
				if mode == "unknown_commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || mode == "unknown_rollback" && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("reset proof outcome classification lost", mode, err)
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !committed {
					if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
						t.Fatal("failed reset lost proof/password/session/audit rollback", mode)
					}
				} else {
					assertResetCommit(t, f, before, selfPassword, 2)
					if _, found, err := runtime.SessionStore().Load(t.Context(), proof.ID()); err != nil || found {
						t.Fatal("committed reset retained old proof ID", err)
					}
				}
				switch mode {
				case "swallow", "opaque", "password_write", "after_password", "audit_insert", "after_audit", "revoke", "session_insert", "after_session", "after_rotation_delete":
					if boundary.faults != 1 {
						t.Fatal("required reset proof fault did not execute", mode, boundary.faults)
					}
				}
				requirePasswordPrivate(t, err, "private-password-fault", token.Encoded(), proof.ID().Encoded(), selfPassword)
			})
		}
	}
}

type resetAdvanceBoundary struct {
	TransitionBackend
	advance func()
}

func (b *resetAdvanceBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return b.TransitionBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if b.advance != nil {
			fn := b.advance
			b.advance = nil
			fn()
		}
		return callback(session)
	})
}

func RunPasswordResetSessionRaces(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("two_connections_consume_one_proof", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 4)
		now := loginInstant
		_, first := resetSessionBinding(t, f, backend, &now, sessions.Config{})
		_, second := resetSessionBinding(t, f, other, &now, sessions.Config{})
		proof, _ := startResetProof(t, first, sessions.Record{}, f.user.PrincipalID)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		reached := make(chan struct{}, 2)
		release := make(chan struct{})
		f.hasher.hook = func(ctx context.Context) error {
			reached <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		type outcome struct {
			result auth.PasswordResetResult
			err    error
		}
		done := make(chan outcome, 2)
		for _, provider := range []*systemstate.PasswordResetPersistence{first, second} {
			go func(p *systemstate.PasswordResetPersistence) {
				result, err := p.ResetPassword(ctx, proof.ID(), f.user.PrincipalID, selfPassword)
				done <- outcome{result, err}
			}(provider)
		}
		for range 2 {
			select {
			case <-reached:
			case <-ctx.Done():
				close(release)
				t.Fatal("reset preparation retained a database scope", ctx.Err())
			}
		}
		close(release)
		wins := 0
		for range 2 {
			value := <-done
			if value.err == nil {
				wins++
				if !value.result.Record.ID().Valid() {
					t.Fatal("winning proof did not publish its session")
				}
			} else if value.err != auth.ErrInvalidResetProof || value.result.Record.ID().Valid() {
				t.Fatal("losing proof returned wrong outcome", value.err)
			}
		}
		f.hasher.hook = nil
		if wins != 1 || f.hasher.calls.Load() != 2 {
			t.Fatal("concurrent proof consumed more than once", wins)
		}
		assertResetCommit(t, f, f.user, selfPassword, 2)
	})
	t.Run("expiry_is_rechecked_after_waiting_for_fence", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 1)
		now := loginInstant
		boundary := &resetAdvanceBoundary{TransitionBackend: backend}
		_, p := resetSessionBinding(t, f, boundary, &now, sessions.Config{})
		proof, _ := startResetProof(t, p, sessions.Record{}, f.user.PrincipalID)
		rows, audit := snapshotIdentitySystemRows(t, backend)
		before := f.stored(t)
		f.hasher.hook = func(context.Context) error { boundary.advance = func() { now = now.Add(31 * time.Minute) }; return nil }
		result, err := p.ResetPassword(t.Context(), proof.ID(), f.user.PrincipalID, selfPassword)
		f.hasher.hook = nil
		if err != auth.ErrInvalidResetProof || result.Record.ID().Valid() {
			t.Fatal("stale rotation timestamp admitted an expired proof", err)
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("expired proof was cleaned up or reset outside admission")
		}
	})
	for _, mode := range []string{"logout", "replacement", "password", "email", "profile"} {
		t.Run("changed_during_hash/"+mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 4)
			now := loginInstant
			_, p := resetSessionBinding(t, f, backend, &now, sessions.Config{})
			_, second := resetSessionBinding(t, f, other, &now, sessions.Config{})
			proof, _ := startResetProof(t, p, sessions.Record{}, f.user.PrincipalID)
			var replacement sessions.Record
			f.hasher.hook = func(ctx context.Context) error {
				if mode == "logout" {
					return second.Sessions().Delete(ctx, proof.ID())
				}
				if mode == "replacement" {
					token, err := second.Resetter().IssueToken(ctx, f.root.PrincipalID)
					if err != nil {
						return err
					}
					replacement, err = second.StartPasswordReset(ctx, proof.ID(), f.root.PrincipalID, token.Encoded())
					return err
				}
				patch := models.UserPatch{}.WithFirstName("current reset profile").WithRevision(7)
				if mode == "email" {
					patch = patch.WithEmail("changed@example.test")
				}
				if mode == "password" {
					encoded, err := f.hasher.PasswordHasher.Hash(ctx, managementNewPassword)
					if err != nil {
						return err
					}
					patch = patch.WithEncodedPassword(encoded)
				}
				_, err := models.UserObjects.Patch(ctx, other, f.user, patch)
				return err
			}
			result, err := p.ResetPassword(t.Context(), proof.ID(), f.user.PrincipalID, selfPassword)
			f.hasher.hook = nil
			if mode == "profile" {
				if err != nil || f.stored(t).Revision != 8 || f.stored(t).FirstName != "current reset profile" {
					t.Fatal("reset lost current profile", err)
				}
			} else if err != auth.ErrInvalidResetProof || result.Record.ID().Valid() {
				t.Fatal("stale reset proof admitted", mode, err)
			}
			if mode == "replacement" {
				if err := second.CheckPasswordReset(t.Context(), replacement.ID(), f.root.PrincipalID, nil); err != nil {
					t.Fatal("failed old request damaged replacement proof", err)
				}
			}
			if f.hasher.calls.Load() != 1 {
				t.Fatal("raced reset hashed more than once")
			}
		})
	}
	for _, mode := range []string{"confirmed", "exhausted", "unknown", "entropy"} {
		t.Run("collision/"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 4)
			now := loginInstant
			boundary := &passwordChangeBoundary{TransitionBackend: backend}
			if mode == "unknown" {
				boundary.mode = "collision_unknown"
			}
			runtime, p := resetSessionBinding(t, f, boundary, &now, sessions.Config{})
			proof, _ := startResetProof(t, p, sessions.Record{}, f.user.PrincipalID)
			oldBytes, _ := base64.RawURLEncoding.DecodeString(proof.ID().Encoded())
			foreignBytes, _ := base64.RawURLEncoding.DecodeString(f.ids[1].Encoded())
			freshBytes := bytes.Repeat([]byte{164}, 32)
			entropy := append(append(append([]byte(nil), oldBytes...), foreignBytes...), freshBytes...)
			if mode == "exhausted" {
				entropy = bytes.Repeat(foreignBytes, 4)
			}
			if mode == "unknown" {
				entropy = append(append([]byte(nil), foreignBytes...), freshBytes...)
			}
			if mode == "entropy" {
				entropy = nil
			}
			reader := bytes.NewReader(entropy)
			manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return now }, Random: reader})
			if err != nil {
				t.Fatal(err)
			}
			consumer, err := runtime.PasswordResetPersistence(manager, resetConfig(t, &now))
			if err != nil {
				t.Fatal(err)
			}
			rows, audit := snapshotIdentitySystemRows(t, backend)
			before := f.stored(t)
			f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
			result, err := consumer.ResetPassword(t.Context(), proof.ID(), f.user.PrincipalID, selfPassword)
			boundary.armed = false
			f.hasher.hook = nil
			wantCalls := map[string]int{"confirmed": 2, "exhausted": 4, "unknown": 1, "entropy": 0}[mode]
			if boundary.calls != wantCalls || f.hasher.calls.Load() != 1 {
				t.Fatal("proof collision retried hash or uncertain transaction", mode, boundary.calls)
			}
			if mode == "confirmed" {
				if err != nil || result.Record.ID().Encoded() != base64.RawURLEncoding.EncodeToString(freshBytes) || reader.Len() != 0 {
					t.Fatal("confirmed proof collision did not use fresh ID", err)
				}
				assertResetCommit(t, f, before, selfPassword, 2)
			} else {
				if err == nil || result.Record.ID().Valid() {
					t.Fatal("proof collision failure published result", err)
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
					t.Fatal("proof collision failure lost rollback")
				}
				if mode == "unknown" && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
					t.Fatal("proof collision unknown classification lost", err)
				}
			}
		})
	}
}

type resetEntropyHook struct{ before func() error }

func (r *resetEntropyHook) Read(target []byte) (int, error) {
	if r.before != nil {
		fn := r.before
		r.before = nil
		if err := fn(); err != nil {
			return 0, err
		}
	}
	return rand.Read(target)
}

func RunPasswordResetSessionEntry(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, kind := range []string{"missing", "anonymous"} {
		for _, mode := range []string{"zero", "nil", "twice", "swallow", "opaque", "session_insert", "after_session", "cancel", "unknown_rollback", "unknown_commit", "late_cancel", "email_changed"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				backend, other := open(t)
				f := newManagementFixture(t, backend, 1)
				now := loginInstant
				boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: mode}
				entropy := &resetEntropyHook{}
				runtime, p := resetSessionBinding(t, f, boundary, &now, sessions.Config{Random: entropy})
				previous := resetPrevious(t, runtime, p.Sessions(), kind, f.user.PrincipalID)
				token := resetToken(t, p.Resetter(), f.user.PrincipalID)
				rows, audit := snapshotIdentitySystemRows(t, backend)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boundary.afterCommit = cancel
				entropy.before = func() error {
					boundary.armed = true
					if mode == "email_changed" {
						_, err := models.UserObjects.Patch(ctx, other, f.user, models.UserPatch{}.WithEmail("changed@example.test"))
						return err
					}
					return nil
				}
				record, err := p.StartPasswordReset(ctx, previous.ID(), f.user.PrincipalID, token.Encoded())
				boundary.armed = false
				if boundary.calls != 1 || f.hasher.calls.Load() != 0 {
					t.Fatal("reset proof entry retried or hashed", boundary.calls)
				}
				committed := mode == "late_cancel" || mode == "unknown_commit"
				if mode == "late_cancel" {
					if err != nil || !record.ID().Valid() || ctx.Err() != context.Canceled {
						t.Fatal("entry discarded confirmed acceptance", err)
					}
				} else if err == nil || record.ID().Valid() {
					t.Fatal("unconfirmed entry published a session", mode, err)
				}
				if mode == "email_changed" && err != auth.ErrInvalidResetProof {
					t.Fatal("entry admitted token for changed account", err)
				}
				if mode == "unknown_commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || mode == "unknown_rollback" && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("entry outcome classification lost", mode, err)
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(audit, afterAudit) {
					t.Fatal("proof entry wrote audit")
				}
				if !committed && !reflect.DeepEqual(rows, afterRows) {
					t.Fatal("failed proof entry mutated sessions", mode)
				}
				if committed && reflect.DeepEqual(rows, afterRows) {
					t.Fatal("confirmed entry did not reach storage")
				}
				switch mode {
				case "swallow", "opaque", "session_insert", "after_session":
					if boundary.faults != 1 {
						t.Fatal("required entry fault did not execute", mode, boundary.faults)
					}
				}
				requirePasswordPrivate(t, err, "private-password-fault", token.Encoded())
			})
		}
	}
}

func RunPasswordResetSessionHTTP(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"anonymous", "self", "other", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 4)
			now := loginInstant
			boundary := &passwordChangeBoundary{TransitionBackend: backend}
			runtime, p := resetSessionBinding(t, f, boundary, &now, sessions.Config{})
			kind := mode
			if kind == "unknown" {
				kind = "anonymous"
			}
			previous := resetPrevious(t, runtime, p.Sessions(), kind, f.user.PrincipalID)
			token := resetToken(t, p.Resetter(), f.user.PrincipalID)
			authRuntime, err := sessionauth.New(sessionauth.Config{Sessions: p.Sessions(), Authenticator: runtime.Authenticator(), Authorizer: auth.PrincipalAuthorizer{}, PasswordResetPersistence: p, SessionCookie: sessionauth.CookieConfig{AllowInsecure: true}, CSRFCookie: sessionauth.CookieConfig{AllowInsecure: true}, Clock: func() time.Time { return now }, LoginPath: "/csrf/", FallbackPath: "/", AllowedNextPaths: []string{"/"}})
			if err != nil {
				t.Fatal(err)
			}
			configured, err := settings.New(settings.Definition{ProjectName: "reset_http_probe", InstalledApps: []apps.Config{{Name: "github.com/progresshans/godj/internal/identitytest", Label: "resetprobe"}}})
			if err != nil {
				t.Fatal(err)
			}
			// These small probe handlers exercise the real Web runtime and DB.
			// Product Form/JSON parsing and token URL routes are a separate owner.
			finishDone := make(chan struct{}, 1)
			routes := []web.Route{
				{Name: "resetprobe:csrf", Method: "GET", Path: "/csrf/", Handler: func(request *web.Request) (web.Response, error) {
					csrf, err := authRuntime.CSRFToken(request)
					if err != nil {
						return web.Response{}, err
					}
					response, err := web.NewResponse(200, nil, []byte(csrf.Value()))
					if err != nil {
						return web.Response{}, err
					}
					return csrf.Apply(response)
				}},
				{Name: "resetprobe:entry", Method: "GET", Path: "/entry/", Handler: func(request *web.Request) (web.Response, error) {
					result, err := authRuntime.StartPasswordReset(request, f.user.PrincipalID, request.HTTP().Header.Get("X-Test-Reset-Token"))
					if err != nil {
						return web.Response{}, err
					}
					response, err := web.NewResponse(302, http.Header{"Location": []string{"/confirm/"}, "Cache-Control": []string{"no-store"}, "Referrer-Policy": []string{"no-referrer"}}, nil)
					if err != nil {
						return web.Response{}, err
					}
					return result.Apply(response)
				}},
				{Name: "resetprobe:check", Method: "GET", Path: "/confirm/", Handler: func(request *web.Request) (web.Response, error) {
					if err := authRuntime.CheckPasswordReset(request, f.user.PrincipalID, nil); err != nil {
						return web.NewResponse(403, nil, nil)
					}
					return web.NewResponse(200, nil, []byte("confirmation"))
				}},
				{Name: "resetprobe:finish", Method: "POST", Path: "/confirm/", Handler: func(request *web.Request) (web.Response, error) {
					if err := authRuntime.VerifyCSRF(request, nil); err != nil {
						return web.NewResponse(403, nil, nil)
					}
					defer func() { finishDone <- struct{}{} }()
					password, err := base64.RawURLEncoding.DecodeString(request.HTTP().Header.Get("X-Test-Password-Base64"))
					if err != nil {
						return web.NewResponse(400, nil, nil)
					}
					result, err := authRuntime.ResetPassword(request, f.user.PrincipalID, string(password))
					if err != nil {
						return web.Response{}, err
					}
					response, err := web.NewResponse(302, http.Header{"Location": []string{"/done/"}}, nil)
					if err != nil {
						return web.Response{}, err
					}
					return result.Apply(response)
				}},
				{Name: "resetprobe:who", Method: "GET", Path: "/who/", Handler: func(request *web.Request) (web.Response, error) {
					principal, err := authRuntime.InspectPrincipal(request)
					if err != nil {
						return web.Response{}, err
					}
					if principal.Authenticated() {
						return web.NewResponse(200, nil, []byte("authenticated"))
					}
					return web.NewResponse(200, nil, []byte("anonymous"))
				}},
			}
			application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(application)
			defer server.Close()
			h := &identityHTTP{server: server}
			client := h.newClient(t)
			serverURL, _ := url.Parse(server.URL)
			sessionName, csrfName := authRuntime.CookieNames()
			client.Jar.SetCookies(serverURL, []*http.Cookie{{Name: sessionName, Value: previous.ID().Encoded(), Path: "/"}})
			_, csrfToken := h.request(t, client, "GET", "/csrf/", nil)
			response, body := h.request(t, client, "GET", "/entry/", http.Header{"X-Test-Reset-Token": []string{token.Encoded()}})
			if response.StatusCode != 302 || response.Header.Get("Location") != "/confirm/" || strings.Contains(body+response.Header.Get("Location"), token.Encoded()) {
				t.Fatal("entry failed to publish only hidden destination")
			}
			cookies := response.Cookies()
			if len(cookies) != 1 || cookies[0].Name != sessionName || cookies[0].Value == previous.ID().Encoded() {
				t.Fatal("entry did not rotate opaque cookie")
			}
			proofID, err := sessions.ParseID(cookies[0].Value)
			if err != nil {
				t.Fatal(err)
			}
			response, _ = h.request(t, h.newClient(t), "GET", "/confirm/", nil)
			if response.StatusCode != 403 {
				t.Fatal("another browser borrowed proof")
			}
			rows, audit := snapshotIdentitySystemRows(t, backend)
			response, _ = h.request(t, client, "GET", "/confirm/", nil)
			if response.StatusCode != 200 {
				t.Fatal("current browser cannot read proof")
			}
			response, _ = h.request(t, client, "POST", "/confirm/", http.Header{"X-Test-Password-Base64": []string{base64.RawURLEncoding.EncodeToString([]byte(selfPassword))}})
			if response.StatusCode != 403 {
				t.Fatal("reset accepted missing CSRF")
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || f.hasher.calls.Load() != 0 {
				t.Fatal("read/CSRF refusal mutated or hashed")
			}
			before := f.stored(t)
			if mode == "unknown" {
				boundary.mode = "unknown_commit"
				f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
			}
			response, _ = h.request(t, client, "POST", "/confirm/", http.Header{"X-Test-Password-Base64": []string{base64.RawURLEncoding.EncodeToString([]byte(selfPassword))}, authRuntime.CSRFHeader(): []string{csrfToken}})
			<-finishDone
			boundary.armed = false
			f.hasher.hook = nil
			if mode == "unknown" {
				if response.StatusCode != 500 || len(response.Cookies()) != 0 {
					t.Fatal("uncertain HTTP reset published cookie/success")
				}
			} else if response.StatusCode != 302 || len(response.Cookies()) != 1 || response.Cookies()[0].Name != sessionName {
				t.Fatal("confirmed HTTP reset did not publish session outcome", response.StatusCode)
			}
			assertResetCommit(t, f, before, selfPassword, 2)
			if _, found, err := runtime.SessionStore().Load(t.Context(), proofID); err != nil || found {
				t.Fatal("HTTP reset retained consumed proof", err)
			}
			_, who := h.request(t, client, "GET", "/who/", nil)
			want := "anonymous"
			if mode == "other" {
				want = "authenticated"
			}
			if who != want {
				t.Fatal("reset changed authentication unexpectedly", mode, who)
			}
			csrfRetained := false
			for _, cookie := range client.Jar.Cookies(serverURL) {
				if cookie.Name == csrfName {
					csrfRetained = true
				}
			}
			if !csrfRetained {
				t.Fatal("reset removed independent CSRF cookie")
			}
		})
	}
}
