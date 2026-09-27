package identitytest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
)

// RunPasswordResetComposition checks the borrowed transaction contract with
// actual independent connections. It exercises state after the reset itself,
// so a later session/proof write cannot accidentally leave a changed password.
func RunPasswordResetComposition(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("later_failure_rolls_back_password_sessions_and_audit", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 7)
		now := loginInstant
		resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		token := resetToken(t, resetter, f.user.PrincipalID)
		rows, audit := snapshotIdentitySystemRows(t, other)
		before := f.stored(t)
		prepared, err := resetter.Prepare(t.Context(), token.PrincipalID(), token.Encoded(), selfPassword)
		if err != nil || f.hasher.calls.Load() != 1 {
			t.Fatal("reset preparation failed", err)
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, other)
		if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("preparation changed durable state")
		}
		requirePasswordPrivate(t, prepared, token.Encoded(), before.EncodedPassword, selfPassword)
		laterFailure := errors.New("later proof storage failure")
		var observedChanged bool
		err = f.runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			if err := resetter.ApplyIn(t.Context(), session, prepared); err != nil {
				return err
			}
			row, found, err := models.UserObjects.Using(session).Filter(models.UserFields.ID.Exact(before.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
			if err != nil || !found {
				t.Fatal("borrowed mutation cannot be observed inside its transaction", err)
			}
			observedChanged = row.EncodedPassword != before.EncodedPassword && row.Revision == before.Revision+1
			return laterFailure
		})
		if err != laterFailure || !observedChanged {
			t.Fatal("reset did not remain provisional in the caller's transaction", err)
		}
		afterRows, afterAudit = snapshotIdentitySystemRows(t, other)
		if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("later storage failure left a partial reset")
		}
		if err := resetter.CheckPassword(t.Context(), token.PrincipalID(), token.Encoded(), nil); err != nil {
			t.Fatal("rolled-back reset consumed its token", err)
		}
		// A confirmed rollback may reuse the already prepared hash explicitly.
		// The product never retries an unknown result.
		if err := f.runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			return resetter.ApplyIn(t.Context(), session, prepared)
		}); err != nil {
			t.Fatal("borrowed reset failed to commit", err)
		}
		assertResetCommit(t, f, before, selfPassword, before.Revision+1)
		if f.hasher.calls.Load() != 1 {
			t.Fatal("ApplyIn repeated password work")
		}
		if err := f.runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			return resetter.ApplyIn(t.Context(), session, prepared)
		}); err != identity.ErrInvalidResetToken {
			t.Fatal("prepared material bypassed consumed token admission", err)
		}
	})
	t.Run("owner_and_invalid_calls_have_no_effects", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 1)
		now := loginInstant
		resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		other := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		token := resetToken(t, resetter, f.user.PrincipalID)
		prepared, err := resetter.Prepare(t.Context(), token.PrincipalID(), token.Encoded(), selfPassword)
		if err != nil {
			t.Fatal(err)
		}
		rows, audit := snapshotIdentitySystemRows(t, backend)
		before := f.stored(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var absent *identity.PasswordResetter
		if err := f.runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			for name, check := range map[string]func() error{
				"owner":   func() error { return other.ApplyIn(t.Context(), session, prepared) },
				"zero":    func() error { return resetter.ApplyIn(t.Context(), session, identity.PreparedPasswordReset{}) },
				"context": func() error { return resetter.ApplyIn(nil, session, prepared) },
				"session": func() error { return resetter.ApplyIn(t.Context(), nil, prepared) },
				"service": func() error { return absent.ApplyIn(t.Context(), session, prepared) },
			} {
				if err := check(); !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
					t.Fatal("invalid borrowed reset was not rejected", name, err)
				}
			}
			if err := resetter.ApplyIn(ctx, session, prepared); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled borrowed reset was not rejected", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("rejected borrowed calls wrote state")
		}
	})
	for _, change := range []string{"profile", "email", "password", "inactive", "expired"} {
		t.Run("current_state/"+change, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 1)
			now := loginInstant
			resetter := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
			token := resetToken(t, resetter, f.user.PrincipalID)
			prepared, err := resetter.Prepare(t.Context(), token.PrincipalID(), token.Encoded(), selfPassword)
			if err != nil {
				t.Fatal(err)
			}
			patch := models.UserPatch{}.WithFirstName("new profile").WithRevision(8)
			switch change {
			case "email":
				patch = patch.WithEmail("changed@example.test")
			case "password":
				encoded, err := f.hasher.PasswordHasher.Hash(t.Context(), managementNewPassword)
				if err != nil {
					t.Fatal(err)
				}
				patch = patch.WithEncodedPassword(encoded)
			case "inactive":
				patch = patch.WithActive(false)
			case "expired":
				now = now.Add(time.Hour + time.Second)
			}
			if _, err := models.UserObjects.Update(t.Context(), other, f.user, patch); err != nil {
				t.Fatal(err)
			}
			before := f.stored(t)
			rows, audit := snapshotIdentitySystemRows(t, backend)
			err = f.runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
				return resetter.ApplyIn(t.Context(), session, prepared)
			})
			if change == "profile" {
				if err != nil {
					t.Fatal("unrelated current profile rejected", err)
				}
				assertResetCommit(t, f, before, selfPassword, 9)
			} else {
				if err != identity.ErrInvalidResetToken {
					t.Fatal("prepared reset ignored changed current state", change, err)
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
					t.Fatal("invalidated prepared reset changed durable state")
				}
			}
			if f.hasher.calls.Load() != 1 {
				t.Fatal("borrowed reset hashed again")
			}
		})
	}
	t.Run("token_check_borrows_scope_and_current_state", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 1)
		now := loginInstant
		original := resetService(t, f.runtime, f.hasher, resetConfig(t, &now))
		token := resetToken(t, original, f.user.PrincipalID)
		owner := &resetNoSnapshot{ManagementBackend: f.runtime}
		resetter := resetService(t, owner, f.hasher, resetConfig(t, &now))
		rows, audit := snapshotIdentitySystemRows(t, backend)
		before := f.stored(t)
		if err := f.runtime.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
			if err := resetter.CheckTokenIn(t.Context(), reader, token.PrincipalID(), token.Encoded()); err != nil {
				t.Fatal("valid token check failed in borrowed scope", err)
			}
			for _, value := range []struct{ id, token string }{{token.PrincipalID(), "invalid"}, {"missing", token.Encoded()}, {"", token.Encoded()}} {
				if err := resetter.CheckTokenIn(t.Context(), reader, value.id, value.token); err != identity.ErrInvalidResetToken {
					t.Fatal("invalid borrowed token accepted", err)
				}
			}
			if err := resetter.CheckTokenIn(t.Context(), nil, token.PrincipalID(), token.Encoded()); !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
				t.Fatal("nil borrowed reader accepted", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := resetter.CheckTokenIn(ctx, reader, token.PrincipalID(), token.Encoded()); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled token check accepted", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
		if owner.reads != 0 || f.hasher.calls.Load() != 0 || !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(audit, afterAudit) || !reflect.DeepEqual(before, f.stored(t)) {
			t.Fatal("borrowed check nested a read, hashed or changed state")
		}
		if _, err := models.UserObjects.Update(t.Context(), other, f.user, models.UserPatch{}.WithEmail("later@example.test")); err != nil {
			t.Fatal(err)
		}
		if err := f.runtime.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			return resetter.CheckTokenIn(t.Context(), session, token.PrincipalID(), token.Encoded())
		}); err != identity.ErrInvalidResetToken || owner.reads != 0 {
			t.Fatal("borrowed check did not use current transaction state", err)
		}
	})
}

type resetNoSnapshot struct {
	identity.ManagementBackend
	reads int
}

func (backend *resetNoSnapshot) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	backend.reads++
	return errors.New("borrowed token check must not open its own snapshot")
}
