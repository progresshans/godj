package accountstoretest

import (
	"errors"
	"reflect"
	"testing"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	hostdef "github.com/progresshans/godj/conformance/identityfixture/modeldef"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func catalogHostGraph(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	local, err := hostdef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]migrations.Operation, 0, len(local.Models))
	for _, model := range local.Models {
		operations = append(operations, migrations.CreateModel{AppLabel: local.AppLabel, Model: model})
	}
	document, err := definition.Encode(definition.Producer{Name: "catalog-host-fixture", Version: "1"}, migrations.Migration{App: local.AppLabel, Name: "0001_initial", Dependencies: []migrations.MigrationKey{identity.InitialMigrationKey()}, Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	sources := append(identity.MigrationSources(), domain.MigrationSources()...)
	sources = append(sources, accountaudit.MigrationSources()...)
	sources = append(sources, definition.Source{SourceID: "catalog-host/0001_initial", Document: document})
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	// The role's durable domain is initialized against the complete host
	// source, exactly like the subsequently composed relation capabilities.
	f := provision(t, backend, ir.AccountExact, local)
	credential, _ := catalogAuthority(t, backend, f)
	collections, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	deleters, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := accountmodel.NewCatalog(f.store, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.AccountsGroupPermissions, deleters.AccountsGroup, deleters.AccountsPermission, auth.PrincipalAuthorizer{}, catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	actor := credential.Principal()
	permission, err := catalog.Permissions().Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("host.catalog"), "name": query.String("Host catalog")}))
	if err != nil {
		t.Fatal(err)
	}
	group, err := catalog.Groups().Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("host catalog")}, map[string][]int64{"permissions": {permission.ID}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.user.ID, permission.ID)); err != nil {
		t.Fatal(err)
	}
	note, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("host relation", permission.ID).WithGroupID(group.ID))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := hostmodels.AccessGuardObjects.Create(t.Context(), backend, hostmodels.NewAccessGuardCreate().WithGroupID(group.ID).WithPermissionID(permission.ID))
	if err != nil {
		t.Fatal(err)
	}
	before := profileRow(t, backend, f.user.ID)
	for _, target := range []string{"group", "permission"} {
		if !t.Run("protected_"+target, func(t *testing.T) {
			var result accountmodel.CatalogDeletion
			var err error
			if target == "group" {
				result, err = catalog.Groups().Delete(t.Context(), actor, group.ID, 1)
			} else {
				result, err = catalog.Permissions().Delete(t.Context(), actor, permission.ID, 1)
			}
			var protected *query.ProtectedForeignKeyError
			if !errors.As(err, &protected) || protected.ProtectedSourceRows() != 1 || result != (accountmodel.CatalogDeletion{}) {
				t.Fatal("host protection missing", err)
			}
			if got := profileRow(t, reopen(t), f.user.ID); !reflect.DeepEqual(got, before) {
				t.Fatal("protected deletion retained owner revision")
			}
			current, err := catalog.Groups().Get(t.Context(), actor, group.ID)
			if err != nil || current.Revision() != 1 {
				t.Fatal("protected deletion changed catalog owner", err)
			}
			if keys, loaded := current.View().Collection("permissions"); !loaded || len(keys) != 1 || keys[0] != permission.ID {
				t.Fatal("protected deletion changed grant link")
			}
			storedNote := catalogRow(t, hostmodels.AccessNoteObjects.Using(reopen(t)).Filter(hostmodels.AccessNoteFields.ID.Exact(note.ID)).OrderBy(hostmodels.AccessNoteFields.ID.Asc()))
			if storedNote.GroupID == nil || *storedNote.GroupID != group.ID || storedNote.PermissionID != permission.ID {
				t.Fatal("protected deletion changed host relation")
			}
			if len(catalogAudit(t, backend, actor, "group", group.ID)) != 1 || len(catalogAudit(t, backend, actor, "permission", permission.ID)) != 1 {
				t.Fatal("protected deletion appended audit")
			}
		}) {
			return
		}
	}
	if count, err := hostmodels.AccessGuardObjects.Delete(t.Context(), backend, &guard); err != nil || count != 1 {
		t.Fatal(err)
	}
	if result, err := catalog.Groups().Delete(t.Context(), actor, group.ID, 1); err != nil || result.DeletedRows < 3 {
		t.Fatal("host SET_NULL deletion", err)
	}
	storedNote := catalogRow(t, hostmodels.AccessNoteObjects.Using(reopen(t)).Filter(hostmodels.AccessNoteFields.ID.Exact(note.ID)).OrderBy(hostmodels.AccessNoteFields.ID.Asc()))
	if storedNote.GroupID != nil || storedNote.PermissionID != permission.ID || profileRow(t, reopen(t), f.user.ID).Revision != 2 {
		t.Fatal("host SET_NULL or direct owner revision missing")
	}
	if result, err := catalog.Permissions().Delete(t.Context(), actor, permission.ID, 1); err != nil || result.DeletedRows < 3 {
		t.Fatal("host CASCADE deletion", err)
	}
	if count, err := hostmodels.AccessNoteObjects.Using(reopen(t)).Count(t.Context()); err != nil || count != 0 {
		t.Fatal("host CASCADE retained row", err)
	}
	if after := profileRow(t, reopen(t), f.user.ID); after.Revision != 3 || after.EncodedPassword != before.EncodedPassword || after.PrincipalID != before.PrincipalID {
		t.Fatal("host deletion changed wrong account state")
	}
	if len(catalogAudit(t, backend, actor, "group", group.ID)) != 2 || len(catalogAudit(t, backend, actor, "permission", permission.ID)) != 2 {
		t.Fatal("host deletion audit is not atomic")
	}
}
