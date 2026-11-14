package accountstoretest

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

type resetSessionReference struct {
	Django, Python, Backend string
	Sources                 map[string]string `json:"source_sha256"`
	Observations            struct {
		Success            map[string]map[string]json.RawMessage
		SessionSaveFailure map[string]json.RawMessage `json:"session_save_failure"`
		PasswordErrors     map[string]struct {
			Errors map[string][]string
			Status int
		} `json:"password_errors"`
	}
}

func resetHTTPReference(t *testing.T, vendor string) resetSessionReference {
	t.Helper()
	raw, err := identityreference.ResetHTTP(vendor)
	if err != nil {
		t.Fatal(err)
	}
	var reference resetSessionReference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 8 || len(reference.Observations.Success) != 3 {
		t.Fatal("incomplete or mismatched independent reset session reference")
	}
	for _, name := range []string{"auth", "csrf", "forms", "hashers", "session_backend", "session_middleware", "tokens", "views"} {
		value := reference.Sources[name]
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			t.Fatal("missing independent reset HTTP source digest")
		}
	}
	return reference
}

func resetSessionObservation(t *testing.T, mode string) map[string]bool {
	t.Helper()
	var first map[string]bool
	for _, vendor := range []string{"sqlite", "postgres"} {
		reference := resetHTTPReference(t, vendor)
		boolean := func(values map[string]json.RawMessage, name string) bool {
			var value *bool
			if err := json.Unmarshal(values[name], &value); err != nil || value == nil {
				t.Fatal("missing reset session observation", name, err)
			}
			return *value
		}
		key := mode
		if key != "self" && key != "other" {
			key = "anonymous"
		}
		observations, found := reference.Observations.Success[key]
		if !found {
			t.Fatal("missing reset browser observation", key)
		}
		actual := make(map[string]bool)
		for _, name := range []string{"authenticated", "token_removed", "last_login_unchanged", "new_password_valid", "trimmed_password_valid", "old_authenticated"} {
			actual[name] = boolean(observations, name)
		}
		if !boolean(observations, "old_session_before_access") || boolean(observations, "old_session_after_access") || !boolean(reference.Observations.SessionSaveFailure, "new_password_valid") || !boolean(reference.Observations.SessionSaveFailure, "persisted_token_retained") {
			t.Fatal("Django lazy revocation or non-atomic session save deviation disappeared")
		}
		if first != nil && !reflect.DeepEqual(first, actual) {
			t.Fatal("native reset session references disagree")
		}
		first = actual
	}
	return first
}

func resetBrowserPrevious(t *testing.T, f *loginFixture, mode string) sessions.Record {
	t.Helper()
	switch mode {
	case "missing":
		return sessions.Record{}
	case "self":
		return f.targetSessions[0]
	case "other":
		return passwordSession(t, f.runtime.Sessions(), f.actor, "other reset browser")
	case "expired":
		at := f.clock()
		f.instant.Store(at.Add(-3 * time.Hour).UnixNano())
		defer f.instant.Store(at.UnixNano())
	}
	record, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"cart": "retained browser payload", "csrf": "independent"})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func passwordResetLifecycle(t *testing.T, open Open) {
	for _, mode := range []string{"missing", "anonymous", "self", "other", "expired", "same_password"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			owner := resetNativeOwner(t, f)
			previous := resetBrowserPrevious(t, f, mode)
			before := userReferenceSnapshot(t, reopen(t))
			f.owner.boundary.calls = 0
			entropy := f.entropy.count()
			token := selectedResetToken(t, owner, f.target.PrincipalID)
			if token.Username() != f.target.Username || token.Email() != f.target.Email {
				t.Fatal("selected delivery lost its current profile")
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			f.instant.Add(int64(time.Minute))
			proof := selectedResetProof(t, owner, previous, token)
			afterEntry := userReferenceSnapshot(t, reopen(t))
			prior := previous
			if mode == "expired" {
				prior = sessions.Record{}
			}
			expected, wanted := resetEntryExpected(t, f, before, afterEntry, prior, f.entropy.one(t, entropy), token.Encoded())
			if !reflect.DeepEqual(proof.Snapshot(), wanted.Snapshot()) || f.owner.boundary.calls != 1 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 {
				t.Fatal("reset entry changed current state, inherited expired authority or performed password work")
			}
			assertUserReferenceState(t, expected, afterEntry, f.actor.Principal())
			if mode == "self" {
				f.targetSessions[0] = wanted
			}
			entropy = f.entropy.count()
			f.owner.boundary.calls = 0
			if err := owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, nil); err != nil || f.owner.policies.Load() != 0 {
				t.Fatal("proof-only admission ran password policy", err)
			}
			weak := "short"
			if diagnostics, rejected := validation.Rejected(owner.CheckPasswordReset(t.Context(), proof.ID(), f.target.PrincipalID, &weak)); !rejected || diagnostics.ByField("password").Empty() {
				t.Fatal("proof check lost its typed strength policy")
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.boundary.calls != 0 || f.entropy.count() != entropy {
				t.Fatal("advisory check changed password/session work")
			}
			assertUserReferenceState(t, afterEntry, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			// Consume through a newly constructed owner on an independent DB connection.
			second := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
			consumer, err := second.runtime.PasswordResetter(resetNativeConfig(t, f))
			if err != nil {
				t.Fatal(err)
			}
			raw := selfPassword
			if mode == "same_password" {
				raw = password
			}
			f.instant.Add(int64(time.Minute))
			result, err := consumer.ResetPassword(t.Context(), proof.ID(), f.target.PrincipalID, raw)
			if err != nil || second.hasher.hashes.Load() != 1 || second.hasher.verifies.Load() != 0 || second.policies.Load() != 2 || second.boundary.calls != 1 {
				t.Fatal("reopened reset lost its one-hash native transition", err)
			}
			var id sessions.ID
			if mode == "self" {
				if f.entropy.count() != entropy {
					t.Fatal("self reset allocated a bearer")
				}
			} else {
				id = f.entropy.one(t, entropy)
			}
			consumedEntropy := f.entropy.count()
			after := userReferenceSnapshot(t, reopen(t))
			expected, envelope := resetFinishExpected(t, f, second, afterEntry, after, wanted, id)
			if result.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), envelope.Record.Snapshot()) {
				t.Fatal("reopened reset published different browser authority")
			}
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			// The exact snapshot above proves eager revocation before any access.
			current, err := f.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, raw)
			if err != nil {
				t.Fatal("reset did not survive the original connection", err)
			}
			_, trimmedErr := f.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, strings.TrimSpace(raw))
			if strings.TrimSpace(raw) != raw && trimmedErr != auth.ErrInvalidCredentials || strings.TrimSpace(raw) == raw && trimmedErr != nil {
				t.Fatal("reset changed raw password bytes", trimmedErr)
			}
			authenticated := false
			if principalID, found := result.Record.Value(auth.SessionPrincipalIDKey); found {
				credential, err := second.runtime.Authenticator().Resolve(t.Context(), principalID)
				stamp, stamped := result.Record.Value(auth.SessionCredentialStampKey)
				authenticated = err == nil && stamped && credential.Principal().Authenticated() && credential.MatchesSessionStamp(stamp)
			}
			stored := profileRow(t, reopen(t), f.target.ID)
			_, tokenPresent := result.Record.Value(auth.SessionResetTokenKey)
			_, targetPresent := result.Record.Value(auth.SessionResetPrincipalIDKey)
			actual := map[string]bool{"authenticated": authenticated, "token_removed": !tokenPresent && !targetPresent, "last_login_unchanged": stored.LastLogin != nil && stored.LastLogin.Equal(*f.target.LastLogin), "new_password_valid": current.Principal().Authenticated(), "trimmed_password_valid": trimmedErr == nil, "old_authenticated": current.MatchesSessionStamp(f.credential.SessionStamp())}
			wantedObservation := resetSessionObservation(t, mode)
			if mode == "same_password" {
				// This control reuses the fixture's existing raw password without
				// surrounding spaces; trimming is therefore the identical input.
				if wantedObservation["trimmed_password_valid"] || strings.TrimSpace(raw) != raw {
					t.Fatal("same-password raw input control changed")
				}
				wantedObservation["trimmed_password_valid"] = true
			}
			if !reflect.DeepEqual(actual, wantedObservation) {
				t.Fatal("selected browser lifecycle differs from shared Django reset semantics", mode)
			}
			for _, checkID := range []sessions.ID{proof.ID(), result.Record.ID()} {
				if err := consumer.CheckPasswordReset(t.Context(), checkID, f.target.PrincipalID, nil); err != auth.ErrInvalidResetProof {
					t.Fatal("consumed session retained proof", err)
				}
			}
			replay, err := consumer.StartPasswordReset(t.Context(), sessions.ID{}, f.target.PrincipalID, token.Encoded())
			if err != auth.ErrInvalidResetProof || !reflect.DeepEqual(replay, sessions.Record{}) || second.hasher.hashes.Load() != 1 || second.policies.Load() != 2 || second.boundary.calls != 1 || f.entropy.count() != consumedEntropy {
				t.Fatal("reopened consumed token repeated native work", err)
			}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			for _, value := range []any{owner, consumer, result, err} {
				selfPasswordPrivate(t, value, raw, token.Encoded(), proof.ID().Encoded(), result.Record.ID().Encoded(), stored.EncodedPassword)
			}
		})
	}
}
