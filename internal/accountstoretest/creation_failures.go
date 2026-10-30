package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func creationCounts(t *testing.T, backend db.Queryer) []int64 {
	t.Helper()
	result := make([]int64, 0, 4)
	for _, count := range []func(context.Context) (int64, error){models.UserObjects.Using(backend).Count, models.UserGroupsLinkObjects.Using(backend).Count, models.UserPermissionsLinkObjects.Using(backend).Count, auditmodels.EventObjects.Using(backend).Count} {
		value, err := count(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, value)
	}
	return result
}

func creationCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	for _, mode := range []string{"skipped", "nil_session", "root_session", "write_failure", "group_failure", "direct_failure", "audit_failure", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			before := creationCounts(t, backend)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private creation fault")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			effects := map[string]int{}
			if mode == "write_failure" || mode == "group_failure" || mode == "direct_failure" || mode == "audit_failure" {
				owner.mode = "creation_" + mode
				owner.decorate = func(scope db.Session) db.Session {
					relation, ok := scope.(db.RelationSession)
					if !ok {
						t.Fatal("lost relation scope")
					}
					conflict, ok := scope.(db.ConflictInserter)
					if !ok {
						t.Fatal("lost conflict insert")
					}
					return &roleFaultScope{RelationSession: relation, conflict: conflict, mode: mode, fault: fault, effects: effects}
				}
			}
			commit := false
			config := creationConfig()
			config.Validate = func(context.Context, db.Queryer, models.User, accountmodel.AccountMemberships) (validation.Errors, error) {
				if !commit {
					return validation.Errors{}, nil
				}
				if mode == "validator_rejection" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_rejection" {
					diagnostics := validation.NewErrors(validation.New("first_name", "rejected"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
				return validation.Errors{}, nil
			}
			config.Now = func() time.Time {
				if mode == "invalid_clock" && commit {
					return time.Time{}
				}
				return joined
			}
			hasher := &creationHasher{PasswordHasher: f.hasher}
			creator := accountCreator(t, owner, hasher, config)
			input := roleInput(t, map[string]query.Value{"username": query.String("failure-" + mode)}, map[string][]int64{"groups": {f.group.ID}, "permissions": {f.grants[0].ID}})
			pending, err := creator.Prepare(ctx, actor, input, password)
			if err != nil {
				t.Fatal("failure fixture prepare", err)
			}
			commit = true
			result, err := creator.Commit(ctx, actor, pending)
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if owner.calls != 1 || hasher.calls.Load() != 1 {
				t.Fatal("creation skipped or retried write/hash", owner.calls, hasher.calls.Load())
			}
			if mode == "post_commit_cancel" {
				if err != nil || result.Revision != 1 {
					t.Fatal("late cancel erased commit", err)
				}
			} else {
				if err == nil || !reflect.DeepEqual(result, accountmodel.Creation{}) {
					t.Fatal("failed creation published result", result, err)
				}
				if mode == "validator_rejection" {
					if failures, rejected := validation.Rejected(err); !rejected || failures.ByField("first_name").Empty() {
						t.Fatal("confirmed rejection lost", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected {
						t.Fatal("execution failure became input rejection")
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "unknown_rollback" || mode == "unknown_commit" {
						code = accountmodel.CodeOutcomeUnknown
					}
					requireProfileCode(t, err, code)
				}
				for _, text := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
					if strings.Contains(text, fault.Error()) || strings.Contains(text, password) {
						t.Fatal("private failure exposed")
					}
				}
			}
			if again, err := creator.Commit(t.Context(), actor, pending); again.Revision != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
				t.Fatal("failed or uncertain candidate reused", err)
			}
			after := creationCounts(t, reopen(t))
			for i := range before {
				expected := before[i]
				if committed {
					expected++
				}
				if after[i] != expected {
					t.Fatal("creation left partial row, membership or audit", mode, before, after)
				}
			}
			if exists := creationExists(t, backend, "failure-"+mode); exists != committed {
				t.Fatal("durable creation differs from outcome", mode)
			}
			// Lifecycle companions (including audit) precede the collected links.
			// The final direct-link fault still follows every native write.
			tables := []string{"godj_identity_user", "godj_account_audit_event", "godj_identity_user_groups", "godj_identity_user_permissions"}
			failing := map[string]int{"write_failure": 0, "audit_failure": 1, "group_failure": 2, "direct_failure": 3}
			if last, injected := failing[mode]; injected {
				for index, table := range tables {
					want := 0
					if index <= last {
						want = 1
					}
					if effects[table] != want {
						t.Fatal("creation fault native effect prefix differs", mode, table, effects)
					}
				}
			}
		})
	}
}
