package identitytest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity"
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
}

type catalogFailingAuthorizer struct{}

func (catalogFailingAuthorizer) Allowed(_ context.Context, _ auth.Principal, required auth.Permission) (bool, error) {
	if required == identity.ViewGroup || required == identity.ViewPermission || required == identity.ViewUser {
		return false, &identity.Error{Code: identity.CodePermission, Field: "actor"}
	}
	return true, nil
}
