package model

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestRelatedLifecyclePermissionsDoNotInventMissingOperationGrants(t *testing.T) {
	if grants, err := lifecycleRequiredPermissions("", []auth.Permission{"account.extra"}, false); err != nil || len(grants) != 0 {
		t.Fatal("additional permission invented an absent effect operation", err)
	}
	if _, err := lifecycleRequiredPermissions("", nil, true); err == nil {
		t.Fatal("required deletion operation accepted an absent grant")
	}
	if _, err := lifecycleRequiredPermissions("", []auth.Permission{"not a permission"}, false); err == nil {
		t.Fatal("omitted operation hid malformed additional permissions")
	}
	if grants, err := lifecycleRequiredPermissions("account.change", []auth.Permission{"account.extra"}, false); err != nil || len(grants) != 2 {
		t.Fatal("explicit related operation lost its full permission policy", err)
	}
}

func TestSelectedModelThroughStorageRequiresIdentityCreatingOwnership(t *testing.T) {
	role := ir.AccountRole{Model: ir.ModelIdentity{AppLabel: "people", ModelName: "member"}, Catalog: ir.AccountCatalog{
		Group: ir.ModelIdentity{AppLabel: "access", ModelName: "team"}, Permission: ir.ModelIdentity{AppLabel: "access", ModelName: "grant"}}}
	for _, model := range []ir.ModelIdentity{role.Model, role.Catalog.Group, role.Catalog.Permission} {
		if err := validateLifecycleThroughStorage(role, model); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
			t.Fatal("generic through defaults gained selected-model creation authority", err)
		}
	}
	if err := validateLifecycleThroughStorage(role, ir.ModelIdentity{AppLabel: "links", ModelName: "membership"}); err != nil {
		t.Fatal("ordinary through storage was rejected", err)
	}
}
