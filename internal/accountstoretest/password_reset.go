package accountstoretest

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
)

func selectedResetConfig(t *testing.T, now *time.Time) accountmodel.PasswordResetConfig[models.User] {
	t.Helper()
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{66}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return accountmodel.PasswordResetConfig[models.User]{Keys: keys, Clock: func() time.Time { return *now }}
}
func selectedResetOwner(t *testing.T, configured passwordFixture, config accountmodel.PasswordResetConfig[models.User]) *accountmodel.PasswordResetter[models.User] {
	t.Helper()
	owner, err := project.NewAccountPasswordResetter(configured.store, configured.manager, configured.authenticator, config)
	if err != nil || owner.Sessions() != configured.manager {
		t.Fatal("selected reset composition", err)
	}
	return owner
}
func selectedResetToken(t *testing.T, owner *accountmodel.PasswordResetter[models.User], principalID string) auth.PasswordResetToken {
	t.Helper()
	token, err := owner.IssueToken(t.Context(), principalID)
	if err != nil || token.PrincipalID() != principalID || token.Encoded() == "" {
		t.Fatal("selected reset issuance", err)
	}
	return token
}
func selectedResetProof(t *testing.T, owner *accountmodel.PasswordResetter[models.User], previous sessions.Record, token auth.PasswordResetToken) sessions.Record {
	t.Helper()
	proof, err := owner.StartPasswordReset(t.Context(), previous.ID(), token.PrincipalID(), token.Encoded())
	if err != nil || !proof.ID().Valid() || proof.ID() == previous.ID() {
		t.Fatal("selected reset proof entry", err)
	}
	return proof
}
func selectedResetNoResult(t *testing.T, result auth.PasswordResetResult, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(result, auth.PasswordResetResult{}) {
		t.Fatal("failed reset published a cookie outcome", err)
	}
}

func passwordResetNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	configured := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	owner := selectedResetOwner(t, configured, selectedResetConfig(t, &now))
	for _, key := range []int64{0, -17} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			row := roleTarget(t, backend, f, fmt.Sprintf("reset-key-%d", key))
			storedKey(t, backend, "user", row.ID, key)
			(models.UserDescriptor{}).SetPrimaryKey(&row, key)
			old := passwordSession(t, configured.manager, passwordCredential(t, f, row), "signed-key")
			proof := selectedResetProof(t, owner, sessions.Record{}, selectedResetToken(t, owner, row.PrincipalID))
			result, err := owner.ResetPassword(t.Context(), proof.ID(), row.PrincipalID, password+"-new")
			if err != nil || !result.Record.ID().Valid() || result.ClearSession {
				t.Fatal("present nonpositive reset key was lost", err)
			}
			stored := profileRow(t, backend, key)
			entries := profileHistory(t, backend, passwordCredential(t, f, stored).Principal(), key)
			if stored.Revision != 2 || stored.EncodedPassword == row.EncodedPassword || len(entries) != 1 || entries[0].ObjectID() != key {
				t.Fatal("nonpositive reset row/audit mismatch")
			}
			accountSessionRecord(t, configured.store, old, false)
			accountSessionRecord(t, configured.store, proof, false)
			accountSessionRecord(t, configured.store, result.Record, true)
		})
	}
}
