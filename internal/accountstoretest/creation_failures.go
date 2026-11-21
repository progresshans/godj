package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/admin"
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
	credential := profileActor(t, f)
	actor := credential.Principal()
	now := joined
	_, manager, _ := accountSessions(t, backend, &now)
	passwordSession(t, manager, credential, "unrelated actor")
	if _, err := manager.Create(t.Context(), map[string]string{"anonymous": "preserved"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil_session", "root_session", "write_failure", "group_failure", "direct_failure", "audit_failure", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			before := accountNativeSnapshot(t, reopen(t))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private creation fault")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			effects := map[string]int{}
			if mode == "write_failure" || mode == "group_failure" || mode == "direct_failure" || mode == "audit_failure" {
				owner.mode = "creation_" + mode
				owner.decorate = func(scope db.Session) db.Session {
					return newRoleFaultScope(t, scope, mode, fault, effects)
				}
			}
			commit := false
			config := creationConfig()
			principal := "creation-failure-" + mode
			config.PrincipalID = func(context.Context) (string, error) { return principal, nil }
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
			tracked := &creationOwner{Backend: owner}
			var encoded atomic.Value
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes, encode: func(ctx context.Context, raw string) (string, error) {
				value, err := f.hasher.Hash(ctx, raw)
				if err == nil {
					encoded.Store(value)
				}
				return value, err
			}}
			creator := accountCreator(t, tracked, hasher, config)
			input := roleInput(t, map[string]query.Value{"username": query.String("failure-" + mode)}, map[string][]int64{"groups": {f.group.ID}, "permissions": {f.grants[0].ID}})
			pending, err := creator.Prepare(ctx, actor, input, password)
			if err != nil {
				t.Fatal("failure fixture prepare", err)
			}
			assertCatalogRows(t, before, accountNativeSnapshot(t, reopen(t)), actor)
			commit = true
			result, err := creator.Commit(ctx, actor, pending)
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if owner.calls != 1 || hasher.hashes.Load() != 1 {
				t.Fatal("creation skipped or retried write/hash", owner.calls, hasher.hashes.Load())
			}
			if mode == "post_commit_cancel" {
				if err != nil || result.Revision != 1 || ctx.Err() == nil || owner.nativeError != nil {
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
				if strings.HasSuffix(mode, "_failure") && !errors.Is(err, fault) {
					t.Fatal("creation failure lost its native or validator cause", err)
				}
				if mode == "cancel_confirmed_rollback" && (ctx.Err() == nil || !errors.Is(err, context.Canceled) || owner.nativeError == nil || !errors.Is(err, owner.nativeError)) {
					t.Fatal("creation cancellation lost acknowledged native rollback", err)
				}
			}
			if again, err := creator.Commit(t.Context(), actor, pending); again.Revision != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
				t.Fatal("failed or uncertain candidate reused", err)
			}
			after := accountNativeSnapshot(t, reopen(t))
			if committed {
				var allocated []models.User
				for _, row := range after.users {
					if row.Username == "failure-"+mode {
						allocated = append(allocated, row)
					}
				}
				if len(allocated) != 1 || allocated[0].ID <= 0 || slices.ContainsFunc(before.users, func(row models.User) bool { return row.ID == allocated[0].ID }) {
					t.Fatal("creation did not allocate one new selected user")
				}
				expected := userReferenceExpectedRow(allocated[0].ID, "failure-"+mode, "", encoded.Load().(string))
				expected.PrincipalID = principal
				userReferenceReplace(&before, expected)
				userReferenceRelations(t, &before, after, expected.ID, []int64{f.group.ID}, []int64{f.grants[0].ID})
				assertCatalogRows(t, before, after, actor, catalogExpectedEvent{"user", expected.ID, admin.ActionAdd, userReferenceCreationFields})
				if mode == "post_commit_cancel" && (result.ID != expected.ID || result.Revision != 1 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, userReferenceCreationFields)) {
					t.Fatal("confirmed creation metadata differs from native effect")
				}
			} else {
				assertCatalogRows(t, before, after, actor)
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
