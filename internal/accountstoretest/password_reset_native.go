package accountstoretest

import (
	"bytes"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
)

func resetNativeConfig(t *testing.T, f *loginFixture) accountmodel.RuntimeResetConfig {
	t.Helper()
	keys, err := auth.NewPasswordResetKeyRing(bytes.Repeat([]byte{66}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return accountmodel.RuntimeResetConfig{Keys: keys, Clock: f.clock}
}

func resetNativeOwner(t *testing.T, f *loginFixture) *accountmodel.PasswordResetter[models.User] {
	t.Helper()
	owner, err := f.runtime.PasswordResetter(resetNativeConfig(t, f))
	if err != nil || owner.Sessions() != f.runtime.Sessions() {
		t.Fatal("native reset lost its composed session owner", err)
	}
	return owner
}

func resetExpectedRecord(t *testing.T, previous sessions.Record, id sessions.ID, values map[string]string, at time.Time) sessions.Record {
	t.Helper()
	snapshot := previous.Snapshot()
	if !previous.ID().Valid() {
		snapshot.CreatedAt, snapshot.AbsoluteExpiresAt = at, at.Add(loginLifetime)
	}
	snapshot.ID, snapshot.Values, snapshot.AccessedAt = id, values, at
	snapshot.IdleExpiresAt = at.Add(loginIdle)
	if snapshot.IdleExpiresAt.After(snapshot.AbsoluteExpiresAt) {
		snapshot.IdleExpiresAt = snapshot.AbsoluteExpiresAt
	}
	if id == previous.ID() {
		t.Fatal("reset reused the previous bearer")
	}
	record, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func resetEntryExpected(t *testing.T, f *loginFixture, before, after userReferenceState, previous sessions.Record, id sessions.ID, token string) (userReferenceState, sessions.Record) {
	t.Helper()
	values := previous.Values()
	if values == nil {
		values = make(map[string]string)
	}
	values[auth.SessionResetPrincipalIDKey], values[auth.SessionResetTokenKey] = f.target.PrincipalID, token
	record := resetExpectedRecord(t, previous, id, values, f.clock())
	namespace := loginNamespace(t, f.credential)
	if previous.ID().Valid() {
		loginRemoveRecord(t, &before.account, namespace, previous.ID())
	}
	loginPutRecord(t, &before.account, after.account, namespace, record, false)
	return before, record
}

// Hash bytes and the bearer are observed before persistence; the native
// after-state supplies only fresh generated row keys to the independent oracle.
func resetFinishExpected(t *testing.T, f *loginFixture, owner *userReferenceOwner, before, after userReferenceState, proof sessions.Record, id sessions.ID) (userReferenceState, auth.PasswordResetResult) {
	t.Helper()
	encoded, ok := owner.encoded.Load().(string)
	if !ok || encoded == "" || encoded == f.target.EncodedPassword {
		t.Fatal("reset lacks its fresh upstream hash")
	}
	row := f.target
	row.EncodedPassword, row.Revision = encoded, row.Revision+1
	userReferenceReplace(&before.account, row)
	namespace := loginNamespace(t, f.credential)
	for _, record := range f.targetSessions {
		loginRemoveRecord(t, &before.account, namespace, record.ID())
	}
	if principal, _ := proof.Value(auth.SessionPrincipalIDKey); principal == f.target.PrincipalID {
		if id.Valid() {
			t.Fatal("self reset expected an unnecessary bearer")
		}
		return before, auth.PasswordResetResult{ClearSession: true}
	}
	values := proof.Values()
	delete(values, auth.SessionResetPrincipalIDKey)
	delete(values, auth.SessionResetTokenKey)
	record := resetExpectedRecord(t, proof, id, values, f.clock())
	loginRemoveRecord(t, &before.account, namespace, proof.ID())
	loginPutRecord(t, &before.account, after.account, namespace, record, false)
	return before, auth.PasswordResetResult{Record: record}
}
