package accountstoretest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func catalogConfig() accountmodel.CatalogConfig[models.Group, models.Permission] {
	return accountmodel.CatalogConfig[models.Group, models.Permission]{
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"},
		Group:              accountmodel.CatalogModelConfig[models.Group]{ReadFields: []string{"name", "permissions"}, WriteFields: []string{"name", "permissions"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group"}},
		Permission:         accountmodel.CatalogModelConfig[models.Permission]{ReadFields: []string{"code", "name"}, WriteFields: []string{"code", "name"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission"}},
		Now:                func() time.Time { return joined },
	}
}

type catalogFixture struct {
	account    *fixture
	credential auth.Credential
	catalog    *accountmodel.Catalog[models.Group, models.Permission]
	grants     map[auth.Permission]models.UserPermissionsLink
}

func catalogAuthority(t *testing.T, backend Backend, f *fixture) (auth.Credential, map[auth.Permission]models.UserPermissionsLink) {
	t.Helper()
	links := make(map[auth.Permission]models.UserPermissionsLink)
	config := catalogConfig()
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for _, policy := range []accountmodel.CatalogPermissions{config.Group.Permissions, config.Permission.Permissions} {
			for _, code := range []auth.Permission{policy.View, policy.Add, policy.Change, policy.Delete} {
				permission, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(string(code), string(code)))
				if err != nil {
					return err
				}
				link, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(f.user.ID, permission.ID))
				if err != nil {
					return err
				}
				links[code] = link
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	credential, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	if err != nil || !found {
		t.Fatal("catalog actor credential", err)
	}
	return credential, links
}

func provisionCatalog(t *testing.T, backend Backend) *catalogFixture {
	t.Helper()
	f := provision(t, backend, ir.AccountExact)
	credential, links := catalogAuthority(t, backend, f)
	catalog, err := project.NewAccountCatalog(catalogSessions(t, backend), auth.PrincipalAuthorizer{}, catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	return &catalogFixture{account: f, credential: credential, catalog: catalog, grants: links}
}

func catalogSessions(t *testing.T, backend sessiondb.Backend) *accountmodel.SessionStore[models.User] {
	t.Helper()
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func catalogGroupInput(t *testing.T, values map[string]query.Value, collections map[string][]int64) orm.ModelInput[models.Group] {
	t.Helper()
	input, err := models.GroupObjects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.WithCollections(collections)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func catalogPermissionInput(t *testing.T, values map[string]query.Value) orm.ModelInput[models.Permission] {
	t.Helper()
	input, err := models.PermissionObjects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func catalogRow[M any](t *testing.T, source orm.QuerySet[M]) M {
	t.Helper()
	row, present, err := source.First(t.Context())
	if err != nil || !present {
		t.Fatal("catalog fixture row absent", err)
	}
	return row
}
func catalogAudit(t *testing.T, backend Backend, actor auth.Principal, model string, id int64) []accountaudit.Entry {
	t.Helper()
	namespace, _ := actor.CredentialDomain()
	var entries []accountaudit.Entry
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		var err error
		entries, err = accountaudit.HistoryIn(t.Context(), scope, namespace, "godj_identity."+model, id, 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}

func catalogCRUD(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor, groups, permissions := f.credential.Principal(), f.catalog.Groups(), f.catalog.Permissions()
	permission, err := permissions.Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("documents.read"), "name": query.String("Read private documents")}))
	if err != nil || permission.ID == 0 || permission.Revision != 1 {
		t.Fatal("catalog permission create", err)
	}
	input := catalogGroupInput(t, map[string]query.Value{"name": query.String("Private reviewers")}, map[string][]int64{"permissions": {permission.ID, permission.ID}})
	group, err := groups.Create(t.Context(), actor, input)
	if err != nil || group.Revision != 1 || !reflect.DeepEqual(group.ChangedFields, []string{"name", "permissions"}) {
		t.Fatal("catalog group create", group, err)
	}
	view, err := groups.Get(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys, loaded := view.View().Collection("permissions")
	if !loaded || !slices.Equal(keys, []int64{permission.ID}) {
		t.Fatal("catalog detail lost selected membership")
	}
	keys[0] = 999
	if _, exposed := view.View().Get("revision"); exposed {
		t.Fatal("catalog revision became implicit selected output")
	}
	for _, value := range []any{f.catalog, *f.catalog, groups, view, &view, view.View()} {
		for _, format := range []string{"%v", "%+v", "%#v", "%p", "%w", "%x", "%q"} {
			printed := fmt.Sprintf(format, value)
			if strings.Contains(printed, "Private reviewers") || strings.Contains(printed, f.account.user.EncodedPassword) {
				t.Fatal("catalog diagnostic leaked values", format)
			}
		}
	}
	if encoded, err := json.Marshal(view); err != nil || string(encoded) != "{}" {
		t.Fatal("catalog record bypassed output policy", err)
	}
	changed, err := groups.Update(t.Context(), actor, group.ID, 1, catalogGroupInput(t, map[string]query.Value{"name": query.String("Edited reviewers")}, nil))
	if err != nil || changed.Revision != 2 || !slices.Equal(changed.ChangedFields, []string{"name"}) {
		t.Fatal("catalog scalar update", changed, err)
	}
	current, err := groups.Get(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if keys, _ := current.View().Collection("permissions"); !slices.Equal(keys, []int64{permission.ID}) {
		t.Fatal("omitted collection did not preserve current state")
	}
	if name, _ := view.View().Get("name"); !name.Equal(query.String("Private reviewers")) {
		t.Fatal("returned view did not remain an observation")
	}
	if result, err := groups.Update(t.Context(), actor, group.ID, 2, catalogGroupInput(t, nil, nil)); err != nil || result.Revision != 2 || len(result.ChangedFields) != 0 {
		t.Fatal("catalog no-op advanced revision", err)
	}
	if result, err := groups.Update(t.Context(), actor, group.ID, 1, input); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) || result.Revision != 0 {
		t.Fatal("stale catalog update succeeded", err)
	}
	for _, invalid := range []orm.ModelInput[models.Group]{
		catalogGroupInput(t, map[string]query.Value{"name": query.String(f.account.group.Name)}, nil),
		catalogGroupInput(t, nil, map[string][]int64{"permissions": {mathMaxKey}}),
	} {
		result, err := groups.Update(t.Context(), actor, group.ID, 2, invalid)
		if _, rejected := validation.Rejected(err); !rejected || result.Revision != 0 {
			t.Fatal("catalog uniqueness/choice rejection", err)
		}
	}
	if entries, err := groups.History(t.Context(), actor, group.ID, 100); err != nil || len(entries) != 2 {
		t.Fatal("catalog no-op/rejection changed audit", err)
	}
	cleared, err := groups.Update(t.Context(), actor, group.ID, 2, catalogGroupInput(t, nil, map[string][]int64{"permissions": nil}))
	if err != nil || cleared.Revision != 3 || !slices.Equal(cleared.ChangedFields, []string{"permissions"}) {
		t.Fatal("explicit catalog clear", err)
	}
	current, err = groups.Get(t.Context(), actor, group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if keys, present := current.View().Collection("permissions"); !present || len(keys) != 0 {
		t.Fatal("cleared detail became unloaded")
	}
	if _, err := groups.Update(t.Context(), actor, group.ID, 3, catalogGroupInput(t, nil, map[string][]int64{"permissions": {permission.ID}})); err != nil {
		t.Fatal(err)
	}
	other, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("catalog-indirect", "catalog-indirect", f.account.user.EncodedPassword, joined))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.account.user.ID, other.ID} {
		if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(id, group.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.account.user.ID, permission.ID)); err != nil {
		t.Fatal(err)
	}
	before, found, err := f.account.store.CredentialByID(t.Context(), other.PrincipalID)
	requireGrants(t, before, found, err, "documents.read")
	renamed, err := permissions.Update(t.Context(), actor, permission.ID, 1, catalogPermissionInput(t, map[string]query.Value{"code": query.String("documents.inspect")}))
	if err != nil || renamed.ID != permission.ID || renamed.Revision != 2 {
		t.Fatal("permission code did not retain identity", err)
	}
	after, found, err := f.account.store.CredentialByID(t.Context(), other.PrincipalID)
	requireGrants(t, after, found, err, "documents.inspect")
	if after.SessionStamp() != before.SessionStamp() {
		t.Fatal("catalog code changed session credential stamp")
	}
	deleted, err := permissions.Delete(t.Context(), actor, permission.ID, 2)
	if err != nil || deleted.Revision != 2 || deleted.DeletedRows < 3 {
		t.Fatal("permission deletion", deleted, err)
	}
	storedActor, storedOther := profileRow(t, reopen(t), f.account.user.ID), profileRow(t, reopen(t), other.ID)
	storedGroup := catalogRow(t, models.GroupObjects.Using(backend).Filter(models.GroupFields.ID.Exact(group.ID)).OrderBy(models.GroupFields.ID.Asc()))
	if storedActor.Revision != 2 || storedOther.Revision != 1 || storedGroup.Revision != 5 {
		t.Fatal("permission deletion advanced the wrong owners", storedActor.Revision, storedOther.Revision, storedGroup.Revision)
	}
	if entries := catalogAudit(t, backend, actor, "group", group.ID); len(entries) != 4 {
		t.Fatal("permission deletion created a separate group edit audit")
	}
	deleted, err = groups.Delete(t.Context(), actor, group.ID, 5)
	if err != nil || deleted.DeletedRows < 3 {
		t.Fatal("group deletion", err)
	}
	storedActor, storedOther = profileRow(t, reopen(t), f.account.user.ID), profileRow(t, reopen(t), other.ID)
	if storedActor.Revision != 3 || storedOther.Revision != 2 || storedActor.EncodedPassword != f.account.user.EncodedPassword || storedOther.EncodedPassword != other.EncodedPassword || storedActor.PrincipalID != f.account.user.PrincipalID {
		t.Fatal("group deletion changed wrong account state")
	}
	currentCredential, found, err := f.account.store.CredentialByID(t.Context(), f.account.user.PrincipalID)
	if err != nil || !found || currentCredential.SessionStamp() != f.credential.SessionStamp() {
		t.Fatal("catalog owner revisions changed session stamp", err)
	}
	if result, err := groups.Get(t.Context(), actor, group.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFound}) || result.Revision() != 0 {
		t.Fatal("deleted group published a profile", err)
	}
	if entries := catalogAudit(t, backend, actor, "group", group.ID); len(entries) != 5 || entries[4].Action() != admin.ActionDelete {
		t.Fatal("group delete audit missing")
	}
	if entries := catalogAudit(t, backend, actor, "permission", permission.ID); len(entries) != 3 {
		t.Fatal("permission audit lifecycle differs")
	}
	page, err := groups.List(t.Context(), actor, 0, 100)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID() != f.account.group.ID {
		t.Fatal("catalog scalar page", err)
	}
	if _, loaded := page.Items[0].View().Collection("permissions"); loaded {
		t.Fatal("catalog list implicitly loaded each grant set")
	}
}

const mathMaxKey = int64(1<<63 - 1)

func catalogNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provisionCatalog(t, backend)
	actor, groups, permissions := f.credential.Principal(), f.catalog.Groups(), f.catalog.Permissions()
	for _, pair := range [][2]int64{{0, -17}, {-17, 0}} {
		permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(fmt.Sprintf("signed.key%d", -pair[1]), "Stored signed key"))
		if err != nil {
			t.Fatal(err)
		}
		storedKey(t, backend, "permission", permission.ID, pair[1])
		(models.PermissionDescriptor{}).SetPrimaryKey(&permission, pair[1])
		group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate(fmt.Sprintf("signed-%d", pair[0])))
		if err != nil {
			t.Fatal(err)
		}
		storedKey(t, backend, "group", group.ID, pair[0])
		(models.GroupDescriptor{}).SetPrimaryKey(&group, pair[0])
		input := catalogGroupInput(t, map[string]query.Value{"name": query.String("updated signed group")}, map[string][]int64{"permissions": {permission.ID}})
		input, err = input.WithTargetKey(query.Integer(group.ID))
		if err != nil {
			t.Fatal(err)
		}
		result, err := groups.Update(t.Context(), actor, group.ID, 1, input)
		if err != nil || result.ID != group.ID || result.Revision != 2 {
			t.Fatal("signed catalog owner update", err)
		}
		view, err := groups.Get(t.Context(), actor, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		if keys, present := view.View().Collection("permissions"); !present || !slices.Equal(keys, []int64{permission.ID}) {
			t.Fatal("signed catalog target selection changed")
		}
		if history, err := groups.History(t.Context(), actor, group.ID, 100); err != nil || len(history) != 1 {
			t.Fatal("signed catalog history", err)
		}
		if _, err := groups.Delete(t.Context(), actor, group.ID, 2); err != nil {
			t.Fatal("signed catalog delete", err)
		}
		if _, err := permissions.Delete(t.Context(), actor, permission.ID, 1); err != nil {
			t.Fatal("signed permission delete", err)
		}
	}
}
