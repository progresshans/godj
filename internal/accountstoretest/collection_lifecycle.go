package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func runCollectionLifecycle(t *testing.T, open Open, storedKey StoredKeyFixture) {
	t.Helper()
	for _, kind := range []string{"account", "catalog"} {
		t.Run("collection_"+kind+"_lifecycle", func(t *testing.T) {
			modes := []string{
				"commit", "zero_key", "negative_key", "scalar_only", "additional_denial", "validator_rejection",
				"protected", "root_deleted", "corrupt_revision", "query_failure", "close_failure",
				"late_revision_failure", "late_session_failure", "late_root_write", "late_insert_failure", "wrong_null_count", "final_read_failure",
				"skipped", "repeated", "unknown_rollback", "unknown_commit", "cleanup_failure", "post_commit_cancel", "cancel_confirmed_rollback",
			}
			if kind == "account" {
				modes = append(modes, "self", "missing_effect_policy", "final_union_bound")
			} else {
				modes = append(modes, "effective_limit")
			}
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					backend, reopen := open(t)
					migrateDeletion(t, backend)
					collectionLifecycle(t, backend, reopen, storedKey, kind, mode)
				})
			}
		})
	}
	t.Run("collection_account_creation", func(t *testing.T) {
		for _, mode := range []string{"effects_allowed", "new_authority_denied", "missing_effect_policy", "root_deleted", "validator_rollback", "unknown_rollback", "unknown_commit", "cleanup_failure"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				migrateDeletion(t, backend)
				collectionAccountCreation(t, backend, reopen, mode)
			})
		}
	})
	t.Run("collection_catalog_creation", func(t *testing.T) {
		for _, mode := range []string{"effects_allowed", "new_authority_denied", "validator_rollback"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				migrateDeletion(t, backend)
				collectionCatalogCreation(t, backend, reopen, mode)
			})
		}
	})
}

func lifecycleCatalogConfig() accountmodel.CatalogConfig[access.Team, access.Grant] {
	return accountmodel.CatalogConfig[access.Team, access.Grant]{
		Group:              accountmodel.CatalogModelConfig[access.Team]{ReadFields: []string{"title", "grants"}, WriteFields: []string{"title", "grants"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group"}},
		Permission:         accountmodel.CatalogModelConfig[access.Grant]{ReadFields: []string{"key", "title"}, WriteFields: []string{"key", "title"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission"}},
		AccountPermissions: deletionConfig().Account, Now: func() time.Time { return joined },
	}
}

func lifecycleInput[M any](t *testing.T, objects orm.Manager[M], values map[string]query.Value, collections map[string][]int64) orm.ModelInput[M] {
	t.Helper()
	input, err := objects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.WithCollections(collections)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func collectionLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend, storedKey StoredKeyFixture, kind, mode string) {
	t.Helper()
	f := provisionDeletion(t, backend)
	tree := collectionLifecycleFixture(t, backend, f, storedKey, kind, mode)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault := errors.New("private composed account lifecycle failure")
	owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
	var observed *lifecycleFaultScope
	owner.decorate = func(session db.Session) db.Session {
		scope, ok := session.(db.RelationSession)
		if !ok {
			t.Fatal("lifecycle lost native relation session")
		}
		observed = &lifecycleFaultScope{RelationSession: scope, kind: kind, mode: mode, fault: fault, rootID: tree.member.ID, rootTable: "people_member"}
		if kind == "catalog" {
			observed.rootID, observed.rootTable = tree.group.ID, "access_team"
		}
		return observed
	}
	store, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var overlay auth.Authorizer = auth.PrincipalAuthorizer{}
	if mode == "additional_denial" {
		overlay = overlayFunc(func(_ context.Context, _ auth.Principal, permission auth.Permission) (bool, error) {
			return permission != "effects.extra", nil
		})
	}
	before := deletionSnapshot(t, reopen(t))
	var id, revision int64
	var fields []string
	var revoked, validations int
	wantID, wantRevoked := tree.member.ID, len(tree.rootSessions)+len(tree.victimSessions)
	wantFields := []string{"enabled", "grants", "nullable_membership", "region", "teams"}
	if kind == "catalog" {
		wantID, wantRevoked = tree.group.ID, len(tree.victimSessions)
		wantFields = []string{"grants", "nullable_membership", "reviewer", "title"}
	}
	if mode == "scalar_only" {
		wantRevoked = 0
		wantFields = []string{"region"}
		if kind == "catalog" {
			wantFields = []string{"title"}
		}
	}
	if kind == "account" {
		policy := deletionConfig()
		config := accountmodel.AccountEditConfig[people.Member]{Fields: []string{"region", "enabled", "teams", "grants"}, ChangePermission: "account.change", RelatedPermissions: accountmodel.RelatedRowPermissions{Account: policy.Account, Group: policy.Group, Permission: policy.Permission}, Now: policy.Now}
		if mode == "missing_effect_policy" {
			config.RelatedPermissions = accountmodel.RelatedRowPermissions{}
		}
		if mode == "additional_denial" {
			config.RelatedPermissions.Account.Additional = []auth.Permission{"effects.extra"}
		}
		config.Validate = func(_ context.Context, _ db.Queryer, current, candidate people.Member, selection accountmodel.RoleMemberships) (validation.Errors, error) {
			validations++
			if !reflect.DeepEqual(current, tree.member) || candidate.ID != tree.member.ID || candidate.Version != 2 || candidate.Region != "JP" || candidate.Subject != tree.member.Subject || candidate.Secret != tree.member.Secret {
				t.Fatal("account validator did not receive detached current and final typed state")
			}
			groups, direct := []int64{tree.group.ID}, tree.desired
			if mode == "scalar_only" {
				groups, direct = []int64{tree.oldGroup.ID, tree.group.ID}, []int64{tree.deletedGrant.ID, tree.grant.ID}
				slices.Sort(groups)
				slices.Sort(direct)
				if candidate.NullableMembershipID == nil || !candidate.Enabled {
					t.Fatal("scalar-only validation changed unrelated fields")
				}
			} else if candidate.NullableMembershipID != nil || candidate.Enabled {
				t.Fatal("account validator missed collected NULL or deactivation")
			}
			if !slices.Equal(selection.Groups(), groups) || !slices.Equal(selection.Permissions(), direct) {
				t.Fatal("account validator saw intermediate memberships")
			}
			if mode == "validator_rejection" {
				return validation.NewErrors(validation.New("region", "rejected")), nil
			}
			return validation.Errors{}, nil
		}
		editor, failure := project.NewAccountEditor(store, overlay, config)
		if failure != nil {
			t.Fatal(failure)
		}
		values := map[string]query.Value{"region": query.String("JP"), "enabled": query.Boolean(false)}
		collections := map[string][]int64{"teams": {tree.group.ID}, "grants": tree.desired}
		if mode == "scalar_only" {
			delete(values, "enabled")
			collections = nil
		}
		result, failure := editor.Update(ctx, f.actor, tree.member.ID, 1, lifecycleInput(t, people.MemberObjects, values, collections))
		err = failure
		id, revision, fields, revoked = result.ID, result.Revision, result.ChangedFields, result.RevokedSessions
	} else {
		config := lifecycleCatalogConfig()
		if mode == "additional_denial" {
			config.AccountPermissions.Additional = []auth.Permission{"effects.extra"}
		}
		config.Group.Validate = func(_ context.Context, _ db.Queryer, current *access.Team, candidate access.Team) (validation.Errors, error) {
			validations++
			if current == nil || !reflect.DeepEqual(*current, tree.group) || candidate.ID != tree.group.ID || candidate.Version != 2 || candidate.Title != "Updated group" {
				t.Fatal("catalog validator did not receive detached current and final typed state")
			}
			if mode == "scalar_only" {
				if candidate.NullableMembershipID == nil || candidate.ReviewerID == nil {
					t.Fatal("scalar-only catalog candidate changed relations")
				}
			} else if candidate.NullableMembershipID != nil || candidate.ReviewerID != nil {
				t.Fatal("catalog validator missed collected NULL fields")
			}
			if mode == "validator_rejection" {
				return validation.NewErrors(validation.New("title", "rejected")), nil
			}
			return validation.Errors{}, nil
		}
		catalog, failure := project.NewAccountCatalog(store, overlay, config)
		if failure != nil {
			t.Fatal(failure)
		}
		collections := map[string][]int64{"grants": tree.desired}
		if mode == "scalar_only" {
			collections = nil
		}
		result, failure := catalog.Groups().Update(ctx, f.actor, tree.group.ID, 1, lifecycleInput(t, access.TeamObjects, map[string]query.Value{"title": query.String("Updated group")}, collections))
		err = failure
		id, revision, fields, revoked = result.ID, result.Revision, result.ChangedFields, result.RevokedSessions
	}
	if owner.calls != 1 {
		t.Fatal("composed lifecycle retried or skipped the native owner", owner.calls)
	}
	success := slices.Contains([]string{"commit", "zero_key", "negative_key", "scalar_only", "self", "final_union_bound", "post_commit_cancel"}, mode)
	committed := success || mode == "unknown_commit" || mode == "cleanup_failure"
	if success {
		slices.Sort(fields)
		if err != nil || id != wantID || revision != 2 || revoked != wantRevoked || !slices.Equal(fields, wantFields) || validations != 1 {
			t.Fatal("composed lifecycle result differs", kind, mode, err, validations)
		}
	} else {
		if err == nil || id != 0 || revision != 0 || fields != nil || revoked != 0 {
			t.Fatal("failed composed lifecycle published a result", kind, mode, err)
		}
		switch mode {
		case "validator_rejection", "effective_limit":
			diagnostics, rejected := validation.Rejected(err)
			field := "grants"
			if mode == "validator_rejection" {
				field = "title"
				if kind == "account" {
					field = "region"
				}
			}
			if !rejected || diagnostics.ByField(validation.Field(field)).Empty() {
				t.Fatal("composed candidate lost its validation failure", err)
			}
		case "protected":
			requireProtectedDeletionInput(t, err, 1)
		default:
			code := accountmodel.CodePersistence
			if mode == "root_deleted" {
				code = accountmodel.CodeConflict
			}
			if mode == "missing_effect_policy" || mode == "additional_denial" {
				code = accountmodel.CodePermission
			}
			if mode == "skipped" || mode == "repeated" || mode == "unknown_commit" || mode == "unknown_rollback" {
				code = accountmodel.CodeOutcomeUnknown
			}
			if !errors.Is(err, &accountmodel.Error{Code: code}) {
				t.Fatal("composed lifecycle lost failure classification", kind, mode, err)
			}
		}
		if mode == "validator_rejection" && validations != 1 {
			t.Fatal("final candidate validator was omitted or repeated")
		}
	}
	if committed {
		if mode == "scalar_only" {
			assertLifecycleScalarOnly(t, reopen(t), f, tree, before, kind)
		} else {
			assertCollectionLifecycle(t, reopen(t), f, tree, kind)
		}
	} else {
		requireDeletionUnchanged(t, reopen(t), before)
	}
	if observed != nil {
		if observed.ValidateSession(t.Context()) == nil {
			t.Fatal("composed lifecycle retained its transaction")
		}
		observed.assertFault(t, err)
	}
}

func assertLifecycleScalarOnly(t *testing.T, backend Backend, f *deletionFixture, tree collectionLifecycleTree, before map[string]any, kind string) {
	t.Helper()
	after := deletionSnapshot(t, backend)
	table, model, id, field := "member", "people.member", tree.member.ID, "region"
	if kind == "catalog" {
		table, model, id, field = "team", "access.team", tree.group.ID, "title"
	}
	for name, rows := range before {
		if name != table && name != "audit" && !reflect.DeepEqual(rows, after[name]) {
			t.Fatal("scalar-only edit changed another table", name)
		}
	}
	if kind == "account" {
		want := slices.Clone(before[table].([]people.Member))
		for index := range want {
			if want[index].ID == id {
				want[index].Region, want[index].Version = "JP", 2
			}
		}
		if !reflect.DeepEqual(want, after[table]) {
			t.Fatal("scalar-only account edit changed another row or field")
		}
	} else {
		want := slices.Clone(before[table].([]access.Team))
		for index := range want {
			if want[index].ID == id {
				want[index].Title, want[index].Version = "Updated group", 2
			}
		}
		if !reflect.DeepEqual(want, after[table]) {
			t.Fatal("scalar-only catalog edit changed another row or field")
		}
	}
	events := deletionHistory(t, backend, f.actor, model, id)
	if len(events) != 1 || events[0].Action() != admin.ActionChange || !slices.Equal(events[0].ChangedFields(), []string{field}) || reflect.ValueOf(after["audit"]).Len() != 1 {
		t.Fatal("scalar-only edit audit differs")
	}
}

type lifecycleFaultScope struct {
	db.RelationSession
	kind, mode, rootTable                                                                    string
	rootID                                                                                   int64
	fault                                                                                    error
	updates, inserts, deletes, nulls, rootWrites, revisions, sessions, closes, failedQueries int
}

func (scope *lifecycleFaultScope) ValidateSession(ctx context.Context) error {
	return scope.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *lifecycleFaultScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := scope.RelationSession.Query(ctx, plan)
	if err != nil {
		return rows, err
	}
	fail := plan.Table() == "links_membership" && len(plan.SourceFields()) == 3 && (scope.mode == "query_failure" || scope.mode == "close_failure")
	fail = fail || scope.mode == "final_read_failure" && scope.rootWrites != 0 && plan.Table() == scope.rootTable
	if !fail {
		return rows, nil
	}
	scope.failedQueries++
	wrapped := &lifecycleFaultRows{Rows: rows, scope: scope}
	if scope.mode == "close_failure" {
		return wrapped, nil
	}
	return wrapped, scope.fault
}

type lifecycleFaultRows struct {
	db.Rows
	scope *lifecycleFaultScope
}

func (rows *lifecycleFaultRows) Close() error {
	rows.scope.closes++
	err := rows.Rows.Close()
	if rows.scope.mode == "close_failure" {
		return errors.Join(err, rows.scope.fault)
	}
	return err
}
func (scope *lifecycleFaultScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := scope.RelationSession.Update(ctx, plan)
	if err != nil {
		return count, err
	}
	scope.updates += int(count)
	key, valid := plan.KeyValue().Integer()
	if plan.Table() == scope.rootTable && valid && key == scope.rootID {
		scope.rootWrites += int(count)
		if scope.mode == "late_root_write" {
			return count, scope.fault
		}
	} else if plan.Table() == "people_member" {
		scope.revisions += int(count)
		if scope.mode == "late_revision_failure" && scope.revisions == 257 {
			return count, scope.fault
		}
	}
	return count, nil
}
func (scope *lifecycleFaultScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.RelationSession.Insert(ctx, plan)
	if err != nil {
		return key, err
	}
	scope.inserts++
	if scope.mode == "late_insert_failure" && (plan.Table() == "links_membership" || plan.Table() == "links_grant_link") {
		return key, scope.fault
	}
	return key, nil
}
func (scope *lifecycleFaultScope) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	inserted, err := scope.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, plan)
	if inserted {
		scope.inserts++
	}
	return inserted, err
}
func (scope *lifecycleFaultScope) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	count, err := scope.RelationSession.Delete(ctx, plan)
	if err != nil {
		return count, err
	}
	scope.deletes += int(count)
	if plan.Table() == "godj_sessions_session" {
		scope.sessions += int(count)
		threshold := 257
		if scope.kind == "account" {
			threshold += 2
		}
		if scope.mode == "late_session_failure" && scope.sessions == threshold {
			return count, scope.fault
		}
	}
	return count, nil
}
func (scope *lifecycleFaultScope) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	count, err := scope.RelationSession.RelationSetNull(ctx, plan)
	if err != nil {
		return count, err
	}
	scope.nulls += int(count)
	if scope.mode == "wrong_null_count" {
		return count + 1, nil
	}
	return count, nil
}
func (scope *lifecycleFaultScope) assertFault(t *testing.T, failure error) {
	t.Helper()
	switch scope.mode {
	case "query_failure", "close_failure", "final_read_failure":
		if scope.failedQueries != 1 || scope.closes != 1 || !errors.Is(failure, scope.fault) {
			t.Fatal("failed lifecycle query lost its cause or rowset cleanup")
		}
		if scope.mode == "final_read_failure" {
			if scope.rootWrites != 1 {
				t.Fatal("final read failed before the root write")
			}
		} else if scope.updates+scope.inserts+scope.deletes+scope.nulls != 0 {
			t.Fatal("failed preparation began lifecycle writes")
		}
	case "late_revision_failure":
		if scope.revisions != 257 || !errors.Is(failure, scope.fault) {
			t.Fatal("revision failure did not reach a later owner batch")
		}
	case "late_session_failure":
		threshold := 257
		if scope.kind == "account" {
			threshold += 2
		}
		if scope.sessions != threshold || !errors.Is(failure, scope.fault) {
			t.Fatal("session failure did not reach a later session batch")
		}
	case "late_root_write":
		if scope.rootWrites != 1 || scope.deletes == 0 || scope.nulls == 0 || scope.inserts == 0 || !errors.Is(failure, scope.fault) {
			t.Fatal("root failure did not follow the collected graph and audit writes")
		}
	case "late_insert_failure":
		if scope.inserts < 2 || scope.deletes == 0 || scope.nulls == 0 || !errors.Is(failure, scope.fault) {
			t.Fatal("insertion failure did not follow collected effects")
		}
	case "wrong_null_count":
		if scope.nulls == 0 {
			t.Fatal("NULL count refusal did not follow an actual native mutation")
		}
	case "missing_effect_policy", "additional_denial", "validator_rejection", "protected", "root_deleted", "corrupt_revision", "effective_limit":
		if scope.updates+scope.inserts+scope.deletes+scope.nulls != 0 {
			t.Fatal("invalid composed state performed DML")
		}
	}
}
