package identitytest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

func runCatalogBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("view_fallback_never_masks_authorizer_failure", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		group, _, permission := managementUserSelections(t, f)
		manager, err := identity.NewManager(f.runtime, f.hasher, catalogFailingAuthorizer{})
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range []func() (any, error){
			func() (any, error) { return manager.Group(t.Context(), f.actor, group.ID) },
			func() (any, error) { return manager.Groups(t.Context(), f.actor, 0, 10) },
			func() (any, error) { return manager.Permission(t.Context(), f.actor, permission.ID) },
			func() (any, error) { return manager.Permissions(t.Context(), f.actor, 0, 10) },
			func() (any, error) { return manager.User(t.Context(), f.actor, f.user.ID) },
			func() (any, error) { return manager.Users(t.Context(), f.actor, 0, 10) },
		} {
			result, err := call()
			if !errors.Is(err, &identity.Error{Code: identity.CodePersistence}) || !reflect.ValueOf(result).IsZero() {
				t.Fatal("permission-shaped authorizer failure fell back to successful change authority", err)
			}
		}
	})
	t.Run("invalid_duplicate_missing_and_stale_input_preserve_state", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		policy := managementHost(t, backend)
		group, _, permission := managementUserSelections(t, f)
		manager := f.manager(t, f.runtime)
		before := catalogSnapshot(t, f)
		calls := []func() (any, error){
			func() (any, error) { return manager.CreateGroup(t.Context(), f.actor, identity.GroupCreate{}) },
			func() (any, error) { return manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("")) },
			func() (any, error) {
				return manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate(strings.Repeat("한", 151)))
			},
			func() (any, error) {
				return manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("nul\x00name"))
			},
			func() (any, error) {
				return manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate(group.Name))
			},
			func() (any, error) {
				return manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("missing").WithPermissions(99999))
			},
			func() (any, error) {
				return manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithPermissions(-1))
			},
			func() (any, error) {
				return manager.CreatePermission(t.Context(), f.actor, identity.PermissionCreate{})
			},
			func() (any, error) {
				return manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("Malformed", "Name"))
			},
			func() (any, error) {
				return manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("helpdesk.valid", ""))
			},
			func() (any, error) {
				return manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("helpdesk.valid", "bad\xff"))
			},
			func() (any, error) {
				return manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate(permission.Code, "Duplicate"))
			},
			func() (any, error) {
				return manager.UpdatePermission(t.Context(), f.actor, permission.ID, 1, identity.PermissionPatch{}.WithCode("helpdesk.ticket.view"))
			},
		}
		for index, call := range calls {
			result, err := call()
			if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.Empty() || !reflect.ValueOf(result).IsZero() {
				t.Fatal("catalog invalid input did not reject before publishing", index, err)
			}
			if !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
				t.Fatal("catalog invalid input mutated state", index)
			}
		}
		for _, call := range []func() (any, error){
			func() (any, error) {
				return manager.UpdateGroup(t.Context(), f.actor, group.ID, 2, identity.GroupPatch{}.WithName("stale"))
			},
			func() (any, error) {
				return manager.DeleteGroup(t.Context(), f.actor, group.ID, 2, policy.AccountsGroup)
			},
			func() (any, error) {
				return manager.UpdatePermission(t.Context(), f.actor, permission.ID, 2, identity.PermissionPatch{}.WithName("stale"))
			},
			func() (any, error) {
				return manager.DeletePermission(t.Context(), f.actor, permission.ID, 2, policy.AccountsPermission)
			},
		} {
			result, err := call()
			if !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || !reflect.ValueOf(result).IsZero() || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
				t.Fatal("stale catalog revision changed state", err)
			}
		}
		if _, err := manager.Group(t.Context(), f.actor, 99999); !errors.Is(err, &identity.Error{Code: identity.CodeNotFound}) {
			t.Fatal("group absence classification", err)
		}
		if _, err := manager.Permission(t.Context(), f.actor, 99999); !errors.Is(err, &identity.Error{Code: identity.CodeNotFound}) {
			t.Fatal("permission absence classification", err)
		}
		if f.hasher.calls.Load() != 0 {
			t.Fatal("catalog validation used password hasher")
		}
	})
	t.Run("omission_clear_unicode_pages_and_deny_overlay", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		group, view, _ := managementUserSelections(t, f)
		manager := f.manager(t, f.runtime)
		groupDetail, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithName(strings.Repeat("한", 150)))
		if err != nil || len(groupDetail.PermissionIDs) != 2 {
			t.Fatal("omitted permissions were cleared or character length counted as bytes", err)
		}
		groupDetail, err = manager.UpdateGroup(t.Context(), f.actor, group.ID, 2, identity.GroupPatch{}.WithPermissions())
		if err != nil || groupDetail.PermissionIDs == nil || len(groupDetail.PermissionIDs) != 0 || groupDetail.Revision != 3 {
			t.Fatal("explicit empty permissions not cleared", err)
		}
		for _, name := range []string{"A", "B"} {
			if _, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate(name)); err != nil {
				t.Fatal(err)
			}
		}
		page, err := manager.Groups(t.Context(), f.actor, 1, 1)
		if err != nil || page.Total != 3 || len(page.Groups) != 1 || page.Groups[0].Name != "A" {
			t.Fatal("group scalar page", err)
		}
		page.Groups[0].Name = "caller mutation"
		if same, err := manager.Groups(t.Context(), f.actor, 1, 1); err != nil || same.Groups[0].Name != "A" {
			t.Fatal("page retained caller mutation", err)
		}
		permissions, err := manager.Permissions(t.Context(), f.actor, 1, 1)
		if err != nil || permissions.Total != 2 || len(permissions.Permissions) != 1 {
			t.Fatal("permission scalar page", err)
		}
		if _, err := manager.Groups(t.Context(), f.actor, 0, 101); !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
			t.Fatal("unbounded group page")
		}
		if _, err := manager.Permissions(t.Context(), f.actor, -1, 1); !errors.Is(err, &identity.Error{Code: identity.CodeInvalidInput}) {
			t.Fatal("negative permission offset")
		}
		denied, err := identity.NewManager(f.runtime, f.hasher, denyIdentityAuthorization{})
		if err != nil {
			t.Fatal(err)
		}
		before := catalogSnapshot(t, f)
		for _, call := range []func() (any, error){
			func() (any, error) { return denied.Group(t.Context(), f.actor, group.ID) },
			func() (any, error) { return denied.Permission(t.Context(), f.actor, view.ID) },
			func() (any, error) {
				return denied.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Denied"))
			},
			func() (any, error) {
				return denied.UpdatePermission(t.Context(), f.actor, view.ID, 1, identity.PermissionPatch{}.WithName("Denied"))
			},
		} {
			result, err := call()
			if !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || !reflect.ValueOf(result).IsZero() {
				t.Fatal("catalog deny overlay escaped", err)
			}
		}
		if !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("denied catalog request mutated state")
		}
	})
	for _, kind := range []string{"group", "permission"} {
		t.Run("host_protect_"+kind, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			policy := managementHost(t, backend)
			group, _, permission := managementUserSelections(t, f)
			if _, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("protected graph", permission.ID).WithGroupID(group.ID)); err != nil {
				t.Fatal(err)
			}
			if _, err := hostmodels.AccessGuardObjects.Create(t.Context(), backend, hostmodels.NewAccessGuardCreate().WithGroupID(group.ID).WithPermissionID(permission.ID)); err != nil {
				t.Fatal(err)
			}
			before := catalogSnapshot(t, f)
			var result identity.CatalogDeletion
			var err error
			if kind == "group" {
				result, err = f.manager(t, f.runtime).DeleteGroup(t.Context(), f.actor, group.ID, 1, policy.AccountsGroup)
			} else {
				result, err = f.manager(t, f.runtime).DeletePermission(t.Context(), f.actor, permission.ID, 1, policy.AccountsPermission)
			}
			if err == nil || result != (identity.CatalogDeletion{}) || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
				t.Fatal("host PROTECT failed or partial delete committed", err)
			}
		})
	}
	t.Run("read_scope_failure_never_publishes_catalog", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		group, _, permission := managementUserSelections(t, f)
		for _, mode := range []string{"read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure"} {
			manager := f.manager(t, &managementBoundary{ManagementBackend: f.runtime, mode: mode})
			for _, call := range []func() (any, error){
				func() (any, error) { return manager.Group(t.Context(), f.actor, group.ID) },
				func() (any, error) { return manager.Groups(t.Context(), f.actor, 0, 10) },
				func() (any, error) { return manager.Permission(t.Context(), f.actor, permission.ID) },
				func() (any, error) { return manager.Permissions(t.Context(), f.actor, 0, 10) },
			} {
				result, err := call()
				if err == nil || !reflect.ValueOf(result).IsZero() {
					t.Fatal("failed catalog snapshot published", mode, err)
				}
			}
		}
	})
	t.Run("write_scope_contract_never_publishes_catalog", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		policy := managementHost(t, backend)
		group, view, permission := managementUserSelections(t, f)
		before := catalogSnapshot(t, f)
		for _, mode := range []string{"write_zero", "write_nil", "write_twice", "write_swallowed_failure"} {
			for _, operation := range []string{"create_group", "update_group", "delete_group", "create_permission", "update_permission", "delete_permission"} {
				boundary := &userManagementBoundary{ManagementBackend: f.runtime, mode: mode}
				result, err := callCatalogMutation(t.Context(), f.manager(t, boundary), f.actor, operation, group, permission, []int64{view.ID}, policy)
				if err == nil || !reflect.ValueOf(result).IsZero() || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
					t.Fatal("invalid write scope published or mutated catalog", mode, operation, err)
				}
			}
		}
	})
	t.Run("rejected_write_with_cleanup_failure_is_execution_error", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		group, _, _ := managementUserSelections(t, f)
		boundary := &userManagementBoundary{ManagementBackend: f.runtime, mode: "rejected_cleanup"}
		before := catalogSnapshot(t, f)
		result, err := f.manager(t, boundary).CreateGroup(t.Context(), f.actor, identity.NewGroupCreate(group.Name))
		if _, rejected := validation.Rejected(err); rejected || !errors.Is(err, &identity.Error{Code: identity.CodePersistence}) || result.ID != 0 || boundary.faults != 1 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("catalog cleanup failure became renderable input", err)
		}
	})
	for _, operation := range []string{"create_group", "update_group", "delete_group", "create_permission", "update_permission", "delete_permission"} {
		modes := []string{"audit_failure", "callback_cancel", "unknown_rollback", "unknown_commit", "late_cancel"}
		if strings.HasPrefix(operation, "update_") || strings.HasPrefix(operation, "delete_") {
			modes = append(modes, "update_failure")
		}
		if operation == "create_group" || operation == "update_group" {
			modes = append(modes, "membership_failure")
		}
		for _, mode := range modes {
			t.Run(operation+"_"+mode, func(t *testing.T) { runCatalogFault(t, open, operation, mode) })
		}
	}
}

type catalogFailingAuthorizer struct{}

func (catalogFailingAuthorizer) Allowed(_ context.Context, _ auth.Principal, required auth.Permission) (bool, error) {
	if required == identity.ViewGroup || required == identity.ViewPermission || required == identity.ViewUser {
		return false, &identity.Error{Code: identity.CodePermission, Field: "actor"}
	}
	return true, nil
}

func runCatalogFault(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend), operation, mode string) {
	relations := catalogRelations(t)
	backend, _ := open(t)
	f := newManagementFixture(t, backend, 3)
	policy := managementHost(t, backend)
	group, view, permission := managementUserSelections(t, f)
	extra, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.extra", "Extra"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	note, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("keep unless permission deleted", permission.ID).WithGroupID(group.ID))
	if err != nil {
		t.Fatal(err)
	}
	before := catalogSnapshot(t, f)
	beforeUser := f.stored(t)
	beforeSessions, _ := snapshotIdentitySystemRows(t, backend)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fault := &userManagementBoundary{ManagementBackend: f.runtime, mode: mode, afterCommit: cancel}
	result, err := callCatalogMutation(ctx, f.manager(t, fault), f.actor, operation, group, permission, []int64{view.ID, extra.ID}, policy)
	committed := mode == "unknown_commit" || mode == "late_cancel"
	if mode == "late_cancel" {
		if err != nil || reflect.ValueOf(result).IsZero() || ctx.Err() == nil {
			t.Fatal("confirmed catalog commit lost", err)
		}
	} else if err == nil || !reflect.ValueOf(result).IsZero() {
		t.Fatal("failed/unknown catalog operation published", err)
	}
	if strings.HasPrefix(mode, "unknown_") && !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) {
		t.Fatal("catalog unknown classification lost", err)
	}
	if fault.calls != 1 || strings.HasSuffix(mode, "_failure") && fault.faults != 1 {
		t.Fatal("catalog fault was not reached exactly once", mode, fault.calls, fault.faults)
	}
	if !committed {
		if !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("failed catalog mutation did not roll back all rows")
		}
		return
	}
	manager := f.manager(t, f.runtime)
	var auditID int64
	object := "permission"
	if strings.HasSuffix(operation, "_group") {
		object = "group"
	}
	switch operation {
	case "create_group":
		rows := catalogRows(t, models.GroupObjects.Using(backend).Filter(models.GroupFields.Name.Exact("Created group")))
		if len(rows) != 1 || rows[0].Revision != 1 {
			t.Fatal("committed group absent")
		}
		auditID = rows[0].ID
		value, err := manager.Group(t.Context(), f.actor, auditID)
		if err != nil || !reflect.DeepEqual(value.PermissionIDs, []int64{view.ID, extra.ID}) {
			t.Fatal("created group lost selection", err)
		}
	case "update_group":
		auditID = group.ID
		value, err := manager.Group(t.Context(), f.actor, group.ID)
		if err != nil || value.Name != "Changed group" || value.Revision != 2 || !reflect.DeepEqual(value.PermissionIDs, []int64{view.ID, extra.ID}) {
			t.Fatal("committed group update incomplete", err)
		}
	case "delete_group":
		beforeUser.Revision++
		auditID = group.ID
		if _, err := manager.Group(t.Context(), f.actor, group.ID); !errors.Is(err, &identity.Error{Code: identity.CodeNotFound}) {
			t.Fatal("committed group still present", err)
		}
		rows := catalogRows(t, hostmodels.AccessNoteObjects.Using(backend).Filter(hostmodels.AccessNoteFields.ID.Exact(note.ID)))
		if len(rows) != 1 || rows[0].GroupID != nil {
			t.Fatal("committed group did not SET_NULL host")
		}
		if len(catalogRows(t, models.UserGroupsLinkObjects.Using(backend).Filter(relations.IdentityUserGroupsLink.Target.ID.Exact(group.ID)))) != 0 {
			t.Fatal("deleted group retained user membership")
		}
	case "create_permission":
		rows := catalogRows(t, models.PermissionObjects.Using(backend).Filter(models.PermissionFields.Code.Exact("helpdesk.ticket.created")))
		if len(rows) != 1 || rows[0].Name != "Created permission" || rows[0].Revision != 1 {
			t.Fatal("committed permission absent")
		}
		auditID = rows[0].ID
	case "update_permission":
		auditID = permission.ID
		value, err := manager.Permission(t.Context(), f.actor, permission.ID)
		if err != nil || value.Name != "Changed permission" || value.Code != "helpdesk.ticket.renamed" || value.Revision != 2 {
			t.Fatal("committed permission update incomplete", err)
		}
		if !reflect.DeepEqual(managedGrants(t, f.runtime, f.user.PrincipalID), []string{"renamed", "view"}) {
			t.Fatal("permission rename did not propagate through group")
		}
	case "delete_permission":
		auditID = permission.ID
		if _, err := manager.Permission(t.Context(), f.actor, permission.ID); !errors.Is(err, &identity.Error{Code: identity.CodeNotFound}) {
			t.Fatal("committed permission still present", err)
		}
		if len(catalogRows(t, hostmodels.AccessNoteObjects.Using(backend).Filter(hostmodels.AccessNoteFields.ID.Exact(note.ID)))) != 0 {
			t.Fatal("committed permission did not CASCADE host")
		}
		if !reflect.DeepEqual(managedGrants(t, f.runtime, f.user.PrincipalID), []string{"view"}) {
			t.Fatal("deleted permission retained effective grant")
		}
		owner, ownerErr := manager.Group(t.Context(), f.actor, group.ID)
		if ownerErr != nil || owner.Revision != 2 || !reflect.DeepEqual(owner.PermissionIDs, []int64{view.ID}) {
			t.Fatal("permission delete lost owner revision or membership", ownerErr)
		}
	}
	history, historyErr := f.runtime.AuditHistory(t.Context(), "godj_identity."+object, auditID, 10)
	if historyErr != nil || len(history) != 1 {
		t.Fatal("catalog committed without exactly one audit", historyErr)
	}
	afterSessions, _ := snapshotIdentitySystemRows(t, backend)
	if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeUser, f.stored(t)) || f.hasher.calls.Load() != 0 {
		t.Fatal("catalog mutation changed unrelated credential/session")
	}
}

func callCatalogMutation(ctx context.Context, manager *identity.Manager, actor auth.Principal, operation string, group models.Group, permission models.Permission, keys []int64, policy hostproject.RelationDeleters) (any, error) {
	switch operation {
	case "create_group":
		return manager.CreateGroup(ctx, actor, identity.NewGroupCreate("Created group").WithPermissions(keys...))
	case "update_group":
		return manager.UpdateGroup(ctx, actor, group.ID, 1, identity.GroupPatch{}.WithName("Changed group").WithPermissions(keys...))
	case "delete_group":
		return manager.DeleteGroup(ctx, actor, group.ID, 1, policy.AccountsGroup)
	case "create_permission":
		return manager.CreatePermission(ctx, actor, identity.NewPermissionCreate("helpdesk.ticket.created", "Created permission"))
	case "update_permission":
		return manager.UpdatePermission(ctx, actor, permission.ID, 1, identity.PermissionPatch{}.WithName("Changed permission").WithCode("helpdesk.ticket.renamed"))
	case "delete_permission":
		return manager.DeletePermission(ctx, actor, permission.ID, 1, policy.AccountsPermission)
	default:
		return nil, errors.New("unknown catalog test operation")
	}
}
