package model_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type untouchedCatalogBackend struct{ untouchedProfileBackend }

func (backend *untouchedCatalogBackend) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	backend.writes++
	return errors.New("unexecuted native relation transaction")
}

func unitCatalogConfig() accountmodel.CatalogConfig[models.Group, models.Permission] {
	return accountmodel.CatalogConfig[models.Group, models.Permission]{
		Group:      accountmodel.CatalogModelConfig[models.Group]{ReadFields: []string{"name", "permissions"}, WriteFields: []string{"permissions", "name"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group"}},
		Permission: accountmodel.CatalogModelConfig[models.Permission]{ReadFields: []string{"code", "name"}, WriteFields: []string{"name", "code"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission"}},
	}
}

func TestAccountCatalogCompositionAndInputRejectBeforeIO(t *testing.T) {
	binding, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedCatalogBackend{}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	policies, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	build := func(config accountmodel.CatalogConfig[models.Group, models.Permission]) (*accountmodel.Catalog[models.Group, models.Permission], error) {
		return accountmodel.NewCatalog(store, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.IdentityGroupPermissions, policies.IdentityGroup, policies.IdentityPermission, auth.PrincipalAuthorizer{}, config)
	}
	for _, mutate := range []func(*accountmodel.CatalogConfig[models.Group, models.Permission]){
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) { c.Group.ReadFields = nil },
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.ReadFields = []string{"name", "name"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.ReadFields = []string{"missing"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.WriteFields = []string{"permissions"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.WriteFields = []string{"name", "revision"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.WriteFields = []string{"name", "id"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.WriteFields = []string{"name", "name"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Permission.WriteFields = []string{"name"}
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Permission.Permissions.Delete = ""
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.Permissions.View = "invalid permission"
		},
		func(c *accountmodel.CatalogConfig[models.Group, models.Permission]) {
			c.Group.Permissions.Additional = []auth.Permission{"catalog.add_group"}
		},
	} {
		config := unitCatalogConfig()
		mutate(&config)
		if catalog, err := build(config); err == nil || catalog != nil {
			t.Fatal("invalid catalog policy admitted")
		}
	}
	hostCollections, err := hostproject.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	hostPolicies, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	for _, capabilities := range []struct {
		grants      orm.ManyToMany[models.Group, models.Permission, models.GroupPermissionsLink]
		groups      orm.RelationDeleter[models.Group]
		permissions orm.RelationDeleter[models.Permission]
	}{
		{hostCollections.AccountsGroupPermissions, policies.IdentityGroup, policies.IdentityPermission},
		{collections.IdentityGroupPermissions, hostPolicies.AccountsGroup, policies.IdentityPermission},
		{collections.IdentityGroupPermissions, policies.IdentityGroup, hostPolicies.AccountsPermission},
		{},
	} {
		catalog, err := accountmodel.NewCatalog(store, models.GroupDescriptor{}, models.PermissionDescriptor{}, capabilities.grants, capabilities.groups, capabilities.permissions, auth.PrincipalAuthorizer{}, unitCatalogConfig())
		if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) || catalog != nil {
			t.Fatal("foreign/zero full host capability admitted", err)
		}
	}
	readOnly, err := accountmodel.NewCredentialStore(&untouchedAccountBackend{}, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if catalog, err := accountmodel.NewCatalog(readOnly, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.IdentityGroupPermissions, policies.IdentityGroup, policies.IdentityPermission, auth.PrincipalAuthorizer{}, unitCatalogConfig()); err == nil || catalog != nil {
		t.Fatal("unfenced catalog backend admitted")
	}
	narrow, err := accountmodel.NewCredentialStore(&untouchedProfileBackend{}, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	if catalog, err := accountmodel.NewCatalog(narrow, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.IdentityGroupPermissions, policies.IdentityGroup, policies.IdentityPermission, auth.PrincipalAuthorizer{}, unitCatalogConfig()); err == nil || catalog != nil {
		t.Fatal("ordinary coordinated writer substituted for native relation owner")
	}
	for _, overlay := range []auth.Authorizer{nil, (*auth.PrincipalAuthorizer)(nil)} {
		if catalog, err := accountmodel.NewCatalog(store, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.IdentityGroupPermissions, policies.IdentityGroup, policies.IdentityPermission, overlay, unitCatalogConfig()); err == nil || catalog != nil {
			t.Fatal("missing catalog overlay admitted")
		}
	}
	config := unitCatalogConfig()
	catalog, err := build(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Group.ReadFields[0], config.Group.WriteFields[0] = "foreign", "revision"
	catalog.Groups().ReadFields()[0], catalog.Groups().WriteFields()[0] = "foreign", "revision"
	if !reflect.DeepEqual(catalog.Groups().ReadFields(), []string{"name", "permissions"}) || !reflect.DeepEqual(catalog.Groups().ListFields(), []string{"name"}) || !reflect.DeepEqual(catalog.Groups().WriteFields(), []string{"name", "permissions"}) {
		t.Fatal("catalog field ownership/order changed")
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := models.GroupObjects.Input(map[string]query.Value{"name": query.String("valid")})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := orm.NewManager[models.Group](otherCatalogGroupSource{}).Input(map[string]query.Value{"name": query.String("valid")})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []orm.ModelInput[models.Group]{{}, foreign}
	for _, values := range []map[string]query.Value{{"revision": query.Integer(4)}, {"name": query.String("")}, {"name": query.String("bad\x00name")}, {"name": query.String(strings.Repeat("n", 151))}} {
		value, err := models.GroupObjects.Input(values)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, value)
	}
	for _, key := range []query.Value{query.Null(), query.Integer(7)} {
		value, err := valid.WithTargetKey(key)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, value)
	}
	overflow, err := valid.WithCollections(map[string][]int64{"permissions": make([]int64, auth.MaximumPermissions+1)})
	if err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, overflow)
	for _, input := range inputs {
		if result, err := catalog.Groups().Update(t.Context(), actor, 0, 1, input); err == nil || result.Revision != 0 {
			t.Fatal("invalid catalog input reached owner")
		}
	}
	bound, err := valid.WithTargetKey(query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := catalog.Groups().Create(t.Context(), actor, bound); err == nil || result.Revision != 0 {
		t.Fatal("stored zero target became create")
	}
	for _, revision := range []int64{0, -1, math.MaxInt64} {
		if _, err := catalog.Groups().Update(t.Context(), actor, 0, revision, valid); err == nil {
			t.Fatal("invalid revision admitted")
		}
		if _, err := catalog.Groups().Delete(t.Context(), actor, 0, revision); err == nil {
			t.Fatal("invalid deletion revision admitted")
		}
	}
	for _, code := range []string{"", "UPPER.Code", "invalid permission"} {
		input, err := models.PermissionObjects.Input(map[string]query.Value{"code": query.String(code)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.Permissions().Update(t.Context(), actor, 0, 1, input); err == nil {
			t.Fatal("invalid canonical code admitted")
		}
	}
	for _, page := range [][2]int{{-1, 1}, {0, 0}, {0, accountmodel.MaximumCatalogPageSize + 1}} {
		if _, err := catalog.Groups().List(t.Context(), actor, page[0], page[1]); err == nil {
			t.Fatal("invalid page admitted")
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, canceled} {
		if value, err := catalog.Groups().Get(ctx, actor, 0); err == nil || value.Revision() != 0 {
			t.Fatal("invalid context admitted read")
		}
		if _, err := catalog.Groups().Create(ctx, actor, valid); err == nil {
			t.Fatal("invalid context admitted write")
		}
	}
	for _, resource := range []accountmodel.CatalogResource[models.Group]{{}, (*accountmodel.Catalog[models.Group, models.Permission])(nil).Groups(), new(accountmodel.Catalog[models.Group, models.Permission]).Groups()} {
		if _, err := resource.Get(t.Context(), actor, 0); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			t.Fatal("zero catalog resource admitted read", err)
		}
		if _, err := resource.Create(t.Context(), actor, valid); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) {
			t.Fatal("zero catalog resource admitted write", err)
		}
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("invalid catalog configuration/input performed I/O", backend.calls, backend.writes)
	}
	if _, err := catalog.Groups().Update(t.Context(), actor, 0, 1, valid); err == nil || backend.writes != 1 {
		t.Fatal("valid zero target did not reach transaction owner", err)
	}
}

type otherCatalogGroupSource struct{ models.GroupDescriptor }

func (otherCatalogGroupSource) Metadata() ir.Model {
	value := (models.GroupDescriptor{}).Metadata()
	value.DBTable = "another_catalog_group"
	return value
}
