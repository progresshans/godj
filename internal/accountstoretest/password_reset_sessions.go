package accountstoretest

import (
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/sessions"
)

func passwordResetManySessions(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionSelfPassword(t, backend)
	for len(f.targetSessions) < 257 {
		f.targetSessions = append(f.targetSessions, passwordSession(t, f.runtime.Sessions(), f.credential, "native-batch-target"))
	}
	owner := resetNativeOwner(t, f)
	token := selectedResetToken(t, owner, f.target.PrincipalID)
	previous := f.targetSessions[0]
	before := userReferenceSnapshot(t, reopen(t))
	entropy := f.entropy.count()
	proof := selectedResetProof(t, owner, previous, token)
	afterEntry := userReferenceSnapshot(t, reopen(t))
	expected, expectedProof := resetEntryExpected(t, f, before, afterEntry, previous, f.entropy.one(t, entropy), token.Encoded())
	if !reflect.DeepEqual(proof.Snapshot(), expectedProof.Snapshot()) {
		t.Fatal("multi-batch reset fixture lost its exact proof")
	}
	assertUserReferenceState(t, expected, afterEntry, f.actor.Principal())
	f.targetSessions[0] = expectedProof
	f.owner.hasher.hashes.Store(0)
	f.owner.hasher.verifies.Store(0)
	f.owner.boundary.calls = 0
	entropy = f.entropy.count()
	f.instant.Add(int64(time.Minute))
	result, err := owner.ResetPassword(t.Context(), proof.ID(), f.target.PrincipalID, selfPassword)
	if err != nil || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 1 || f.entropy.count() != entropy {
		t.Fatal("multi-batch reset did not remain one complete transition without new entropy", err)
	}
	after := userReferenceSnapshot(t, reopen(t))
	expected, envelope := resetFinishExpected(t, f, f.owner, afterEntry, after, expectedProof, sessions.ID{})
	if !reflect.DeepEqual(result, envelope) {
		t.Fatal("multi-batch self reset did not clear its current browser")
	}
	assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
}
