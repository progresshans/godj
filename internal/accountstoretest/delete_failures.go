package accountstoretest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type deletionFaultScope struct {
	db.RelationSession
	mode            string
	fault           error
	writes          map[string]int64
	closed, queried int
}

func (scope *deletionFaultScope) ValidateSession(ctx context.Context) error {
	return scope.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *deletionFaultScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := scope.RelationSession.Query(ctx, plan)
	if err == nil && plan.Table() == "links_membership" && len(plan.SourceFields()) == 3 && (scope.mode == "query_failure" || scope.mode == "close_failure") {
		scope.queried++
		wrapped := &deletionFaultRows{Rows: rows, scope: scope}
		if scope.mode == "query_failure" {
			return wrapped, scope.fault
		}
		return wrapped, nil
	}
	return rows, err
}

type deletionFaultRows struct {
	db.Rows
	scope *deletionFaultScope
}

func (rows *deletionFaultRows) Close() error {
	rows.scope.closed++
	err := rows.Rows.Close()
	if rows.scope.mode == "close_failure" {
		return errors.Join(err, rows.scope.fault)
	}
	return err
}
func (scope *deletionFaultScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := scope.RelationSession.Update(ctx, plan)
	if err != nil {
		return count, err
	}
	scope.writes["revision"] += count
	if scope.mode == "revision_failure" {
		return count, scope.fault
	}
	return count, nil
}
func (scope *deletionFaultScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.RelationSession.Insert(ctx, plan)
	if err != nil {
		return key, err
	}
	scope.writes["audit"]++
	if scope.mode == "audit_failure" {
		return key, scope.fault
	}
	return key, nil
}
func (scope *deletionFaultScope) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	count, err := scope.RelationSession.RelationSetNull(ctx, plan)
	if err != nil {
		return count, err
	}
	scope.writes["null"] += count
	if scope.mode == "null_failure" {
		return count, scope.fault
	}
	if scope.mode == "wrong_null_count" {
		return 0, nil
	}
	return count, nil
}
func (scope *deletionFaultScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.RelationSession.Delete(ctx, plan)
	if err != nil {
		return count, err
	}
	if plan.Table() == "godj_sessions_session" {
		scope.writes["session"] += count
		if scope.mode == "session_failure" || scope.mode == "late_session_failure" && scope.writes["session"] == 257 {
			return count, scope.fault
		}
	} else {
		scope.writes["delete"] += count
		if scope.mode == "late_delete_failure" && plan.Table() == "people_member" {
			return count, scope.fault
		}
	}
	return count, nil
}

func accountDeletionFailures(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionDeletion(t, backend)
	for _, mode := range []string{
		"query_failure", "close_failure", "revision_failure", "session_failure", "late_session_failure", "audit_failure", "null_failure", "wrong_null_count", "late_delete_failure",
		"skipped", "nil_session", "root_session", "repeated", "invalid_clock", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "cleanup_failure", "post_commit_cancel",
	} {
		t.Run(mode, func(t *testing.T) {
			tree := deletionTreeFixture(t, backend, f, mode)
			if mode == "late_session_failure" {
				for len(tree.removed) < 259 {
					tree.removed = append(tree.removed, deletionSession(t, f.manager, tree.root.Subject))
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private collected deletion storage failure")
			owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
			var observed *deletionFaultScope
			if strings.HasSuffix(mode, "_failure") && mode != "cleanup_failure" || mode == "wrong_null_count" {
				owner.mode = "delete_" + mode
				owner.decorate = func(scope db.Session) db.Session {
					relation, ok := scope.(db.RelationSession)
					if !ok {
						t.Fatal("deletion lost native relation session")
					}
					observed = &deletionFaultScope{RelationSession: relation, mode: mode, fault: fault, writes: make(map[string]int64)}
					return observed
				}
			}
			store, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
			if err != nil {
				t.Fatal(err)
			}
			config := deletionConfig()
			if mode == "invalid_clock" {
				config.Now = func() time.Time { return time.Time{} }
			}
			deleter, err := project.NewAccountDeleter(store, auth.PrincipalAuthorizer{}, config)
			if err != nil {
				t.Fatal(err)
			}
			before := deletionSnapshot(t, reopen(t))
			result, err := deleter.Delete(ctx, f.actor, tree.root.ID, 1)
			if owner.calls != 1 {
				t.Fatal("deletion retried or skipped native owner", owner.calls)
			}
			committed := mode == "unknown_commit" || mode == "cleanup_failure" || mode == "post_commit_cancel"
			if mode == "post_commit_cancel" {
				if err != nil {
					t.Fatal("late cancellation erased confirmed deletion", err)
				}
				assertDeletionTree(t, reopen(t), f, tree, before, result)
			} else {
				want := accountmodel.CodePersistence
				if mode == "skipped" || mode == "repeated" || mode == "unknown_commit" || mode == "unknown_rollback" {
					want = accountmodel.CodeOutcomeUnknown
				}
				if !errors.Is(err, &accountmodel.Error{Code: want}) || result != (accountmodel.AccountDeletion{}) {
					t.Fatal("failed deletion published a result or lost failure classification", mode, err)
				}
				if observed != nil && mode != "wrong_null_count" && !errors.Is(err, fault) {
					t.Fatal("deletion lost storage cause", mode, err)
				}
			}
			if committed {
				if mode != "post_commit_cancel" {
					after := deletionSnapshot(t, reopen(t))
					// The public result stays empty; this independently observed
					// expectation only checks that the native commit really ran.
					expected := accountmodel.AccountDeletion{ID: tree.root.ID, Revision: 1, DeletedRows: deletionModelCount(before) - deletionModelCount(after), RevokedSessions: len(tree.removed)}
					assertDeletionTree(t, reopen(t), f, tree, before, expected)
				}
			} else {
				requireDeletionUnchanged(t, reopen(t), before)
			}
			if observed != nil {
				if err := observed.ValidateSession(t.Context()); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
					t.Fatal("deletion retained a live borrowed session", err)
				}
				if mode == "query_failure" || mode == "close_failure" {
					if observed.queried != 1 || observed.closed != 1 || len(observed.writes) != 0 {
						t.Fatal("failed membership collection leaked rows or began writes", observed.queried, observed.closed)
					}
				} else {
					kind := map[string]string{"revision_failure": "revision", "session_failure": "session", "late_session_failure": "session", "audit_failure": "audit", "null_failure": "null", "wrong_null_count": "null", "late_delete_failure": "delete"}[mode]
					if kind == "" || observed.writes[kind] == 0 {
						t.Fatal("failure did not follow its actual native write", mode)
					}
					if mode == "late_session_failure" && observed.writes[kind] != 257 {
						t.Fatal("session failure missed later native batch", observed.writes[kind])
					}
				}
			}
		})
	}
}
