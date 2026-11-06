package identitytest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

type passwordChangeBoundary struct {
	TransitionBackend
	mode                   string
	armed                  bool
	calls, faults, deletes int
	afterCommit            func()
}

func (b *passwordChangeBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	if !b.armed {
		return b.TransitionBackend.CoordinatedAtomic(ctx, callback)
	}
	b.calls++
	if b.mode == "zero" {
		return nil
	}
	err := b.TransitionBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if b.mode == "nil" {
			return callback(nil)
		}
		err := callback(&passwordChangeFaultSession{Session: session, owner: b})
		if b.mode == "swallow" {
			return nil
		}
		if err != nil {
			return err
		}
		if b.mode == "twice" {
			return callback(session)
		}
		if b.mode == "cancel" || b.mode == "unknown_rollback" {
			return context.Canceled
		}
		return nil
	})
	if b.mode == "unknown_rollback" {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown, Detail: "private-password-fault"})
	}
	if b.mode == "collision_unknown" && errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
	}
	if b.mode == "unknown_commit" && err == nil {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-password-fault"}
	}
	if b.mode == "validation_cleanup" {
		return errors.Join(err, errors.New("private-password-fault"))
	}
	if b.mode == "late_cancel" && err == nil {
		b.afterCommit()
	}
	return err
}

type passwordChangeFaultSession struct {
	db.Session
	owner *passwordChangeBoundary
}

func (s *passwordChangeFaultSession) fault() error {
	s.owner.faults++
	if s.owner.mode == "opaque" {
		return loginOpaqueError{"private-password-fault"}
	}
	return errors.New("private-password-fault")
}
func (s *passwordChangeFaultSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if s.owner.mode == "swallow" || s.owner.mode == "opaque" {
		return nil, s.fault()
	}
	return s.Session.Query(ctx, plan)
}
func (s *passwordChangeFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "godj_system_session" && s.owner.mode == "session_insert" || plan.Table() == "godj_system_audit" && s.owner.mode == "audit_insert" {
		return 0, s.fault()
	}
	n, err := s.Session.Insert(ctx, plan)
	if err == nil && (plan.Table() == "godj_system_session" && s.owner.mode == "after_session" || plan.Table() == "godj_system_audit" && s.owner.mode == "after_audit") {
		return 0, s.fault()
	}
	return n, err
}
func (s *passwordChangeFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if plan.Table() == "godj_identity_user" && s.owner.mode == "password_write" {
		return 0, s.fault()
	}
	n, err := s.Session.Update(ctx, plan)
	if err == nil && plan.Table() == "godj_identity_user" && s.owner.mode == "after_password" {
		return 0, s.fault()
	}
	return n, err
}
func (s *passwordChangeFaultSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	n, err := s.Session.Delete(ctx, plan)
	if plan.Table() == "godj_system_session" {
		s.owner.deletes++
		if err == nil && (s.owner.mode == "after_rotation_delete" && s.owner.deletes == 1 || s.owner.mode == "revoke" && s.owner.deletes == 2) {
			return 0, s.fault()
		}
	}
	return n, err
}

func RunPasswordChangeBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"confirmed", "exhausted", "unknown", "entropy"} {
		t.Run("collision/"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 4)
			boundary := &passwordChangeBoundary{TransitionBackend: backend}
			if mode == "unknown" {
				boundary.mode = "collision_unknown"
			}
			runtime, _, _, previous := passwordChangeBinding(t, f, boundary)
			oldBytes, _ := base64.RawURLEncoding.DecodeString(previous.ID().Encoded())
			foreignBytes, _ := base64.RawURLEncoding.DecodeString(f.ids[1].Encoded())
			freshBytes := bytes.Repeat([]byte{165}, 32)
			freshID, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(freshBytes))
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := runtime.SessionStore().Load(t.Context(), freshID); err != nil || found {
				t.Fatal("fixture fresh ID is not fresh", err)
			}
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
			manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return loginInstant.Add(time.Second) }, Random: reader})
			if err != nil {
				t.Fatal(err)
			}
			provider, err := runtime.PasswordChangePersistence(manager)
			if err != nil {
				t.Fatal(err)
			}
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
			result, err := provider.ChangePassword(t.Context(), previous.ID(), managementOldPassword, selfPassword)
			boundary.armed = false
			f.hasher.hook = nil
			wantCalls := map[string]int{"confirmed": 2, "exhausted": 4, "unknown": 1, "entropy": 0}[mode]
			if boundary.calls != wantCalls || f.hasher.calls.Load() != 1 {
				t.Fatal("collision retried hash or wrong transaction count", mode, boundary.calls)
			}
			if mode == "confirmed" {
				if err != nil || result.Record.ID() != freshID || reader.Len() != 0 {
					t.Fatal("confirmed collision did not use fresh entropy", err)
				}
				assertSelfPasswordCommit(t, f, runtime, before, previous, result, selfPassword, 2)
			} else {
				if err == nil || result.Record.ID().Valid() {
					t.Fatal("unconfirmed collision/entropy published a result", mode)
				}
				if mode == "unknown" && (!errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || reader.Len() != 32) {
					t.Fatal("uncertain collision was retried or reclassified", err)
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
					t.Fatal("failed collision/entropy changed durable state", mode)
				}
			}
		})
	}
	for _, mode := range []string{"zero", "nil", "twice", "swallow", "opaque", "session_insert", "after_session", "password_write", "after_password", "audit_insert", "after_audit", "after_rotation_delete", "revoke", "cancel", "unknown_rollback", "unknown_commit", "late_cancel", "validation", "validation_cleanup"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 4)
			boundary := &passwordChangeBoundary{TransitionBackend: backend, mode: mode}
			validations := 0
			validator := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
				validations++
				if validations == 2 && (mode == "validation" || mode == "validation_cleanup") {
					return validation.NewErrors(validation.New("password", "password_too_similar"))
				}
				return validation.Errors{}
			})
			runtime, _, provider, previous := passwordChangeBinding(t, f, boundary, validator)
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			boundary.afterCommit = cancel
			f.hasher.hook = func(context.Context) error { boundary.armed = true; return nil }
			result, err := provider.ChangePassword(ctx, previous.ID(), managementOldPassword, selfPassword)
			boundary.armed = false
			f.hasher.hook = nil
			if boundary.calls != 1 || f.hasher.calls.Load() != 1 {
				t.Fatal("failed password change was retried", boundary.calls, f.hasher.calls.Load())
			}
			committed := mode == "unknown_commit" || mode == "late_cancel"
			if mode == "late_cancel" {
				if err != nil || !result.Record.ID().Valid() || ctx.Err() != context.Canceled {
					t.Fatal("late cancellation hid a confirmed commit", err)
				}
				assertSelfPasswordCommit(t, f, runtime, before, previous, result, selfPassword, 2)
			} else if err == nil || result.Record.ID().Valid() || result.Credential.Principal().ID() != "" {
				t.Fatal("unconfirmed password change published a result", mode, err)
			}
			if _, rejected := validation.Rejected(err); rejected != (mode == "validation") {
				t.Fatal("input/execution classification changed", mode, err)
			}
			if mode == "unknown_commit" && !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || mode == "unknown_rollback" && !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("failure cause was lost", mode, err)
			}
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if !committed && (!reflect.DeepEqual(before, f.stored(t)) || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit)) {
				t.Fatal("password/session/audit rollback was incomplete", mode)
			}
			if committed && (f.stored(t).Revision != 2 || len(afterAudit) != 1 || len(afterRows) != 3) {
				t.Fatal("commit state not atomic", mode)
			}
			switch mode {
			case "swallow", "opaque", "session_insert", "after_session", "password_write", "after_password", "audit_insert", "after_audit", "after_rotation_delete", "revoke":
				if boundary.faults != 1 {
					t.Fatal("requested failure did not execute once", mode, boundary.faults)
				}
			}
			requirePasswordPrivate(t, err, "private-password-fault", selfPassword, before.EncodedPassword, previous.ID().Encoded())
			requirePasswordPrivate(t, result, selfPassword, before.EncodedPassword, previous.ID().Encoded())
		})
	}
}

func RunPasswordChangeConcurrency(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"profile", "policy", "password", "inactive", "deleted", "logout", "rotation", "absolute_expiry", "idle_expiry"} {
		t.Run("current_fence/"+mode, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 4)
			validations := 0
			validator := identity.PasswordValidatorFunc(func(_ string, profile identity.Profile) validation.Errors {
				validations++
				if profile.FirstName == "refuse-current" {
					return validation.NewErrors(validation.New("password", "password_too_similar"))
				}
				return validation.Errors{}
			})
			runtime, manager, provider, previous := passwordChangeBinding(t, f, backend, validator)
			otherRuntime, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			before := f.stored(t)
			var edited models.User
			f.hasher.hook = func(ctx context.Context) error {
				// A real second connection must acquire the same coordination
				// domain here. A retained preflight read/write scope would block.
				switch mode {
				case "logout":
					return otherRuntime.SessionStore().Delete(ctx, previous.ID())
				case "rotation":
					otherManager, e := sessions.NewManager(otherRuntime.SessionStore(), sessions.Config{Clock: func() time.Time { return loginInstant.Add(time.Second) }})
					if e != nil {
						return e
					}
					_, e = otherManager.Rotate(ctx, previous)
					return e
				case "absolute_expiry", "idle_expiry":
					return nil
				case "deleted":
					policy := managementHost(t, other)
					_, e := f.manager(t, otherRuntime).DeleteUser(ctx, f.actor, f.user.ID, 1, policy.AccountsUser)
					return e
				}
				return otherRuntime.CoordinatedAtomic(ctx, func(session db.Session) error {
					patch := models.UserPatch{}.WithRevision(2)
					switch mode {
					case "profile":
						patch = patch.WithUsername("renamed").WithFirstName("current name").WithLastLogin(loginInstant)
					case "policy":
						patch = patch.WithFirstName("refuse-current")
					case "password":
						encoded, e := f.hasher.PasswordHasher.Hash(ctx, "concurrent replacement")
						if e != nil {
							return e
						}
						patch = patch.WithEncodedPassword(encoded)
					case "inactive":
						patch = patch.WithActive(false)
					}
					var e error
					edited, e = models.UserObjects.Patch(ctx, session, f.user, patch)
					return e
				})
			}
			if mode == "absolute_expiry" || mode == "idle_expiry" {
				now := loginInstant.Add(time.Second)
				manager, err = sessions.NewManager(runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return now }, AbsoluteLifetime: time.Hour, IdleTimeout: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				provider, err = runtime.PasswordChangePersistence(manager, validator)
				if err != nil {
					t.Fatal(err)
				}
				previous, err = manager.Create(t.Context(), previous.Values())
				if err != nil {
					t.Fatal(err)
				}
				f.hasher.hook = func(context.Context) error {
					if mode == "absolute_expiry" {
						now = now.Add(time.Hour)
					} else {
						now = now.Add(time.Minute)
					}
					return nil
				}
			}
			rows, audit := snapshotIdentitySystemRows(t, backend)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			result, err := provider.ChangePassword(ctx, previous.ID(), managementOldPassword, selfPassword)
			f.hasher.hook = nil
			if f.hasher.calls.Load() != 1 {
				t.Fatal("password hash repeated")
			}
			if mode == "profile" {
				if err != nil || f.stored(t).Username != "renamed" || f.stored(t).FirstName != "current name" || validations != 2 {
					t.Fatal("unrelated current profile edit was lost or blocked", err)
				}
				assertSelfPasswordCommit(t, f, runtime, edited, previous, result, selfPassword, 3)
				if _, e := runtime.Authenticator().Authenticate(t.Context(), "renamed", selfPassword); e != nil {
					t.Fatal("renamed current credential failed", e)
				}
			} else {
				if err == nil || result.Record.ID().Valid() {
					t.Fatal("current fence allowed stale change", mode)
				}
				if mode == "policy" {
					if _, rejected := validation.Rejected(err); !rejected || validations != 2 {
						t.Fatal("current policy missing", err)
					}
				} else if err != auth.ErrInvalidCredentials {
					t.Fatal("current binding rejection changed", mode, err)
				}
				if mode != "deleted" {
					stored := f.stored(t)
					if mode == "password" {
						if stored.EncodedPassword != edited.EncodedPassword {
							t.Fatal("concurrent password overwritten")
						}
					} else if stored.EncodedPassword != before.EncodedPassword {
						t.Fatal("failed change updated password")
					}
				}
				if mode != "deleted" && mode != "logout" && mode != "rotation" {
					afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
					if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) {
						t.Fatal("rejected change published session/audit effects", mode)
					}
				}
			}
		})
	}
	t.Run("two_connections_one_password_change_wins", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 0)
		runtime, _, first, one := passwordChangeBinding(t, f, backend)
		_, _, second, two := passwordChangeBinding(t, f, other)
		ready, release := make(chan struct{}, 2), make(chan struct{})
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
		type outcome struct {
			result auth.PasswordChangeResult
			err    error
		}
		done := make(chan outcome, 2)
		for index, provider := range []auth.PasswordChangePersistence{first, second} {
			id := []sessions.ID{one.ID(), two.ID()}[index]
			go func() {
				result, err := provider.ChangePassword(ctx, id, managementOldPassword, selfPassword)
				done <- outcome{result, err}
			}()
		}
		for range 2 {
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("password hash retained a database scope", ctx.Err())
			}
		}
		close(release)
		wins := 0
		for range 2 {
			result := <-done
			if result.err == nil {
				wins++
				if !result.result.Record.ID().Valid() {
					t.Fatal("winner missing session")
				}
			} else if result.err != auth.ErrInvalidCredentials || result.result.Record.ID().Valid() {
				t.Fatal("loser did not reject stale binding", result.err)
			}
		}
		f.hasher.hook = nil
		rows, audit := snapshotIdentitySystemRows(t, backend)
		if wins != 1 || f.stored(t).Revision != 2 || len(rows) != 1 || len(audit) != 1 || f.hasher.calls.Load() != 2 {
			t.Fatal("racing changes did not have exactly one atomic winner", wins, len(rows), len(audit))
		}
		if _, err := runtime.Authenticator().Authenticate(t.Context(), "member", selfPassword); err != nil {
			t.Fatal(err)
		}
	})
}
