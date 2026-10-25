package accountstoretest

import (
	"fmt"
	"sync"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func catalogConcurrentWrites(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	writers := []accountmodel.CatalogResource[models.Group]{f.catalog.Groups(), {}}
	other, err := project.NewAccountCatalog(catalogSessions(t, reopen(t)), auth.PrincipalAuthorizer{}, catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	writers[1] = other.Groups()
	run := func(ids []int64, inputs []orm.ModelInput[models.Group]) ([]accountmodel.CatalogChange, []error) {
		results, failures := make([]accountmodel.CatalogChange, 2), make([]error, 2)
		start := make(chan struct{})
		var wait sync.WaitGroup
		for index := range writers {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				results[index], failures[index] = writers[index].Update(t.Context(), actor, ids[index], 1, inputs[index])
			}(index)
		}
		close(start)
		wait.Wait()
		return results, failures
	}
	t.Run("same_revision", func(t *testing.T) {
		group, err := writers[0].Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("concurrent revision")}, nil))
		if err != nil {
			t.Fatal(err)
		}
		inputs := []orm.ModelInput[models.Group]{catalogGroupInput(t, map[string]query.Value{"name": query.String("revision first")}, nil), catalogGroupInput(t, map[string]query.Value{"name": query.String("revision second")}, nil)}
		results, failures := run([]int64{group.ID, group.ID}, inputs)
		winners := 0
		for index, err := range failures {
			if err == nil {
				winners++
				if results[index].Revision != 2 {
					t.Fatal("winning catalog revision")
				}
			} else {
				requireProfileCode(t, err, accountmodel.CodeConflict)
				if results[index].Revision != 0 {
					t.Fatal("conflicting catalog published result")
				}
			}
		}
		stored := catalogRow(t, models.GroupObjects.Using(reopen(t)).Filter(models.GroupFields.ID.Exact(group.ID)).OrderBy(models.GroupFields.ID.Asc()))
		if winners != 1 || stored.Revision != 2 || len(catalogAudit(t, reopen(t), actor, "group", group.ID)) != 2 {
			t.Fatal("catalog revision and audit did not share one fence")
		}
	})
	t.Run("different_groups_shared_member", func(t *testing.T) {
		member, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("shared-bound-member", "shared-bound-member", f.account.user.EncodedPassword, joined))
		if err != nil {
			t.Fatal(err)
		}
		groups, permissions := make([]models.Group, 2), make([]models.Permission, 2)
		if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
			for index := 0; index < auth.MaximumPermissions-1; index++ {
				permission, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("race.base%d", index), "Existing grant"))
				if err != nil {
					return err
				}
				if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(member.ID, permission.ID)); err != nil {
					return err
				}
			}
			for index := range groups {
				groups[index], err = models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate(fmt.Sprintf("race group %d", index)))
				if err != nil {
					return err
				}
				permissions[index], err = models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("race.extra%d", index), "Proposed grant"))
				if err != nil {
					return err
				}
				if _, err := models.UserGroupsLinkObjects.Create(t.Context(), scope, models.NewUserGroupsLinkCreate(member.ID, groups[index].ID)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		inputs := make([]orm.ModelInput[models.Group], 2)
		ids := make([]int64, 2)
		for index := range groups {
			ids[index] = groups[index].ID
			inputs[index] = catalogGroupInput(t, map[string]query.Value{"name": query.String(fmt.Sprintf("accepted race group %d", index))}, map[string][]int64{"permissions": {permissions[index].ID}})
		}
		results, failures := run(ids, inputs)
		winners := 0
		for index, err := range failures {
			stored, failure := writers[index].Get(t.Context(), actor, ids[index])
			if failure != nil {
				t.Fatal(failure)
			}
			keys, loaded := stored.View().Collection("permissions")
			history := catalogAudit(t, reopen(t), actor, "group", ids[index])
			if err == nil {
				winners++
				if results[index].Revision != 2 || stored.Revision() != 2 || !loaded || len(keys) != 1 || keys[0] != permissions[index].ID || len(history) != 1 {
					t.Fatal("accepted cross-group expansion was incomplete")
				}
			} else {
				diagnostics, rejected := validation.Rejected(err)
				if !rejected || diagnostics.ByField("permissions").Empty() || results[index].Revision != 0 || stored.Revision() != 1 || !loaded || len(keys) != 0 || len(history) != 0 {
					t.Fatal("concurrent expansion was not rolled back", err)
				}
				if value, _ := stored.View().Get("name"); !value.Equal(query.String(groups[index].Name)) {
					t.Fatal("rejected cross-group scalar survived")
				}
			}
		}
		credential, present, err := f.account.store.CredentialByID(t.Context(), member.PrincipalID)
		if err != nil || !present || winners != 1 || len(credential.Principal().Permissions()) != auth.MaximumPermissions || profileRow(t, reopen(t), member.ID).Revision != 1 {
			t.Fatal("different groups escaped shared account permission fence", winners, err)
		}
	})
}
