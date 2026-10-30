package identitytest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

func catalogGrantKeys(t *testing.T, f *managementFixture, count int) []int64 {
	t.Helper()
	keys := []int64{}
	if err := f.backend.Atomic(t.Context(), func(session db.Session) error {
		for index := range count {
			row, err := models.PermissionObjects.Create(t.Context(), session, models.NewPermissionCreate(fmt.Sprintf("catalog.p%03d", index), "Catalog grant"))
			if err != nil {
				return err
			}
			keys = append(keys, row.ID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return keys
}

func runCatalogLimits(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("delete_versions_direct_owners_across_batches_and_rolls_back_overflow", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		policy := managementHost(t, backend)
		manager := f.manager(t, f.runtime)
		permission := catalogRows(t, models.PermissionObjects.Using(backend).OrderBy(models.PermissionFields.ID.Asc()))[0]
		var lastGroup models.Group
		var indirect models.User
		if err := backend.Atomic(t.Context(), func(session db.Session) error {
			for index := range 257 {
				group, err := models.GroupObjects.Create(t.Context(), session, models.NewGroupCreate(fmt.Sprintf("owner-%03d", index)))
				if err != nil {
					return err
				}
				if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), session, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
					return err
				}
				user, err := models.UserObjects.Create(t.Context(), session, models.NewUserCreate(fmt.Sprintf("owner-%03d", index), fmt.Sprintf("owner-%03d", index), f.user.EncodedPassword, f.user.DateJoined))
				if err != nil {
					return err
				}
				if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), session, models.NewUserPermissionsLinkCreate(user.ID, permission.ID)); err != nil {
					return err
				}
				lastGroup = group
			}
			var err error
			indirect, err = models.UserObjects.Create(t.Context(), session, models.NewUserCreate("indirect-owner", "indirect-owner", f.user.EncodedPassword, f.user.DateJoined))
			if err != nil {
				return err
			}
			_, err = models.UserGroupsLinkObjects.Create(t.Context(), session, models.NewUserGroupsLinkCreate(indirect.ID, lastGroup.ID))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := models.GroupObjects.Patch(t.Context(), backend, lastGroup, models.GroupPatch{}.WithRevision(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		before := catalogSnapshot(t, f)
		if result, err := manager.DeletePermission(t.Context(), f.actor, permission.ID, 1, policy.AccountsPermission); !errors.Is(err, &identity.Error{Code: identity.CodePersistence}) || result.ID != 0 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("late owner overflow left earlier revisions or deletes committed", err)
		}
		if _, err := models.GroupObjects.Patch(t.Context(), backend, lastGroup, models.GroupPatch{}.WithRevision(1)); err != nil {
			t.Fatal(err)
		}
		users := catalogRows(t, models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()))
		groups := catalogRows(t, models.GroupObjects.Using(backend).OrderBy(models.GroupFields.ID.Asc()))
		sessions, _ := snapshotIdentitySystemRows(t, backend)
		result, err := manager.DeletePermission(t.Context(), f.actor, permission.ID, 1, policy.AccountsPermission)
		if err != nil || result.DeletedRows != 516 {
			t.Fatal("permission delete did not complete both owner batches", result, err)
		}
		for index := range users {
			if users[index].ID != f.root.ID && users[index].ID != indirect.ID {
				users[index].Revision++
			}
		}
		for index := range groups {
			groups[index].Revision++
		}
		if !reflect.DeepEqual(users, catalogRows(t, models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()))) || !reflect.DeepEqual(groups, catalogRows(t, models.GroupObjects.Using(backend).OrderBy(models.GroupFields.ID.Asc()))) {
			t.Fatal("direct owner revisions were omitted, duplicated, or expanded to indirect users")
		}
		afterSessions, _ := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(sessions, afterSessions) {
			t.Fatal("owner revision changes revoked sessions")
		}
		if value, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithFirstName("stale")); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || value.ID != 0 {
			t.Fatal("stale direct user revision accepted", err)
		}
		if value, err := manager.UpdateGroup(t.Context(), f.actor, lastGroup.ID, 1, identity.GroupPatch{}.WithName("stale")); !errors.Is(err, &identity.Error{Code: identity.CodeConflict}) || value.ID != 0 {
			t.Fatal("stale group owner revision accepted", err)
		}
	})
	t.Run("group_union_checks_every_member_across_batches", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		managementHost(t, backend)
		manager := f.manager(t, f.runtime)
		keys := catalogGrantKeys(t, f, auth.MaximumPermissions)
		group, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Large group").WithPermissions(keys[:255]...))
		if err != nil {
			t.Fatal(err)
		}
		var last models.User
		view := catalogRows(t, models.PermissionObjects.Using(backend).Filter(models.PermissionFields.Code.Exact("helpdesk.ticket.view")))[0]
		if err := backend.Atomic(t.Context(), func(session db.Session) error {
			for index := range 257 {
				user, err := models.UserObjects.Create(t.Context(), session, models.NewUserCreate(fmt.Sprintf("batch-%03d", index), fmt.Sprintf("batch-%03d", index), f.user.EncodedPassword, f.user.DateJoined))
				if err != nil {
					return err
				}
				if _, err := models.UserGroupsLinkObjects.Create(t.Context(), session, models.NewUserGroupsLinkCreate(user.ID, group.ID)); err != nil {
					return err
				}
				last = user
			}
			_, err := models.UserPermissionsLinkObjects.Create(t.Context(), session, models.NewUserPermissionsLinkCreate(last.ID, view.ID))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		before := catalogSnapshot(t, f)
		result, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithName("must roll back").WithPermissions(keys...))
		if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.Empty() || result.ID != 0 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("last member's direct union overflow was ignored", err)
		}
		current, err := f.runtime.Authenticator().Resolve(t.Context(), last.PrincipalID)
		if err != nil || len(current.Principal().Permissions()) != 256 {
			t.Fatal("exact grant boundary became unusable", err)
		}
		if _, err := manager.UpdateUser(t.Context(), f.actor, last.ID, 1, identity.UserPatch{}.WithPermissions()); err != nil {
			t.Fatal(err)
		}
		users := catalogRows(t, models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()))
		result, err = manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithPermissions(keys...))
		if err != nil || result.Revision != 2 || len(result.PermissionIDs) != 256 {
			t.Fatal("representable group expansion failed", err)
		}
		if !reflect.DeepEqual(users, catalogRows(t, models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()))) {
			t.Fatal("group expansion rewrote user revisions/credentials")
		}
		current, err = f.runtime.Authenticator().Resolve(t.Context(), last.PrincipalID)
		if err != nil || len(current.Principal().Permissions()) != 256 {
			t.Fatal("last page missed committed grants", err)
		}
		tooMany := append(append([]int64{}, keys...), view.ID)
		before = catalogSnapshot(t, f)
		if value, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("too many").WithPermissions(tooMany...)); err == nil || value.ID != 0 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("oversized group selection accepted")
		}
	})
	t.Run("other_group_union_and_overlap_are_preserved", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		manager := f.manager(t, f.runtime)
		keys := catalogGrantKeys(t, f, 256)
		other, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Other grants").WithPermissions(keys[:255]...))
		if err != nil {
			t.Fatal(err)
		}
		group, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Edited grants").WithPermissions(keys[0]))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithGroups(other.ID, group.ID)); err != nil {
			t.Fatal(err)
		}
		before := catalogSnapshot(t, f)
		if result, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithPermissions(keys[255])); err == nil || result.ID != 0 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("other group grants were dropped when validating union", err)
		}
		if result, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 1, identity.GroupPatch{}.WithPermissions(keys[:255]...)); err != nil || result.Revision != 2 {
			t.Fatal("overlapping group permissions counted more than once", err)
		}
	})
	t.Run("current_group_authority_and_self_revocation", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(string(identity.ChangeGroup), "Change group"))
		if err != nil {
			t.Fatal(err)
		}
		group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Actor grants"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.root.ID, group.ID)); err != nil {
			t.Fatal(err)
		}
		if _, err := models.UserObjects.Patch(t.Context(), backend, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2)); err != nil {
			t.Fatal(err)
		}
		actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: f.actor.ID(), Active: true})
		if err != nil {
			t.Fatal(err)
		}
		manager := f.manager(t, f.runtime)
		if value, err := manager.UpdateGroup(t.Context(), actor, group.ID, 1, identity.GroupPatch{}.WithPermissions()); err != nil || value.Revision != 2 {
			t.Fatal("current group authority was not used or self-revocation failed", err)
		}
		before := catalogSnapshot(t, f)
		if value, err := manager.UpdateGroup(t.Context(), f.actor, group.ID, 2, identity.GroupPatch{}.WithName("stale authority")); !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || value.ID != 0 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("past actor grants survived self-revocation", err)
		}
	})
	for _, kind := range []string{"group", "permission"} {
		t.Run("competing_"+kind+"_creators_and_editors", func(t *testing.T) {
			first, second := open(t)
			f := newManagementFixture(t, first, 0)
			managementHost(t, first)
			managers := catalogManagers(t, f, second)
			results := catalogCompete(t, managers, func(ctx context.Context, manager *identity.Manager) error {
				if kind == "group" {
					_, err := manager.CreateGroup(ctx, f.actor, identity.NewGroupCreate("One owner"))
					return err
				}
				_, err := manager.CreatePermission(ctx, f.actor, identity.NewPermissionCreate("catalog.one_owner", "One owner"))
				return err
			})
			assertCatalogCompetition(t, results, false)
			var id int64
			if kind == "group" {
				id = catalogRows(t, models.GroupObjects.Using(first).Filter(models.GroupFields.Name.Exact("One owner")))[0].ID
			} else {
				id = catalogRows(t, models.PermissionObjects.Using(first).Filter(models.PermissionFields.Code.Exact("catalog.one_owner")))[0].ID
			}
			results = catalogCompete(t, managers, func(ctx context.Context, manager *identity.Manager) error {
				if kind == "group" {
					_, err := manager.UpdateGroup(ctx, f.actor, id, 1, identity.GroupPatch{}.WithName("One edit"))
					return err
				}
				_, err := manager.UpdatePermission(ctx, f.actor, id, 1, identity.PermissionPatch{}.WithName("One edit"))
				return err
			})
			assertCatalogCompetition(t, results, true)
			history, err := f.runtime.AuditHistory(t.Context(), "godj_identity."+kind, id, 10)
			if err != nil || len(history) != 2 {
				t.Fatal("concurrent catalog writes duplicated audit or retried", err)
			}
		})
	}
	t.Run("group_edit_competes_with_user_join_under_one_fence", func(t *testing.T) {
		first, second := open(t)
		f := newManagementFixture(t, first, 0)
		managementHost(t, first)
		keys := catalogGrantKeys(t, f, 256)
		managers := catalogManagers(t, f, second)
		group, err := managers[0].CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Concurrent grants").WithPermissions(keys[:255]...))
		if err != nil {
			t.Fatal(err)
		}
		start, results := make(chan struct{}), make(chan error, 2)
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		go func() {
			<-start
			_, err := managers[0].UpdateGroup(ctx, f.actor, group.ID, 1, identity.GroupPatch{}.WithPermissions(keys...))
			results <- err
		}()
		go func() {
			<-start
			_, err := managers[1].UpdateUser(ctx, f.actor, f.user.ID, 1, identity.UserPatch{}.WithGroups(group.ID))
			results <- err
		}()
		close(start)
		assertCatalogCompetition(t, []error{<-results, <-results}, false)
		current, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
		if err != nil || len(current.Principal().Permissions()) > auth.MaximumPermissions {
			t.Fatal("concurrent writers committed an unrepresentable principal", err)
		}
		value, err := managers[0].Group(t.Context(), f.actor, group.ID)
		if err != nil || (value.Revision == 2) == (f.stored(t).Revision == 2) {
			t.Fatal("both grant changes committed or both were lost", err)
		}
	})
}

func catalogManagers(t *testing.T, f *managementFixture, second TransitionBackend) []*identity.Manager {
	t.Helper()
	other, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	return []*identity.Manager{f.manager(t, f.runtime), f.manager(t, other)}
}

func catalogCompete(t *testing.T, managers []*identity.Manager, call func(context.Context, *identity.Manager) error) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	start, results := make(chan struct{}), make(chan error, len(managers))
	for _, manager := range managers {
		go func() { <-start; results <- call(ctx, manager) }()
	}
	close(start)
	values := make([]error, len(managers))
	for index := range values {
		select {
		case values[index] = <-results:
		case <-ctx.Done():
			t.Fatal("catalog coordination did not finish", ctx.Err())
		}
	}
	return values
}

func assertCatalogCompetition(t *testing.T, results []error, conflict bool) {
	t.Helper()
	success, rejected := 0, 0
	for _, err := range results {
		if err == nil {
			success++
			continue
		}
		if conflict && errors.Is(err, &identity.Error{Code: identity.CodeConflict}) {
			rejected++
			continue
		}
		if _, ok := validation.Rejected(err); !conflict && ok {
			rejected++
			continue
		}
		t.Fatal("catalog competition failed outside expected rejection", err)
	}
	if success != 1 || rejected != 1 {
		t.Fatal("catalog competition did not commit exactly one winner", success, rejected)
	}
}
