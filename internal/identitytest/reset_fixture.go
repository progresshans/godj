package identitytest

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
)

// Shared setup and assertions for the remaining historical reset HTTP/session
// consumers. Direct reset acceptance is owned by the selected account runtime.
func resetConfig(t *testing.T, now *time.Time, validators ...identity.PasswordValidator) identity.PasswordResetConfig {
	t.Helper()
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{173}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return identity.PasswordResetConfig{Keys: keys, Timeout: time.Hour, Clock: func() time.Time { return *now }, Validators: validators}
}

func resetToken(t *testing.T, resetter *identity.PasswordResetter, id string) auth.PasswordResetToken {
	t.Helper()
	token, err := resetter.IssueToken(t.Context(), id)
	if err != nil || token.PrincipalID() != id || token.Encoded() == "" {
		t.Fatal("reset issue failed", err)
	}
	return token
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
