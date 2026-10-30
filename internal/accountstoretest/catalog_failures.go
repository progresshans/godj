package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// Preserve the actual native relation session. An interface wrapper that
// erased it would fail before the write under test and miss rollback defects.
type catalogFaultSession struct {
	db.RelationSession
	conflict db.ConflictInserter
	mode     string
	fault    error
}

func (session *catalogFaultSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	inserted, err := session.conflict.InsertOnConflict(ctx, plan)
	if err == nil && session.mode == "collection_failure" && plan.Table() == "godj_identity_group_permissions" {
		return db.ConflictInsertResult{}, session.fault
	}
	return inserted, err
}

func (session *catalogFaultSession) ValidateSession(ctx context.Context) error {
	return session.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (session *catalogFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := session.RelationSession.Insert(ctx, plan)
	if err == nil && (session.mode == "audit_failure" && plan.Table() == "godj_account_audit_event" || session.mode == "collection_failure" && plan.Table() == "godj_identity_group_permissions") {
		return 0, session.fault
	}
	return key, err
}
func (session *catalogFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := session.RelationSession.Update(ctx, plan)
	if err == nil && session.mode == "write_failure" && plan.Table() == "godj_identity_group" {
		return 0, session.fault
	}
	return count, err
}

func catalogFaultOwner(t *testing.T, backend Backend, mode string, fault error, cancel context.CancelFunc) *profileFaultBackend {
	t.Helper()
	owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
	if mode == "write_failure" || mode == "audit_failure" || mode == "collection_failure" {
		owner.mode = "catalog_" + mode
		owner.decorate = func(scope db.Session) db.Session {
			relation, valid := scope.(db.RelationSession)
			if !valid {
				t.Fatal("native coordinated scope erased relation writes")
			}
			conflict, valid := scope.(db.ConflictInserter)
			if !valid {
				t.Fatal("native relation scope erased conflict inserts")
			}
			return &catalogFaultSession{RelationSession: relation, conflict: conflict, mode: mode, fault: fault}
		}
	}
	return owner
}

func catalogCommitFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	group, err := f.catalog.Groups().Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("outcome target")}, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"skipped", "nil_session", "root_session", "audit_failure", "write_failure", "repeated", "validator_rejection", "validator_failure", "swallowed_rejection", "cleanup_failure", "invalid_clock", "cancel_before_commit", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private catalog write failure")
			owner := catalogFaultOwner(t, backend, mode, fault, cancel)
			config := catalogConfig()
			if mode == "validator_rejection" || mode == "validator_failure" || mode == "swallowed_rejection" || mode == "cleanup_failure" {
				config.Group.Validate = func(_ context.Context, _ db.Queryer, current *models.Group, candidate models.Group) (validation.Errors, error) {
					if current == nil || candidate.Name != mode || current.ID != group.ID {
						t.Fatal("typed catalog validator received wrong models")
					}
					current.Name = "must remain detached"
					failures := validation.NewErrors(validation.New("name", "rejected"))
					if mode == "validator_failure" {
						return failures, fault
					}
					return failures, nil
				}
			}
			if mode == "invalid_clock" {
				config.Now = func() time.Time { return time.Time{} }
			}
			catalog, err := project.NewAccountCatalog(catalogSessions(t, owner), auth.PrincipalAuthorizer{}, config)
			if err != nil {
				t.Fatal(err)
			}
			before := catalogRow(t, models.GroupObjects.Using(backend).Filter(models.GroupFields.ID.Exact(group.ID)).OrderBy(models.GroupFields.ID.Asc()))
			history := catalogAudit(t, backend, actor, "group", group.ID)
			result, err := catalog.Groups().Update(ctx, actor, group.ID, before.Revision, catalogGroupInput(t, map[string]query.Value{"name": query.String(mode)}, nil))
			if owner.calls != 1 {
				t.Fatal("catalog mutation retried", owner.calls)
			}
			committed := mode == "unknown_commit" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil || result.Revision != before.Revision+1 {
					t.Fatal("late cancellation erased confirmed catalog commit", err)
				}
			} else {
				if err == nil || result.ID != 0 || result.Revision != 0 || result.ChangedFields != nil {
					t.Fatal("failed catalog mutation retained result", err)
				}
				if mode == "validator_rejection" {
					if failures, rejected := validation.Rejected(err); !rejected || failures.ByField("name").Empty() {
						t.Fatal("confirmed catalog validation rollback lost diagnostics", err)
					}
				} else {
					if _, rejected := validation.Rejected(err); rejected {
						t.Fatal("catalog execution failure became input rejection")
					}
					code := accountmodel.CodePersistence
					if mode == "skipped" || mode == "repeated" || mode == "swallowed_rejection" || mode == "unknown_commit" || mode == "unknown_rollback" {
						code = accountmodel.CodeOutcomeUnknown
					}
					if mode == "cancel_before_commit" || mode == "cancel_confirmed_rollback" {
						if !errors.Is(err, context.Canceled) || owner.nativeError == nil || !errors.Is(err, owner.nativeError) {
							t.Fatal("catalog cancellation lost native outcome", err)
						}
						uncertain := errors.Is(owner.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(owner.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown})
						if mode == "cancel_confirmed_rollback" && uncertain {
							t.Fatal("independent native owner did not acknowledge rollback")
						}
						if uncertain {
							code = accountmodel.CodeOutcomeUnknown
						}
					}
					requireProfileCode(t, err, code)
				}
				if strings.Contains(fmt.Sprintf("%+v", err), "private") {
					t.Fatal("catalog failure leaked private cause")
				}
				if (mode == "write_failure" || mode == "audit_failure" || mode == "validator_failure" || mode == "cleanup_failure") && !errors.Is(err, fault) {
					t.Fatal("catalog failure lost cause", err)
				}
			}
			after := catalogRow(t, models.GroupObjects.Using(reopen(t)).Filter(models.GroupFields.ID.Exact(group.ID)).OrderBy(models.GroupFields.ID.Asc()))
			audit := catalogAudit(t, reopen(t), actor, "group", group.ID)
			if committed {
				if after.Revision != before.Revision+1 || after.Name != mode || len(audit) != len(history)+1 {
					t.Fatal("durable catalog/audit disagree with confirmed outcome")
				}
			} else if !reflect.DeepEqual(after, before) || len(audit) != len(history) {
				t.Fatal("catalog rollback retained partial scalar/audit")
			}
		})
	}
}

func catalogRelationRollback(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	group, err := f.catalog.Groups().Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("atomic relation target")}, map[string][]int64{"permissions": {f.account.grants[0].ID}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.account.user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	before := profileRow(t, backend, f.account.user.ID)
	for _, operation := range []string{"create_audit", "collection_write", "collection_audit", "delete_audit"} {
		t.Run(operation, func(t *testing.T) {
			fault, mode := errors.New("private catalog atomic relation failure"), "audit_failure"
			if operation == "collection_write" {
				mode = "collection_failure"
			}
			owner := catalogFaultOwner(t, backend, mode, fault, func() {})
			catalog, err := project.NewAccountCatalog(catalogSessions(t, owner), auth.PrincipalAuthorizer{}, catalogConfig())
			if err != nil {
				t.Fatal(err)
			}
			input := catalogGroupInput(t, map[string]query.Value{"name": query.String("must roll back")}, map[string][]int64{"permissions": {f.account.grants[1].ID}})
			if operation == "delete_audit" {
				result, failure := catalog.Groups().Delete(t.Context(), actor, group.ID, 1)
				err = failure
				if result != (accountmodel.CatalogDeletion{}) {
					t.Fatal("failed relation delete retained result")
				}
			} else {
				var result accountmodel.CatalogChange
				if operation == "create_audit" {
					result, err = catalog.Groups().Create(t.Context(), actor, input)
				} else {
					result, err = catalog.Groups().Update(t.Context(), actor, group.ID, 1, input)
				}
				if result.ID != 0 || result.Revision != 0 || result.ChangedFields != nil {
					t.Fatal("failed relation mutation retained result")
				}
			}
			if !errors.Is(err, fault) || owner.calls != 1 {
				t.Fatal("relation failure did not reach the intended write", err)
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			current, err := f.catalog.Groups().Get(t.Context(), actor, group.ID)
			if err != nil || current.Revision() != 1 {
				t.Fatal("failed relation operation changed target", err)
			}
			if keys, loaded := current.View().Collection("permissions"); !loaded || len(keys) != 1 || keys[0] != f.account.grants[0].ID {
				t.Fatal("failed relation operation retained partial links")
			}
			if value, _ := current.View().Get("name"); !value.Equal(query.String("atomic relation target")) {
				t.Fatal("failed relation operation changed scalar")
			}
			if actual := profileRow(t, reopen(t), f.account.user.ID); !reflect.DeepEqual(actual, before) {
				t.Fatal("failed relation delete advanced account owner")
			}
			if count, err := models.GroupObjects.Using(reopen(t)).Count(t.Context()); err != nil || count != 2 {
				t.Fatal("failed create retained catalog row", err)
			}
			if len(catalogAudit(t, reopen(t), actor, "group", group.ID)) != 1 {
				t.Fatal("failed relation operation appended audit")
			}
		})
	}
}
