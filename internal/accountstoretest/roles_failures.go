package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/validation"
)

type roleFaultScope struct {
	db.RelationSession
	conflict db.ConflictInserter
	mode     string
	fault    error
	effects  map[string]int
}

func newRoleFaultScope(t *testing.T, scope db.Session, mode string, fault error, effects map[string]int) *roleFaultScope {
	t.Helper()
	relation, valid := scope.(db.RelationSession)
	if !valid {
		t.Fatal("role mutation lost its native relation scope")
	}
	conflict, valid := scope.(db.ConflictInserter)
	if !valid {
		t.Fatal("role mutation lost native conflict insertion")
	}
	return &roleFaultScope{RelationSession: relation, conflict: conflict, mode: mode, fault: fault, effects: effects}
}

func (scope *roleFaultScope) ValidateSession(ctx context.Context) error {
	return scope.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *roleFaultScope) after(table string, err error) error {
	if err != nil {
		return err
	}
	scope.effects[table]++
	wanted := map[string]string{"domain_failure": "godj_account_domain_transition", "write_failure": "godj_identity_user", "group_failure": "godj_identity_user_groups", "direct_failure": "godj_identity_user_permissions", "session_failure": "godj_sessions_session", "audit_failure": "godj_account_audit_event"}
	if table == wanted[scope.mode] {
		return scope.fault
	}
	return nil
}
func (scope *roleFaultScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.RelationSession.Insert(ctx, plan)
	return key, scope.after(plan.Table(), err)
}
func (scope *roleFaultScope) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	inserted, err := scope.conflict.InsertOnConflict(ctx, plan)
	return inserted, scope.after(plan.Table(), err)
}
func (scope *roleFaultScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := scope.RelationSession.Update(ctx, plan)
	return count, scope.after(plan.Table(), err)
}
func (scope *roleFaultScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.RelationSession.Delete(ctx, plan)
	return count, scope.after(plan.Table(), err)
}

func roleCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	actor := credential.Principal()
	now := joined
	store, manager, _ := accountSessions(t, backend, &now)
	reader := roleReader(t, backend)
	passwordSession(t, manager, credential, "unrelated actor")
	if _, err := manager.Create(t.Context(), map[string]string{"anonymous": "preserved"}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil_session", "root_session", "write_failure", "group_failure", "direct_failure", "session_failure", "audit_failure", "repeated", "validator_rejection", "validator_failure", "cleanup_failure", "swallowed_rejection", "invalid_clock", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "role-failure-"+mode)
			beforeSession := accountNativeSnapshot(t, reopen(t))
			record, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: target.PrincipalID})
			if err != nil {
				t.Fatal(err)
			}
			accountSessionRecord(t, store, record, true)
			before := accountNativeSnapshot(t, reopen(t))
			sessionExpected, sessionKey := accountNativeSessionAddition(t, beforeSession, before)
			assertCatalogRows(t, sessionExpected, before, actor)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private role mutation failure")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			effects := make(map[string]int)
			if strings.HasSuffix(mode, "_failure") && mode != "cleanup_failure" && mode != "validator_failure" {
				owner.mode = "role_" + mode
				owner.decorate = func(scope db.Session) db.Session {
					return newRoleFaultScope(t, scope, mode, fault, effects)
				}
			}
			config := accountEditConfig()
			if mode == "validator_rejection" || mode == "validator_failure" || mode == "cleanup_failure" || mode == "swallowed_rejection" {
				config.Validate = func(context.Context, db.Queryer, models.User, models.User, accountmodel.AccountMemberships) (validation.Errors, error) {
					diagnostics := validation.NewErrors(validation.New("staff", "rejected"))
					if mode == "validator_failure" {
						return diagnostics, fault
					}
					return diagnostics, nil
				}
			}
			if mode == "invalid_clock" {
				config.Now = func() time.Time { return time.Time{} }
			}
			scopedSessions, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
			if err != nil {
				t.Fatal(err)
			}
			editor, err := project.NewAccountEditor(scopedSessions, auth.PrincipalAuthorizer{}, config)
			if err != nil {
				t.Fatal(err)
			}
			input := roleInput(t, map[string]query.Value{"username": query.String("combined-" + mode), "email": query.String("Local@EXAMPLE.TEST"), "first_name": query.String("Combined profile"), "active": query.Boolean(false), "staff": query.Boolean(true)}, map[string][]int64{"groups": {f.group.ID}, "permissions": {f.grants[0].ID}})
			result, err := editor.Update(ctx, actor, target.ID, 1, input)
			if owner.calls != 1 {
				t.Fatal("role mutation retried or skipped configured owner", owner.calls)
			}
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || result.Revision != 2 || result.RevokedSessions != 1 || ctx.Err() == nil || owner.nativeError != nil {
					t.Fatal("late cancellation erased committed role edit", err)
				}
			} else {
				if err == nil || result.ID != 0 || result.Revision != 0 || result.ChangedFields != nil || result.RevokedSessions != 0 {
					t.Fatal("failed role operation published metadata", result, err)
				}
				if mode == "validator_rejection" {
					if fields, rejected := validation.Rejected(err); !rejected || fields.ByField("staff").Empty() {
						t.Fatal("confirmed validation rejection lost", err)
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
				if mode == "cancel_confirmed_rollback" && (!errors.Is(err, context.Canceled) || owner.nativeError == nil || !errors.Is(err, owner.nativeError)) {
					t.Fatal("cancellation lost acknowledged native rollback", err)
				}
				if strings.HasSuffix(mode, "_failure") && !errors.Is(err, fault) {
					t.Fatal("injected failure did not occur after intended mutation", err)
				}
				if strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
					t.Fatal("private role failure leaked")
				}
			}
			if mode == "direct_failure" && (effects["godj_identity_user_groups"] == 0 || effects["godj_sessions_session"] == 0 || effects["godj_account_audit_event"] == 0) {
				t.Fatal("direct failure did not follow companion and first collection writes")
			}
			if mode == "session_failure" && (effects["godj_identity_user_groups"] != 0 || effects["godj_identity_user_permissions"] != 0 || effects["godj_sessions_session"] == 0 || effects["godj_account_audit_event"] != 0) {
				t.Fatal("session failure did not occur after revocation and before relation writes")
			}
			if mode == "audit_failure" && (effects["godj_sessions_session"] == 0 || effects["godj_account_audit_event"] == 0) {
				t.Fatal("audit failure did not follow session revocation")
			}
			if mode == "write_failure" && (effects["godj_identity_user_groups"] == 0 || effects["godj_identity_user_permissions"] == 0 || effects["godj_sessions_session"] == 0 || effects["godj_account_audit_event"] == 0) {
				t.Fatal("final scalar failure missed completed companion and collection writes")
			}
			after := accountNativeSnapshot(t, reopen(t))
			if committed {
				expected := target
				expected.Username, expected.Email, expected.FirstName = "combined-"+mode, "Local@example.test", "Combined profile"
				expected.Active, expected.Staff, expected.Revision = false, true, 2
				userReferenceReplace(&before, expected)
				userReferenceRelations(t, &before, after, target.ID, []int64{f.group.ID}, []int64{f.grants[0].ID})
				before.sessions = slices.DeleteFunc(slices.Clone(before.sessions), func(row sessionmodels.Session) bool { return row.ID == sessionKey })
				fields := []string{"username", "first_name", "email", "active", "staff", "groups", "permissions"}
				assertCatalogRows(t, before, after, actor, catalogExpectedEvent{"user", target.ID, admin.ActionChange, fields})
				if mode == "post_commit_cancel" && (result.ID != target.ID || result.Revision != 2 || result.RevokedSessions != 1 || !slices.Equal(result.ChangedFields, fields)) {
					t.Fatal("confirmed role metadata differs from native effect")
				}
				assertRoleKeys(t, reader, actor, target.ID, []int64{f.group.ID}, []int64{f.grants[0].ID})
			} else {
				assertCatalogRows(t, before, after, actor)
				assertRoleKeys(t, reader, actor, target.ID, nil, nil)
			}
			accountSessionRecord(t, store, record, !committed)
		})
	}
}
