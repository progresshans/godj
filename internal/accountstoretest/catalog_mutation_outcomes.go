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
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

type catalogOperationFixture struct {
	*catalogFixture
	group                   models.Group
	permission, view, extra models.Permission
	member                  models.User
	note                    hostmodels.AccessNote
}

// Seed real catalog owners, host relations, actor/member/anonymous sessions and
// an unrelated protected graph. Every outcome observes the entire native state.
func provisionCatalogOperation(t *testing.T, backend Backend) *catalogOperationFixture {
	t.Helper()
	f := &catalogOperationFixture{catalogFixture: provisionCatalogHost(t, backend)}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.view, err = models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	check(err)
	f.extra, err = models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.extra", "Extra ticket"))
	check(err)
	permission, err := f.catalog.Permissions().Create(t.Context(), f.credential.Principal(), catalogPermissionInput(t, map[string]query.Value{"code": query.String("helpdesk.ticket.change"), "name": query.String("Change ticket")}))
	check(err)
	f.permission = catalogRow(t, models.PermissionObjects.Using(backend).Filter(models.PermissionFields.ID.Exact(permission.ID)).OrderBy(models.PermissionFields.ID.Asc()))
	group, err := f.catalog.Groups().Create(t.Context(), f.credential.Principal(), catalogGroupInput(t, map[string]query.Value{"name": query.String("Catalog target")}, map[string][]int64{"permissions": {f.view.ID, f.permission.ID}}))
	check(err)
	f.group = catalogRow(t, models.GroupObjects.Using(backend).Filter(models.GroupFields.ID.Exact(group.ID)).OrderBy(models.GroupFields.ID.Asc()))
	f.member, err = models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("catalog-member", "member", f.account.user.EncodedPassword, joined))
	check(err)
	_, err = models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.member.ID, f.group.ID))
	check(err)
	_, err = models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.member.ID, f.permission.ID))
	check(err)
	f.note, err = hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("catalog host", f.permission.ID).WithGroupID(f.group.ID))
	check(err)
	_, err = hostmodels.AccessGuardObjects.Create(t.Context(), backend, hostmodels.NewAccessGuardCreate().WithGroupID(f.account.group.ID).WithPermissionID(f.account.grants[0].ID))
	check(err)
	store, err := hostproject.NewAccountSessionStore(backend, sessiondb.Config{})
	check(err)
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return joined }})
	check(err)
	credential, found, err := f.account.store.CredentialByID(t.Context(), f.member.PrincipalID)
	if err != nil || !found {
		t.Fatal("catalog fixture lost its member credential", err)
	}
	for _, marker := range []string{"first", "second", "third"} {
		passwordSession(t, manager, credential, marker)
	}
	passwordSession(t, manager, f.credential, "actor")
	_, err = manager.Create(t.Context(), map[string]string{"anonymous": "preserved"})
	check(err)
	return f
}

type catalogMutationProbe struct {
	db.RelationSession
	conflict       db.ConflictInserter
	mode, table    string
	fault          error
	faults, audits int
}

func (probe *catalogMutationProbe) ValidateSession(ctx context.Context) error {
	return probe.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}

func (probe *catalogMutationProbe) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if probe.mode == "swallowed_query" {
		probe.faults++
		return nil, probe.fault
	}
	return probe.RelationSession.Query(ctx, plan)
}

func (probe *catalogMutationProbe) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := probe.RelationSession.Insert(ctx, plan)
	if err == nil && plan.Table() == "godj_account_audit_event" {
		probe.audits++
	}
	if err == nil && (probe.mode == "audit_failure" && plan.Table() == "godj_account_audit_event" ||
		probe.mode == "write_failure" && plan.Table() == probe.table ||
		probe.mode == "collection_failure" && plan.Table() == "godj_identity_group_permissions") {
		probe.faults++
		return 0, probe.fault
	}
	return key, err
}

func (probe *catalogMutationProbe) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	count, err := probe.RelationSession.Update(ctx, plan)
	if err == nil && count > 0 && probe.mode == "write_failure" && plan.Table() == probe.table {
		probe.faults++
		return 0, probe.fault
	}
	return count, err
}

func (probe *catalogMutationProbe) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	result, err := probe.conflict.InsertOnConflict(ctx, plan)
	if err == nil && result.Inserted && probe.mode == "collection_failure" && plan.Table() == "godj_identity_group_permissions" {
		probe.faults++
		return db.ConflictInsertResult{}, probe.fault
	}
	return result, err
}

func runCatalogMutationOutcomes(add registerCase, open Open) {
	add("catalog_mutation_outcomes", func(t *testing.T) {
		for _, operation := range []string{"create_group", "update_group", "delete_group", "create_permission", "update_permission", "delete_permission"} {
			t.Run(operation, func(t *testing.T) {
				modes := []string{"skipped", "nil_session", "root_session", "repeated", "swallowed_query", "audit_failure", "write_failure", "cancel_confirmed_rollback", "unknown_rollback", "unknown_commit", "post_commit_cancel"}
				if operation == "create_group" || operation == "update_group" {
					modes = append(modes, "collection_failure")
				}
				for _, mode := range modes {
					t.Run(mode, func(t *testing.T) {
						backend, reopen := open(t)
						catalogMutationOutcome(t, backend, reopen, operation, mode)
					})
				}
			})
		}
	})
}

func callSelectedCatalogMutation(t *testing.T, ctx context.Context, catalog *accountmodel.Catalog[models.Group, models.Permission], f *catalogOperationFixture, operation string) (any, error) {
	t.Helper()
	actor := f.credential.Principal()
	switch operation {
	case "create_group":
		return catalog.Groups().Create(ctx, actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Created group")}, map[string][]int64{"permissions": {f.view.ID, f.extra.ID}}))
	case "update_group":
		return catalog.Groups().Update(ctx, actor, f.group.ID, f.group.Revision, catalogGroupInput(t, map[string]query.Value{"name": query.String("Changed group")}, map[string][]int64{"permissions": {f.view.ID, f.extra.ID}}))
	case "delete_group":
		return catalog.Groups().Delete(ctx, actor, f.group.ID, f.group.Revision)
	case "create_permission":
		return catalog.Permissions().Create(ctx, actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("helpdesk.ticket.created"), "name": query.String("Created permission")}))
	case "update_permission":
		return catalog.Permissions().Update(ctx, actor, f.permission.ID, f.permission.Revision, catalogPermissionInput(t, map[string]query.Value{"code": query.String("helpdesk.ticket.renamed"), "name": query.String("Changed permission")}))
	case "delete_permission":
		return catalog.Permissions().Delete(ctx, actor, f.permission.ID, f.permission.Revision)
	default:
		t.Fatal("unknown catalog mutation fixture", operation)
		return nil, nil
	}
}

func catalogMutationOutcome(t *testing.T, backend Backend, reopen func(*testing.T) Backend, operation, mode string) {
	f := provisionCatalogOperation(t, backend)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault := errors.New("private selected catalog mutation failure")
	owner := &profileFaultBackend{Backend: backend, mode: mode, fault: fault, cancel: cancel}
	if mode == "audit_failure" || mode == "write_failure" {
		owner.mode = "catalog_" + mode
	} else if mode == "swallowed_query" {
		owner.mode = "swallowed_rejection"
	}
	probe := &catalogMutationProbe{mode: mode, fault: fault, table: "godj_identity_permission"}
	if strings.HasSuffix(operation, "_group") {
		probe.table = "godj_identity_group"
	}
	if strings.HasPrefix(operation, "delete_") {
		// Exercise the direct account owner's revision write, not just the
		// target row. Host and through effects must roll back with that owner.
		probe.table = "godj_identity_user"
	}
	owner.decorate = func(scope db.Session) db.Session {
		var valid bool
		probe.RelationSession, valid = scope.(db.RelationSession)
		if !valid {
			t.Fatal("catalog outcome lost its native relation session")
		}
		probe.conflict, valid = scope.(db.ConflictInserter)
		if !valid {
			t.Fatal("catalog outcome lost its native conflict insert")
		}
		return probe
	}
	store, err := hostproject.NewAccountSessionStore(owner, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := hostproject.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	before := catalogHostSnapshot(t, reopen(t))
	result, err := callSelectedCatalogMutation(t, ctx, catalog, f, operation)
	if owner.calls != 1 {
		t.Fatal("catalog outcome retried its native owner", owner.calls)
	}
	if mode == "swallowed_query" || mode == "audit_failure" || mode == "write_failure" || mode == "collection_failure" {
		if probe.faults != 1 || !errors.Is(err, fault) {
			t.Fatal("catalog outcome did not reach the intended native failure exactly once", probe.faults, err)
		}
	}
	if mode == "repeated" || mode == "audit_failure" || mode == "cancel_confirmed_rollback" || mode == "unknown_rollback" || mode == "unknown_commit" || mode == "post_commit_cancel" {
		if probe.audits != 1 {
			t.Fatal("catalog outcome never reached its actual audit insert", probe.audits)
		}
	}
	if mode == "post_commit_cancel" {
		if err != nil || ctx.Err() == nil || owner.nativeError != nil {
			t.Fatal("late cancellation erased the confirmed native catalog commit", err)
		}
	} else {
		if err == nil || !reflect.ValueOf(result).IsZero() {
			t.Fatal("failed or uncertain catalog mutation published metadata", err)
		}
		code := accountmodel.CodePersistence
		if mode == "skipped" || mode == "repeated" || mode == "swallowed_query" || mode == "unknown_commit" || mode == "unknown_rollback" {
			code = accountmodel.CodeOutcomeUnknown
		}
		requireProfileCode(t, err, code)
		if _, rejected := validation.Rejected(err); rejected || strings.Contains(fmt.Sprintf("%+v", err), "private") {
			t.Fatal("catalog execution failure published diagnostics or its private cause", err)
		}
		if mode == "cancel_confirmed_rollback" {
			if ctx.Err() == nil || !errors.Is(err, context.Canceled) || owner.nativeError == nil || !errors.Is(err, owner.nativeError) ||
				errors.Is(owner.nativeError, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(owner.nativeError, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
				t.Fatal("catalog cancellation lost its independently acknowledged rollback", err)
			}
		}
	}
	if mode != "unknown_commit" && mode != "post_commit_cancel" {
		if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog failure retained partial account/catalog/through/host/session/domain/audit state")
		}
		return
	}
	expectedResult := assertCatalogCommittedOutcome(t, reopen(t), before, f, operation)
	if mode == "post_commit_cancel" && !reflect.DeepEqual(result, expectedResult) {
		t.Fatal("confirmed catalog commit returned incorrect mutation metadata")
	}
}

func assertCatalogCommittedOutcome(t *testing.T, backend Backend, expected catalogHostState, f *catalogOperationFixture, operation string) any {
	t.Helper()
	actual := catalogHostSnapshot(t, backend)
	event := catalogExpectedEvent{"permission", f.permission.ID, admin.ActionChange, []string{"code", "name"}}
	if strings.HasSuffix(operation, "_group") {
		event = catalogExpectedEvent{"group", f.group.ID, admin.ActionChange, []string{"name", "permissions"}}
	}
	revision := int64(2)
	switch operation {
	case "create_group":
		row := catalogRow(t, models.GroupObjects.Using(backend).Filter(models.GroupFields.Name.Exact("Created group")).OrderBy(models.GroupFields.ID.Asc()))
		if row.ID <= 0 || slices.ContainsFunc(expected.account.groups, func(old models.Group) bool { return old.ID == row.ID }) {
			t.Fatal("catalog creation did not allocate a new group")
		}
		created := models.NewGroupWithID(row.ID)
		created.Name, created.Revision = "Created group", 1
		expected.account.groups = append(expected.account.groups, created)
		expected.account.grants = catalogExpectedGrantAdditions(t, expected.account.grants, actual.account.grants, row.ID, []int64{f.view.ID, f.extra.ID})
		event.id, event.action, revision = row.ID, admin.ActionAdd, 1
	case "update_group":
		for index := range expected.account.groups {
			if expected.account.groups[index].ID == f.group.ID {
				expected.account.groups[index].Name, expected.account.groups[index].Revision = "Changed group", 2
			}
		}
		expected.account.grants = slices.DeleteFunc(expected.account.grants, func(row models.GroupPermissionsLink) bool {
			return row.SourceID == f.group.ID && row.TargetID == f.permission.ID
		})
		expected.account.grants = catalogExpectedGrantAdditions(t, expected.account.grants, actual.account.grants, f.group.ID, []int64{f.extra.ID})
	case "delete_group":
		expected.account.groups = slices.DeleteFunc(expected.account.groups, func(row models.Group) bool { return row.ID == f.group.ID })
		expected.account.grants = slices.DeleteFunc(expected.account.grants, func(row models.GroupPermissionsLink) bool { return row.SourceID == f.group.ID })
		expected.account.memberships = slices.DeleteFunc(expected.account.memberships, func(row models.UserGroupsLink) bool { return row.TargetID == f.group.ID })
		for index := range expected.notes {
			if expected.notes[index].ID == f.note.ID {
				expected.notes[index].GroupID = nil
			}
		}
	case "create_permission":
		row := catalogRow(t, models.PermissionObjects.Using(backend).Filter(models.PermissionFields.Code.Exact("helpdesk.ticket.created")).OrderBy(models.PermissionFields.ID.Asc()))
		if row.ID <= 0 || slices.ContainsFunc(expected.account.permissions, func(old models.Permission) bool { return old.ID == row.ID }) {
			t.Fatal("catalog creation did not allocate a new permission")
		}
		created := models.NewPermissionWithID(row.ID)
		created.Code, created.Name, created.Revision = "helpdesk.ticket.created", "Created permission", 1
		expected.account.permissions = append(expected.account.permissions, created)
		event.id, event.action, revision = row.ID, admin.ActionAdd, 1
	case "update_permission":
		for index := range expected.account.permissions {
			if expected.account.permissions[index].ID == f.permission.ID {
				row := &expected.account.permissions[index]
				row.Code, row.Name, row.Revision = "helpdesk.ticket.renamed", "Changed permission", 2
			}
		}
	case "delete_permission":
		expected.account.permissions = slices.DeleteFunc(expected.account.permissions, func(row models.Permission) bool { return row.ID == f.permission.ID })
		expected.account.grants = slices.DeleteFunc(expected.account.grants, func(row models.GroupPermissionsLink) bool { return row.TargetID == f.permission.ID })
		expected.account.direct = slices.DeleteFunc(expected.account.direct, func(row models.UserPermissionsLink) bool { return row.TargetID == f.permission.ID })
		for index := range expected.account.groups {
			if expected.account.groups[index].ID == f.group.ID {
				expected.account.groups[index].Revision++
			}
		}
		expected.notes = slices.DeleteFunc(expected.notes, func(row hostmodels.AccessNote) bool { return row.ID == f.note.ID })
	}
	if strings.HasPrefix(operation, "delete_") {
		event.action, event.fields = admin.ActionDelete, nil
		for index := range expected.account.users {
			if expected.account.users[index].ID == f.member.ID {
				expected.account.users[index].Revision++
			}
		}
	}
	assertCatalogRows(t, expected.account, actual.account, f.credential.Principal(), event)
	if !reflect.DeepEqual(expected.notes, actual.notes) || !reflect.DeepEqual(expected.guards, actual.guards) {
		t.Fatal("committed catalog outcome changed unexpected host state")
	}
	// Read effective grants after reconnecting through the selected source,
	// independently of the result (which is empty for unknown_commit).
	store, err := hostproject.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	grants := []auth.Permission{"helpdesk.ticket.change", "helpdesk.ticket.view"}
	switch operation {
	case "update_group":
		grants = []auth.Permission{"helpdesk.ticket.change", "helpdesk.ticket.extra", "helpdesk.ticket.view"}
	case "update_permission":
		grants = []auth.Permission{"helpdesk.ticket.renamed", "helpdesk.ticket.view"}
	case "delete_group":
		grants = []auth.Permission{"helpdesk.ticket.change"}
	case "delete_permission":
		grants = []auth.Permission{"helpdesk.ticket.view"}
	}
	credential, found, err := store.CredentialByID(t.Context(), f.member.PrincipalID)
	requireGrants(t, credential, found, err, grants...)
	if !reflect.DeepEqual(actual, catalogHostSnapshot(t, backend)) {
		t.Fatal("catalog outcome readback changed native state")
	}
	if strings.HasPrefix(operation, "delete_") {
		return accountmodel.CatalogDeletion{ID: event.id, Revision: 1, DeletedRows: 4}
	}
	return accountmodel.CatalogChange{ID: event.id, Revision: revision, ChangedFields: event.fields}
}
