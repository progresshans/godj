package accountstoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func catalogDeniedOperations[M any](t *testing.T, resource accountmodel.CatalogResource[M], actor auth.Principal, id, revision int64, input orm.ModelInput[M], cause error) {
	t.Helper()
	for _, operation := range []string{"get", "list", "history", "create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			var err error
			switch operation {
			case "get":
				value, failure := resource.Get(t.Context(), actor, id)
				err = failure
				if value.Revision() != 0 || value.View().Values() != nil {
					t.Fatal("denied read published data")
				}
			case "list":
				value, failure := resource.List(t.Context(), actor, 0, 100)
				err = failure
				if value.Items != nil || value.Total != 0 {
					t.Fatal("denied list published page")
				}
			case "history":
				value, failure := resource.History(t.Context(), actor, id, 100)
				err = failure
				if value != nil {
					t.Fatal("denied history published entries")
				}
			case "create", "update":
				var value accountmodel.CatalogChange
				if operation == "create" {
					value, err = resource.Create(t.Context(), actor, input)
				} else {
					value, err = resource.Update(t.Context(), actor, id, revision, input)
				}
				if value.ID != 0 || value.Revision != 0 || value.ChangedFields != nil {
					t.Fatal("denied write published metadata")
				}
			case "delete":
				value, failure := resource.Delete(t.Context(), actor, id, revision)
				err = failure
				if value != (accountmodel.CatalogDeletion{}) {
					t.Fatal("denied delete published metadata")
				}
			}
			if !errors.Is(err, cause) {
				t.Fatal("catalog authority failure differs", err)
			}
		})
	}
}

func catalogAuthorityAndDomain(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor, groups := f.credential.Principal(), f.catalog.Groups()
	group, err := groups.Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("authority target")}, nil))
	if err != nil {
		t.Fatal(err)
	}
	permission, err := f.catalog.Permissions().Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("authority.target"), "name": query.String("Authority target")}))
	if err != nil {
		t.Fatal(err)
	}
	config := catalogConfig()
	config.Group.Permissions.Additional = []auth.Permission{"account.shared"}
	var observed []auth.Permission
	reader, err := project.NewAccountCatalog(catalogSessions(t, backend), overlayFunc(func(_ context.Context, current auth.Principal, permission auth.Permission) (bool, error) {
		if current.ID() != actor.ID() {
			return false, errors.New("wrong catalog actor")
		}
		observed = append(observed, permission)
		return permission != config.Group.Permissions.View, nil
	}), config)
	if err != nil {
		t.Fatal(err)
	}
	config.Group.Permissions.Additional[0] = "account.absent"
	if _, err := reader.Groups().Get(t.Context(), actor, group.ID); err != nil || !reflect.DeepEqual(observed, []auth.Permission{"catalog.view_group", "catalog.change_group", "account.shared"}) {
		t.Fatal("catalog read alternative or owned conjunction changed", observed, err)
	}
	missing, err := project.NewAccountCatalog(catalogSessions(t, backend), auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := missing.Groups().Get(t.Context(), actor, group.ID); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("catalog alternative bypassed required grant", err)
	}
	for _, accepted := range []bool{false, true} {
		calls, fault := 0, errors.New("private catalog overlay failure")
		failing, err := project.NewAccountCatalog(catalogSessions(t, backend), overlayFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) {
			calls++
			return accepted, errors.Join(fault, &accountmodel.Error{Code: accountmodel.CodePermission})
		}), catalogConfig())
		if err != nil {
			t.Fatal(err)
		}
		if result, err := failing.Groups().Get(t.Context(), actor, group.ID); !errors.Is(err, fault) || result.Revision() != 0 || calls != 1 || strings.Contains(fmt.Sprintf("%+v", err), "private") {
			t.Fatal("catalog overlay error fell back or leaked", err)
		}
	}
	groupInput := catalogGroupInput(t, map[string]query.Value{"name": query.String("should never persist")}, nil)
	permissionInput := catalogPermissionInput(t, map[string]query.Value{"code": query.String("authority.denied"), "name": query.String("Should never persist")})
	denyAll := func(t *testing.T, principal auth.Principal, cause error) {
		t.Helper()
		t.Run("group", func(t *testing.T) { catalogDeniedOperations(t, groups, principal, group.ID, 1, groupInput, cause) })
		t.Run("permission", func(t *testing.T) {
			catalogDeniedOperations(t, f.catalog.Permissions(), principal, permission.ID, 1, permissionInput, cause)
		})
	}
	denied := &accountmodel.Error{Code: accountmodel.CodePermission}
	writer := reopen(t)
	for code, link := range f.grants {
		if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), writer, &link); err != nil || count != 1 {
			t.Fatal("revoke catalog grant", code, err)
		}
	}
	t.Run("revoked", func(t *testing.T) { denyAll(t, actor, denied) })
	for code, link := range f.grants {
		restored, err := models.UserPermissionsLinkObjects.Create(t.Context(), writer, models.NewUserPermissionsLinkCreate(f.account.user.ID, link.TargetID))
		if err != nil {
			t.Fatal(err)
		}
		f.grants[code] = restored
	}
	if _, err := models.UserObjects.Patch(t.Context(), writer, f.account.user, models.UserPatch{}.WithActive(false).WithRevision(2)); err != nil {
		t.Fatal(err)
	}
	t.Run("inactive", func(t *testing.T) { denyAll(t, actor, denied) })
	if _, err := models.UserObjects.Patch(t.Context(), writer, f.account.user, models.UserPatch{}.WithActive(true).WithRevision(3)); err != nil {
		t.Fatal(err)
	}
	fingerprint, _ := f.account.binding.RoleFingerprint()
	digest := sha256.Sum256([]byte("catalog account ownership transition"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		_, err := domain.TransitionIn(t.Context(), scope, f.account.ownership, change, joined.Add(time.Hour))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("old_domain", func(t *testing.T) { denyAll(t, actor, denied) })
	fresh, found, err := f.account.store.CredentialByID(t.Context(), f.account.user.PrincipalID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if entries, err := groups.History(t.Context(), fresh.Principal(), group.ID, 100); err != nil || len(entries) != 0 {
		t.Fatal("old-domain group audit inherited", err)
	}
	if entries, err := f.catalog.Permissions().History(t.Context(), fresh.Principal(), permission.ID, 100); err != nil || len(entries) != 0 {
		t.Fatal("old-domain permission audit inherited", err)
	}
	if result, err := groups.Update(t.Context(), fresh.Principal(), group.ID, 1, catalogGroupInput(t, map[string]query.Value{"name": query.String("new domain target")}, nil)); err != nil || result.Revision != 2 {
		t.Fatal("fresh domain catalog update failed", err)
	}
	if history := catalogAudit(t, reopen(t), fresh.Principal(), "group", group.ID); len(history) != 1 {
		t.Fatal("new-domain audit missing")
	}
	stored := catalogRow(t, models.PermissionObjects.Using(reopen(t)).Filter(models.PermissionFields.ID.Exact(permission.ID)).OrderBy(models.PermissionFields.ID.Asc()))
	if stored.Revision != 1 || stored.Code != "authority.target" {
		t.Fatal("denied operation changed permission")
	}
	if count, err := models.GroupObjects.Using(reopen(t)).Count(t.Context()); err != nil || count != 2 {
		t.Fatal("denied create persisted group", err)
	}
}
