package identitytest

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/admin"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	identityproject "github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/systemstate"
)

//go:embed testdata/catalog-django61-*.json
var catalogReferences embed.FS

func catalogRelations(t *testing.T) identityproject.Relations {
	t.Helper()
	relations, err := identityproject.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	return relations
}

func assertCatalogReference(t *testing.T, key string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var actual any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		data, err := catalogReferences.ReadFile("testdata/catalog-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django       string
			Observations map[string]any
		}
		if err := json.Unmarshal(data, &reference); err != nil || reference.Django != "6.1" {
			t.Fatal("invalid catalog reference", err)
		}
		if !reflect.DeepEqual(actual, reference.Observations[key]) {
			t.Fatalf("catalog %s differs from independent %s: %#v != %#v", key, backend, actual, reference.Observations[key])
		}
	}
}

func catalogRows[T any](t *testing.T, objects interface {
	All(context.Context) ([]T, error)
}) []T {
	t.Helper()
	rows, err := objects.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func catalogSnapshot(t *testing.T, f *managementFixture) []any {
	t.Helper()
	sessions, audit := snapshotIdentitySystemRows(t, f.backend)
	return []any{
		catalogRows(t, models.UserObjects.Using(f.backend).OrderBy(models.UserFields.ID.Asc())),
		catalogRows(t, models.GroupObjects.Using(f.backend).OrderBy(models.GroupFields.ID.Asc())),
		catalogRows(t, models.PermissionObjects.Using(f.backend).OrderBy(models.PermissionFields.ID.Asc())),
		catalogRows(t, models.UserGroupsLinkObjects.Using(f.backend).OrderBy(models.UserGroupsLinkFields.ID.Asc())),
		catalogRows(t, models.UserPermissionsLinkObjects.Using(f.backend).OrderBy(models.UserPermissionsLinkFields.ID.Asc())),
		catalogRows(t, models.GroupPermissionsLinkObjects.Using(f.backend).OrderBy(models.GroupPermissionsLinkFields.ID.Asc())),
		catalogRows(t, hostmodels.AccessNoteObjects.Using(f.backend).OrderBy(hostmodels.AccessNoteFields.ID.Asc())),
		catalogRows(t, hostmodels.AccessGuardObjects.Using(f.backend).OrderBy(hostmodels.AccessGuardFields.ID.Asc())),
		sessions, audit,
	}
}

func catalogCodes(t *testing.T, f *managementFixture, ids []int64) []string {
	t.Helper()
	rows := catalogRows(t, models.PermissionObjects.Using(f.backend).Filter(models.PermissionFields.ID.In(ids...)).OrderBy(models.PermissionFields.Code.Asc()))
	result := []string{}
	for _, row := range rows {
		result = append(result, strings.TrimPrefix(row.Code, "helpdesk.ticket."))
	}
	return result
}

func RunCatalogManagement(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("lifecycle_matches_reference_and_host_relations", func(t *testing.T) { runCatalogLifecycle(t, open) })
	t.Run("current_permission_admission_matches_django", func(t *testing.T) { runCatalogAdmission(t, open) })
	runCatalogBoundaries(t, open)
	runCatalogLimits(t, open)
}

func runCatalogLifecycle(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	relations := catalogRelations(t)
	backend, other := open(t)
	f := newManagementFixture(t, backend, 3)
	policy := managementHost(t, backend)
	manager := f.manager(t, f.runtime)
	view := catalogRows(t, models.PermissionObjects.Using(backend).OrderBy(models.PermissionFields.ID.Asc()))[0]
	change, err := manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("helpdesk.ticket.change", "Change ticket"))
	if err != nil || change.Revision != 1 {
		t.Fatal("permission create", err)
	}
	selected := []int64{change.ID, view.ID, change.ID}
	input := identity.NewGroupCreate("Editors").WithPermissions(selected...)
	selected[0] = -1 // An earlier input snapshot must own its original selection.
	group, err := manager.CreateGroup(t.Context(), f.actor, input)
	if err != nil || group.Revision != 1 || len(group.PermissionIDs) != 2 {
		t.Fatal("group create", err)
	}
	if _, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithGroups(group.ID).WithPermissions(change.ID)); err != nil {
		t.Fatal(err)
	}
	beforeUser := f.stored(t)
	beforeSessions, _ := snapshotIdentitySystemRows(t, backend)
	held, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogReference(t, "group_created", map[string]any{"name": group.Name, "permissions": catalogCodes(t, f, group.PermissionIDs), "members": 1, "effective": managedGrants(t, f.runtime, f.user.PrincipalID)})
	group.PermissionIDs[0] = -1
	detail, err := manager.Group(t.Context(), f.actor, group.ID)
	if err != nil || detail.PermissionIDs[0] <= 0 {
		t.Fatal("returned selection alias", err)
	}
	group, err = manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithName("Operators").WithPermissions(change.ID))
	if err != nil || group.Revision != 2 {
		t.Fatal("group update", err)
	}
	heldGrants := []string{}
	for _, permission := range held.Principal().Permissions() {
		heldGrants = append(heldGrants, strings.TrimPrefix(string(permission), "helpdesk.ticket."))
	}
	assertCatalogReference(t, "group_edited", map[string]any{"name": group.Name, "permissions": catalogCodes(t, f, group.PermissionIDs), "effective": managedGrants(t, f.runtime, f.user.PrincipalID), "held_effective": heldGrants, "same_password": f.stored(t).EncodedPassword == beforeUser.EncodedPassword})
	beforeNoop := catalogSnapshot(t, f)
	if same, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 2, identity.GroupPatch{}.WithPermissions(change.ID, change.ID)); err != nil || same.Revision != 2 || !reflect.DeepEqual(beforeNoop, catalogSnapshot(t, f)) {
		t.Fatal("group no-op changed state", err)
	}
	change, err = manager.UpdatePermission(t.Context(), f.actor, change.ID, 1, identity.PermissionPatch{}.WithCode("helpdesk.ticket.manage").WithName("Manage ticket"))
	if err != nil || change.Revision != 2 {
		t.Fatal("permission rename", err)
	}
	userDetails, err := manager.User(t.Context(), f.actor, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogReference(t, "permission_renamed", map[string]any{"name": change.Name, "code": strings.TrimPrefix(change.Code, "helpdesk.ticket."), "direct": catalogCodes(t, f, userDetails.PermissionIDs), "group": catalogCodes(t, f, group.PermissionIDs), "effective": managedGrants(t, f.runtime, f.user.PrincipalID)})
	beforeNoop = catalogSnapshot(t, f)
	if same, err := manager.UpdatePermission(t.Context(), f.actor, change.ID, 2, identity.PermissionPatch{}.WithCode(change.Code).WithName(change.Name)); err != nil || same != change || !reflect.DeepEqual(beforeNoop, catalogSnapshot(t, f)) {
		t.Fatal("permission no-op changed state", err)
	}
	fault := &userManagementBoundary{ManagementBackend: f.runtime, mode: "audit_failure"}
	failed := f.manager(t, fault)
	if result, err := failed.UpdateGroup(t.Context(), f.actor, group.ID, 2, identity.GroupPatch{}.WithName("rollback").WithPermissions()); err == nil || result.ID != 0 {
		t.Fatal("group rollback published")
	}
	if result, err := failed.UpdatePermission(t.Context(), f.actor, change.ID, 2, identity.PermissionPatch{}.WithName("rollback")); err == nil || result.ID != 0 {
		t.Fatal("permission rollback published")
	}
	if fault.faults != 2 || !reflect.DeepEqual(beforeNoop, catalogSnapshot(t, f)) {
		t.Fatal("catalog rollback changed durable state")
	}
	storedGroup, err := manager.Group(t.Context(), f.actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedPermission, err := manager.Permission(t.Context(), f.actor, change.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogReference(t, "rollback", map[string]any{"group_name": storedGroup.Name, "permission_name": storedPermission.Name, "effective": managedGrants(t, f.runtime, f.user.PrincipalID)})
	// This host model has both SET_NULL to Group and CASCADE to Permission.
	survivor, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("preserve through group deletion", view.ID).WithGroupID(group.ID))
	if err != nil {
		t.Fatal(err)
	}
	cascade, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("remove with permission", change.ID).WithGroupID(group.ID))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := manager.DeletePermission(t.Context(), f.actor, change.ID, 2, policy.AccountsPermission)
	if err != nil || deleted.ID != change.ID || deleted.Revision != 2 || deleted.DeletedRows != 4 {
		t.Fatal("host permission deletion", deleted, err)
	}
	if reflect.TypeOf(deleted).NumField() != 3 {
		t.Fatal("delete-only result exposes catalog profile")
	}
	if rows := catalogRows(t, hostmodels.AccessNoteObjects.Using(backend).Filter(hostmodels.AccessNoteFields.ID.Exact(cascade.ID))); len(rows) != 0 {
		t.Fatal("host CASCADE ignored")
	}
	assertCatalogReference(t, "permission_deleted", map[string]any{
		"user_exists":        len(catalogRows(t, models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(f.user.ID)))) == 1,
		"group_exists":       len(catalogRows(t, models.GroupObjects.Using(backend).Filter(models.GroupFields.ID.Exact(group.ID)))) == 1,
		"direct_permissions": len(catalogRows(t, models.UserPermissionsLinkObjects.Using(backend).Filter(relations.IdentityUserPermissionsLink.Source.ID.Exact(f.user.ID)))),
		"group_permissions":  len(catalogRows(t, models.GroupPermissionsLinkObjects.Using(backend).Filter(relations.IdentityGroupPermissionsLink.Source.ID.Exact(group.ID)))),
		"effective":          managedGrants(t, f.runtime, f.user.PrincipalID),
	})
	if stale, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 2, identity.GroupPatch{}.WithName("stale after permission deletion")); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || stale.ID != 0 {
		t.Fatal("permission deletion did not advance group owner revision", err)
	}
	if stale, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, beforeUser.Revision, identity.UserPatch{}.WithFirstName("stale")); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || stale.ID != 0 {
		t.Fatal("permission deletion did not advance direct user revision", err)
	}
	deleted, err = manager.DeleteGroup(t.Context(), f.actor, group.ID, 3, policy.AccountsGroup)
	if err != nil || deleted.ID != group.ID || deleted.DeletedRows != 2 {
		t.Fatal("host group deletion", deleted, err)
	}
	rows := catalogRows(t, hostmodels.AccessNoteObjects.Using(backend).Filter(hostmodels.AccessNoteFields.ID.Exact(survivor.ID)))
	if len(rows) != 1 || rows[0].GroupID != nil || rows[0].PermissionID != view.ID {
		t.Fatal("host SET_NULL changed unrelated data")
	}
	assertCatalogReference(t, "group_deleted", map[string]any{
		"user_exists":       len(catalogRows(t, models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(f.user.ID)))) == 1,
		"memberships":       len(catalogRows(t, models.UserGroupsLinkObjects.Using(backend).Filter(relations.IdentityUserGroupsLink.Source.ID.Exact(f.user.ID)))),
		"permission_exists": len(catalogRows(t, models.PermissionObjects.Using(backend).Filter(models.PermissionFields.ID.Exact(view.ID)))) == 1,
		"effective":         managedGrants(t, f.runtime, f.user.PrincipalID),
	})
	beforeUser.Revision += 2 // Direct permission removal, then group removal.
	if !reflect.DeepEqual(f.stored(t), beforeUser) || f.hasher.calls.Load() != 0 {
		t.Fatal("catalog change rewrote user/credential")
	}
	afterSessions, _ := snapshotIdentitySystemRows(t, backend)
	if !reflect.DeepEqual(beforeSessions, afterSessions) {
		t.Fatal("catalog change altered session bytes")
	}
	reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	current, err := reopened.Authenticator().Authenticate(t.Context(), f.user.Username, managementOldPassword)
	if err != nil || current.SessionStamp() != held.SessionStamp() || len(current.Principal().Permissions()) != 0 {
		t.Fatal("reopen lost credential or retained stale grants", err)
	}
	for _, item := range []struct {
		object       string
		id           int64
		changeFields []string
	}{{"group", group.ID, []string{"name", "permissions"}}, {"permission", change.ID, []string{"code", "name"}}} {
		history, err := f.runtime.AuditHistory(t.Context(), "godj_identity."+item.object, item.id, 10)
		if err != nil || len(history) != 3 || history[0].Action != admin.ActionAdd || history[1].Action != admin.ActionChange || history[2].Action != admin.ActionDelete || !reflect.DeepEqual(history[1].ChangedFields, item.changeFields) {
			t.Fatal("catalog audit semantics", err)
		}
		for _, event := range history {
			if event.ActorID != f.actor.ID() || event.DisplayLabel != "" {
				t.Fatal("catalog audit leaked values or lost actor")
			}
		}
	}
}

func runCatalogAdmission(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	observations := map[string]map[string]map[string]bool{}
	for _, kind := range []string{"group", "permission"} {
		observations[kind] = map[string]map[string]bool{}
		for _, action := range []string{"none", "view", "add", "change", "delete"} {
			t.Run(kind+"_"+action, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 0)
				policy := managementHost(t, backend)
				group, _, permission := managementUserSelections(t, f)
				if _, err := models.UserObjects.Update(t.Context(), backend, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2)); err != nil {
					t.Fatal(err)
				}
				if action != "none" {
					grant, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("godj_identity."+action+"_"+kind, "Admission"))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.root.ID, grant.ID)); err != nil {
						t.Fatal(err)
					}
				}
				manager := f.manager(t, f.runtime)
				var viewErr, pageErr, createErr, updateErr, deleteErr error
				if kind == "group" {
					_, viewErr = manager.Group(t.Context(), f.actor, group.ID)
					_, pageErr = manager.Groups(t.Context(), f.actor, 0, 10)
					_, updateErr = manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{})
					_, createErr = manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Created"))
					_, deleteErr = manager.DeleteGroup(t.Context(), f.actor, group.ID, 1, policy.AccountsGroup)
				} else {
					_, viewErr = manager.Permission(t.Context(), f.actor, permission.ID)
					_, pageErr = manager.Permissions(t.Context(), f.actor, 0, 10)
					_, updateErr = manager.UpdatePermission(t.Context(), f.actor, permission.ID, 1, identity.PermissionPatch{})
					_, createErr = manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("helpdesk.ticket.created", "Created"))
					_, deleteErr = manager.DeletePermission(t.Context(), f.actor, permission.ID, 1, policy.AccountsPermission)
				}
				for _, err := range []error{viewErr, pageErr, createErr, updateErr, deleteErr} {
					if err != nil && !errors.Is(err, &identity.Error{Code: identity.CodePermission}) {
						t.Fatal("catalog admission failed outside permission boundary", err)
					}
				}
				if (viewErr == nil) != (pageErr == nil) || f.hasher.calls.Load() != 0 {
					t.Fatal("catalog read authority diverged or hashed")
				}
				observations[kind][action] = map[string]bool{"view": viewErr == nil, "create": createErr == nil, "change": updateErr == nil, "delete": deleteErr == nil}
			})
		}
	}
	assertCatalogReference(t, "admission", observations)
}
