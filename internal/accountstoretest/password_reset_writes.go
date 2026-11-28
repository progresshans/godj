package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

func resetWriteModes(stage string) []string {
	modes := []string{"skipped", "nil_session", "root_session", "repeated", "query", "opaque", "swallowed_failure", "proof_rejection", "proof_cleanup", "swallowed_proof", "invalid_clock", "callback_cancel", "cancel_before_commit", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"}
	operations := []string{"session"}
	switch stage {
	case "rotate":
		operations = []string{"rotation", "session"}
	case "finish":
		operations = []string{"rotation", "session", "password", "revoke", "audit"}
	case "clear":
		operations = []string{"password", "revoke", "audit"}
	}
	for _, operation := range operations {
		modes = append(modes, "before_"+operation, "after_"+operation)
	}
	point := "session"
	if stage == "finish" || stage == "clear" {
		point = "password"
		modes = append(modes, "policy_rejection", "policy_failure", "policy_cleanup", "swallowed_policy")
	}
	if stage == "finish" {
		modes = append(modes, "after_last_revoke")
	}
	for _, prefix := range []string{"before_", "after_"} {
		for _, kind := range []string{"entropy", "proof", "diagnostics"} {
			modes = append(modes, prefix+point+"_"+kind)
		}
	}
	return modes
}

func resetWriteCase(t *testing.T, open Open, stage, mode string) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	finishing := stage == "finish" || stage == "clear"
	if mode == "after_last_revoke" {
		// Keep 258 independently addressable targets and unrelated rows.
		// The injected failure follows target deletion beyond batch 256.
		for index := len(f.targetSessions); index < 258; index++ {
			f.targetSessions = append(f.targetSessions, passwordSession(t, f.runtime.Sessions(), f.credential, fmt.Sprintf("reset-late-target-%d", index)))
		}
	}
	base := resetNativeOwner(t, f)
	token := selectedResetToken(t, base, f.target.PrincipalID)
	var previous sessions.Record
	if stage == "clear" {
		previous = f.targetSessions[0]
	} else if stage != "create" {
		var err error
		previous, err = f.runtime.Sessions().Create(t.Context(), map[string]string{"cart": "retained reset payload", "csrf": "independent reset secret"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if finishing {
		before := userReferenceSnapshot(t, reopen(t))
		entropy := f.entropy.count()
		proof := selectedResetProof(t, base, previous, token)
		after := userReferenceSnapshot(t, reopen(t))
		expected, wanted := resetEntryExpected(t, f, before, after, previous, f.entropy.one(t, entropy), token.Encoded())
		if !reflect.DeepEqual(proof.Snapshot(), wanted.Snapshot()) {
			t.Fatal("reset write fixture did not establish its exact proof")
		}
		assertUserReferenceState(t, expected, after, f.actor.Principal())
		if stage == "clear" {
			f.targetSessions[0] = wanted
		}
		previous = wanted
	}
	f.instant.Add(int64(time.Minute))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	point, kind := mode, ""
	for _, candidate := range []string{"entropy", "proof", "diagnostics"} {
		if strings.HasSuffix(mode, "_"+candidate) && (strings.HasPrefix(mode, "before_") || strings.HasPrefix(mode, "after_")) {
			point, kind = strings.TrimSuffix(mode, "_"+candidate), candidate
		}
	}
	var fault error = errors.New("private selected reset write fault")
	switch kind {
	case "entropy":
		fault = &sessions.Error{Code: sessions.CodeEntropy}
	case "proof":
		fault = auth.ErrInvalidResetProof
	case "diagnostics":
		fault = validation.Reject(validation.NewErrors(validation.New("password", "native_reset_failure")), nil)
	}
	if mode == "opaque" {
		point, fault = "query", passwordWriteOpaqueError{"private opaque reset material"}
	}
	if mode == "swallowed_failure" {
		point = "query"
	}
	revokeAt := 0
	if mode == "after_last_revoke" {
		point, revokeAt = "after_revoke", len(f.targetSessions)
	}
	boundary := &profileFaultBackend{Backend: backend, mode: point, fault: fault, cancel: cancel}
	if mode == "proof_cleanup" || mode == "policy_cleanup" {
		boundary.mode = "cleanup_failure"
	}
	if mode == "swallowed_failure" || mode == "swallowed_proof" || mode == "swallowed_policy" {
		boundary.mode = "swallowed_rejection"
	}
	var effects []string
	faults := 0
	boundary.decorate = func(scope db.Session) db.Session {
		return &passwordWriteFaultScope{Session: scope, mode: point, fault: fault, effects: &effects, faults: &faults, revokeAt: revokeAt}
	}
	config := f.config(t)
	config.Accounts.ValidatePassword = func(_ context.Context, _ string, profile orm.ModelView[models.User]) (validation.Errors, error) {
		if passwordViewInteger(t, profile, "id") != f.target.ID || passwordViewString(t, profile, "encoded_password") != "" {
			t.Fatal("reset write policy lost its selected private profile")
		}
		if boundary.calls > 0 && (strings.HasPrefix(mode, "policy_") || mode == "swallowed_policy") {
			diagnostics := validation.NewErrors(validation.New("password", "current_reset_policy"))
			if mode == "policy_failure" {
				return diagnostics, fault
			}
			return diagnostics, nil
		}
		return validation.Errors{}, nil
	}
	f.owner = newUserReferenceOwner(t, boundary, f.userReferenceFixture, config)
	f.runtime = f.owner.runtime
	resetConfig := resetNativeConfig(t, f)
	resetConfig.Clock = func() time.Time {
		if boundary.calls > 0 {
			if mode == "invalid_clock" {
				return time.Time{}
			}
			if mode == "proof_rejection" || mode == "proof_cleanup" || mode == "swallowed_proof" {
				return f.clock().Add(73 * time.Hour)
			}
		}
		return f.clock()
	}
	resetter, err := f.runtime.PasswordResetter(resetConfig)
	if err != nil {
		t.Fatal(err)
	}
	before := userReferenceSnapshot(t, reopen(t))
	entropy := f.entropy.count()
	var result auth.PasswordResetResult
	if finishing {
		result, err = resetter.ResetPassword(ctx, previous.ID(), f.target.PrincipalID, selfPassword)
	} else {
		result.Record, err = resetter.StartPasswordReset(ctx, previous.ID(), f.target.PrincipalID, token.Encoded())
	}
	hashes := int64(0)
	if finishing {
		hashes = 1
	}
	if boundary.calls != 1 || f.owner.boundary.calls != 1 || f.owner.hasher.hashes.Load() != hashes || f.owner.hasher.verifies.Load() != 0 {
		t.Fatal("reset write retried or repeated password work", stage, mode, boundary.calls, f.owner.hasher.hashes.Load())
	}
	var id sessions.ID
	if stage == "clear" {
		if f.entropy.count() != entropy {
			t.Fatal("self reset removal generated a replacement bearer")
		}
	} else {
		id = f.entropy.one(t, entropy)
	}
	committed := mode == "unknown_commit" || mode == "post_commit_cancel"
	if mode == "post_commit_cancel" {
		if err != nil || ctx.Err() != context.Canceled {
			t.Fatal("late cancellation erased a confirmed reset effect", err)
		}
	} else {
		selectedResetNoResult(t, result, err)
		if mode == "proof_rejection" {
			if err != auth.ErrInvalidResetProof {
				t.Fatal("confirmed current proof rejection lost its direct refusal", err)
			}
		} else if mode == "policy_rejection" {
			if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || errors.Unwrap(err) != nil || f.owner.policies.Load() != 2 {
				t.Fatal("confirmed current reset policy lost its direct diagnostics", err)
			}
		} else {
			if _, rejected := validation.Rejected(err); rejected || err == auth.ErrInvalidResetProof || err == auth.ErrInvalidResetToken {
				t.Fatal("native reset failure became ordinary proof/password refusal", err)
			}
			code := accountmodel.CodePersistence
			if mode == "skipped" || mode == "repeated" || strings.HasPrefix(mode, "swallowed_") || mode == "unknown_commit" || mode == "unknown_rollback" ||
				errors.Is(boundary.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(boundary.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				code = accountmodel.CodeOutcomeUnknown
			}
			requireProfileCode(t, err, code)
		}
	}
	var want []string
	switch stage {
	case "create":
		want = []string{"session"}
	case "rotate":
		want = []string{"rotation", "session"}
	case "finish":
		want = []string{"rotation", "session", "password"}
	case "clear":
		want = []string{"password"}
	}
	if finishing {
		for range len(f.targetSessions) {
			want = append(want, "revoke")
		}
		want = append(want, "audit")
	}
	if point == "query" || mode == "skipped" || mode == "nil_session" || mode == "root_session" || mode == "invalid_clock" ||
		strings.HasPrefix(mode, "policy_") || strings.HasPrefix(mode, "proof_") || mode == "swallowed_proof" || mode == "swallowed_policy" {
		want = nil
	} else if strings.HasPrefix(point, "before_") || strings.HasPrefix(point, "after_") {
		operation := strings.TrimPrefix(strings.TrimPrefix(point, "before_"), "after_")
		index := slices.Index(want, operation)
		if mode == "after_last_revoke" {
			index = len(want) - 2
		}
		if index < 0 {
			t.Fatal("reset fault has no native operation", mode)
		}
		if strings.HasPrefix(point, "after_") {
			index++
		}
		want = want[:index]
	}
	if !slices.Equal(effects, want) {
		t.Fatal("reset failure missed its exact native write prefix", stage, mode, effects, want)
	}
	if point == "query" || strings.HasPrefix(point, "before_") || strings.HasPrefix(point, "after_") {
		matches := errors.Is(err, fault)
		if mode == "opaque" {
			var observed passwordWriteOpaqueError
			matches = errors.As(err, &observed) && reflect.DeepEqual(observed, fault)
		}
		if faults != 1 || !matches {
			t.Fatal("native reset failure lost its single explicit cause", err, faults)
		}
	}
	if mode == "policy_failure" || mode == "policy_cleanup" || mode == "proof_cleanup" {
		if !errors.Is(err, fault) {
			t.Fatal("reset policy/cleanup failure lost its cause", err)
		}
	}
	if mode == "callback_cancel" || mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" {
		if !errors.Is(err, context.Canceled) || boundary.nativeError == nil || !errors.Is(err, boundary.nativeError) {
			t.Fatal("reset cancellation lost the native transaction outcome", err)
		}
		if mode == "cancel_confirmed_rollback" && (errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
			t.Fatal("confirmed reset rollback became uncertain")
		}
	}
	after := userReferenceSnapshot(t, reopen(t))
	if committed {
		if finishing {
			expected, envelope := resetFinishExpected(t, f, f.owner, before, after, previous, id)
			assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
			if mode == "post_commit_cancel" && (result.ClearSession != envelope.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), envelope.Record.Snapshot())) {
				t.Fatal("confirmed reset result differs from its native committed state")
			}
		} else {
			expected, record := resetEntryExpected(t, f, before, after, previous, id, token.Encoded())
			assertUserReferenceState(t, expected, after, f.actor.Principal())
			if mode == "post_commit_cancel" && (result.ClearSession || !reflect.DeepEqual(result.Record.Snapshot(), record.Snapshot())) {
				t.Fatal("confirmed reset entry did not publish its exact proof")
			}
		}
	} else if !reflect.DeepEqual(before, after) {
		t.Fatal("refused reset changed any native account, session, audit, domain or host state")
	}
	encoded, _ := f.owner.encoded.Load().(string)
	for _, value := range []any{result, err, resetter} {
		selfPasswordPrivate(t, value, selfPassword, token.Encoded(), previous.ID().Encoded(), id.Encoded(), f.target.EncodedPassword, encoded, "private selected reset write fault", "private password opaque fault", "private opaque reset material")
	}
}

func runResetWriteNative(add registerCase, open Open) {
	add("password_reset_write_failures", func(t *testing.T) {
		for _, stage := range []string{"create", "rotate", "finish", "clear"} {
			t.Run(stage, func(t *testing.T) {
				for _, mode := range resetWriteModes(stage) {
					t.Run(mode, func(t *testing.T) { resetWriteCase(t, open, stage, mode) })
				}
			})
		}
	})
}
