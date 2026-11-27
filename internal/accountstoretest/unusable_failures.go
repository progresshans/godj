package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func runUnusableWriteFailures(add registerCase, open Open) {
	add("unusable_write_failures", func(t *testing.T) {
		for _, mode := range []string{"skipped", "nil_session", "root_session", "password_update", "revoke_failure", "late_revoke_failure", "audit_insert", "repeated", "cleanup_failure", "swallowed_failure", "invalid_clock", "unknown_rollback", "unknown_commit", "cancel_before_commit", "cancel_confirmed_rollback", "post_commit_cancel"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				count := 3
				if mode == "late_revoke_failure" {
					count = 258
				}
				f := provisionUnusable(t, backend, count)
				config := unusableConfig(t)
				if mode == "invalid_clock" {
					config.Accounts.Now = func() time.Time { return time.Time{} }
				}
				owner := newUserReferenceOwner(t, backend, f.userReferenceFixture, config)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fault := errors.New("private selected unusable mutation failure")
				owner.boundary.mode, owner.boundary.fault, owner.boundary.cancel = mode, fault, cancel
				faultMode := mode
				if mode == "cleanup_failure" || mode == "swallowed_failure" {
					// The native transaction really rolls back after an audit
					// INSERT; only its completion report is altered afterward.
					faultMode = "audit_insert"
				}
				if mode == "swallowed_failure" {
					owner.boundary.mode = "swallowed_rejection"
				}
				effects := map[string]int{}
				owner.boundary.decorate = func(scope db.Session) db.Session { return &passwordFaultScope{scope, faultMode, fault, effects} }
				before := userReferenceSnapshot(t, reopen(t))
				result, err := owner.runtime.Accounts().Passwords().SetUnusablePassword(ctx, f.actor.Principal(), f.target.ID, f.target.Revision)
				committed := mode == "unknown_commit" || mode == "post_commit_cancel"
				if mode == "post_commit_cancel" {
					if err != nil || result.ID != f.target.ID || result.Revision != 2 || result.RevokedSessions != count || ctx.Err() == nil {
						t.Fatal("late cancellation erased a confirmed disablement", err)
					}
				} else {
					passwordEditNoResult(t, result, err)
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_failure" || mode == "unknown_commit" || mode == "unknown_rollback" || errors.Is(owner.boundary.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(owner.boundary.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
					if _, rejected := validation.Rejected(err); rejected || strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
						t.Fatal("unusable infrastructure failure became input or disclosed its cause")
					}
				}
				if owner.hasher.hashes.Load() != 0 || owner.policies.Load() != 0 || owner.boundary.calls != 1 {
					t.Fatal("failed disablement invoked password work or replayed a native write")
				}
				if mode == "password_update" || mode == "revoke_failure" || mode == "late_revoke_failure" || mode == "audit_insert" || mode == "cleanup_failure" {
					if !errors.Is(err, fault) {
						t.Fatal("unusable failure lost its actual write or cleanup cause", err)
					}
				}
				if mode == "unknown_commit" || mode == "unknown_rollback" {
					if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
						t.Fatal("unusable native outcome lost its uncertainty", err)
					}
				}
				if mode == "skipped" || mode == "repeated" || mode == "swallowed_failure" {
					if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
						t.Fatal("unusable callback ownership lost its uncertain result", err)
					}
				}
				if (mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback") && (!errors.Is(err, context.Canceled) || owner.boundary.nativeError == nil || !errors.Is(err, owner.boundary.nativeError)) {
					t.Fatal("canceled disablement lost its native outcome", err)
				}
				if mode == "cancel_confirmed_rollback" && (errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
					t.Fatal("independent owner did not acknowledge unusable rollback", err)
				}
				wantDeletes := 0
				if mode == "revoke_failure" {
					wantDeletes = 2
				}
				if mode == "late_revoke_failure" || mode == "audit_insert" || mode == "repeated" || mode == "cleanup_failure" || mode == "swallowed_failure" || mode == "invalid_clock" || mode == "unknown_rollback" || committed || mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" {
					wantDeletes = count
				}
				if mode == "password_update" || wantDeletes != 0 {
					if effects["update:godj_identity_user"] != 1 || effects["session_delete"] != wantDeletes {
						t.Fatal("unusable fault missed its exact user/session write prefix", effects)
					}
				}
				if mode == "audit_insert" || mode == "repeated" || mode == "cleanup_failure" || mode == "swallowed_failure" || mode == "unknown_rollback" || committed || mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" {
					if effects["insert:godj_account_audit_event"] != 1 {
						t.Fatal("unusable outcome did not follow the real audit INSERT")
					}
				}
				if committed {
					if owner.boundary.nativeError != nil {
						t.Fatal("claimed committed disablement did not complete natively", owner.boundary.nativeError)
					}
					f.changed(t, before, owner, true)
				} else {
					assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
					for _, record := range f.records {
						accountSessionRecord(t, f.owner.runtime.SessionStore(), record, true)
					}
				}
			})
		}
	})
}
