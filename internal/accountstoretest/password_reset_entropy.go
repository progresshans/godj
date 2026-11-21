package accountstoretest

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

func passwordResetEntropyCase(t *testing.T, open Open, stage, mode string) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	base := resetNativeOwner(t, f)
	token := selectedResetToken(t, base, f.target.PrincipalID)
	var previous sessions.Record
	if stage == "clear" {
		previous = f.targetSessions[0]
	} else if stage != "create" {
		previous = resetBrowserPrevious(t, f, "anonymous")
	}
	finishing := stage == "finish" || stage == "clear"
	if finishing {
		before := userReferenceSnapshot(t, reopen(t))
		entropy := f.entropy.count()
		proof := selectedResetProof(t, base, previous, token)
		after := userReferenceSnapshot(t, reopen(t))
		expected, wanted := resetEntryExpected(t, f, before, after, previous, f.entropy.one(t, entropy), token.Encoded())
		if !reflect.DeepEqual(proof.Snapshot(), wanted.Snapshot()) {
			t.Fatal("entropy fixture lost its exact native proof")
		}
		assertUserReferenceState(t, expected, after, f.actor.Principal())
		previous = wanted
		if stage == "clear" {
			f.targetSessions[0] = wanted
		}
	}
	collision := passwordSession(t, f.runtime.Sessions(), f.actor, "unrelated entropy collision")
	collisionBytes, err := base64.RawURLEncoding.DecodeString(collision.ID().Encoded())
	if err != nil {
		t.Fatal(err)
	}
	unique := sha256.Sum256([]byte("selected-native-reset-entropy-" + stage + mode))
	id, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(unique[:]))
	if err != nil {
		t.Fatal(err)
	}
	if id == collision.ID() || id == previous.ID() {
		t.Fatal("entropy fixture identifiers collide before the control")
	}
	data := append(append([]byte{}, collisionBytes...), unique[:]...)
	if mode == "confirmed_retry" && previous.ID().Valid() {
		old, err := base64.RawURLEncoding.DecodeString(previous.ID().Encoded())
		if err != nil {
			t.Fatal(err)
		}
		data = append(old, data...)
	}
	if mode == "exhausted" {
		data = bytes.Repeat(collisionBytes, 4)
	} else if mode == "entropy_failure" || stage == "clear" {
		data = nil
	}
	random := bytes.NewReader(data)
	fault := &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown}
	boundary := &profileFaultBackend{Backend: backend}
	if mode == "unknown_collision" {
		boundary.mode, boundary.fault = "cleanup_failure", fault
	}
	config := f.config(t)
	config.Sessions.Random = random
	f.owner = newUserReferenceOwner(t, boundary, f.userReferenceFixture, config)
	f.runtime = f.owner.runtime
	owner := resetNativeOwner(t, f)
	f.instant.Add(int64(time.Minute))
	before := userReferenceSnapshot(t, reopen(t))
	var result auth.PasswordResetResult
	if finishing {
		result, err = owner.ResetPassword(t.Context(), previous.ID(), f.target.PrincipalID, selfPassword)
	} else {
		result.Record, err = owner.StartPasswordReset(t.Context(), previous.ID(), f.target.PrincipalID, token.Encoded())
	}
	writes := map[string]int{"confirmed_retry": 2, "exhausted": 4, "unknown_collision": 1, "entropy_failure": 0, "self_clear_no_entropy": 1}[mode]
	hashes, policies := int64(0), int64(0)
	if finishing {
		hashes, policies = 1, int64(1+writes)
	}
	remaining := 0
	if mode == "unknown_collision" {
		remaining = 32
	}
	if f.owner.hasher.hashes.Load() != hashes || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != policies || f.owner.boundary.calls != writes || boundary.calls != writes || random.Len() != remaining {
		t.Fatal("reset entropy retried hash, skipped a current policy or exceeded allocation bounds", mode, err)
	}
	after := userReferenceSnapshot(t, reopen(t))
	if mode == "confirmed_retry" || stage == "clear" {
		if err != nil {
			t.Fatal("confirmed entropy outcome failed", err)
		}
		if finishing {
			if stage == "clear" {
				id = sessions.ID{}
			}
			expected, envelope := resetFinishExpected(t, f, f.owner, before, after, previous, id)
			if result.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), envelope.Record.Snapshot()) {
				t.Fatal("reset entropy published an unobserved session")
			}
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
		} else {
			expected, wanted := resetEntryExpected(t, f, before, after, previous, id, token.Encoded())
			if result.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), wanted.Snapshot()) {
				t.Fatal("reset entry collision changed its prepared proof")
			}
			assertUserReferenceState(t, expected, after, f.actor.Principal())
		}
	} else {
		selectedResetNoResult(t, result, err)
		if mode == "unknown_collision" {
			requireProfileCode(t, err, accountmodel.CodeOutcomeUnknown)
			if !errors.Is(err, fault) {
				t.Fatal("uncertain collision lost its native cause", err)
			}
		} else if !errors.Is(err, &sessions.Error{Code: sessions.CodeEntropy}) {
			t.Fatal("entropy failure lost its allocation classification", err)
		}
		if mode == "entropy_failure" && !errors.Is(err, io.EOF) {
			t.Fatal("entropy reader failure cause was lost", err)
		}
		assertUserReferenceState(t, before, after, f.actor.Principal())
	}
	for _, value := range []any{result, err, owner} {
		selfPasswordPrivate(t, value, selfPassword, token.Encoded(), previous.ID().Encoded(), collision.ID().Encoded(), id.Encoded(), f.target.EncodedPassword)
	}
}

func passwordResetEntropy(t *testing.T, open Open) {
	for _, stage := range []string{"create", "rotate", "finish"} {
		t.Run(stage, func(t *testing.T) {
			for _, mode := range []string{"confirmed_retry", "exhausted", "unknown_collision", "entropy_failure"} {
				t.Run(mode, func(t *testing.T) { passwordResetEntropyCase(t, open, stage, mode) })
			}
		})
	}
	t.Run("self_clear_no_entropy", func(t *testing.T) { passwordResetEntropyCase(t, open, "clear", "self_clear_no_entropy") })
}
