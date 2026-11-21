package model_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
)

func TestFirstAccountDefinitionInspectsPolicyBeforeBackendAcquisition(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	collections, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	hasher := &creationNoHash{}
	identities := 0
	config := accountmodel.FirstAccountConfig[models.User]{CreationPolicy: accountmodel.CreationPolicy[models.User]{Fields: []string{"username", "first_name"}, PrincipalID: func(context.Context) (string, error) { identities++; return "server-id", nil }}, EvidenceSHA256: strings.Repeat("c", 64)}
	build := func(config accountmodel.FirstAccountConfig[models.User]) accountmodel.FirstAccountDefinition[models.User] {
		t.Helper()
		definition, err := accountmodel.NewFirstAccountDefinition(binding, models.GroupDescriptor{}, models.PermissionDescriptor{}, collections.IdentityUserGroups, collections.IdentityUserPermissions, hasher, config)
		if err != nil {
			t.Fatal(err)
		}
		return definition
	}
	definition := build(config)
	original := definition.Fingerprint()
	config.Fields[0] = "encoded_password"
	definition.Fields()[0] = "encoded_password"
	if !slices.Equal(definition.Fields(), []string{"username", "first_name"}) || len(original) != 64 || definition.Fingerprint() != original || definition.MaxPasswordBytes() != 1024 {
		t.Fatal("declaration does not own its selected fields/policy")
	}
	if _, err := definition.Manager().Metadata(); err != nil {
		t.Fatal(err)
	}
	config.Fields = []string{"username", "first_name"}
	config.CaseInsensitiveLogin = true
	if build(config).Fingerprint() == original {
		t.Fatal("creation uniqueness policy did not change the declaration")
	}
	if owner, err := definition.Bind(nil); err == nil || owner != nil {
		t.Fatal("declaration became an owner without a backend")
	}
	backend := &untouchedCatalogBackend{}
	first, err := definition.Bind(backend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := definition.Bind(backend)
	if err != nil || first == second || !slices.Equal(first.Fields(), definition.Fields()) {
		t.Fatal("independent bindings shared a mutable attempt owner", err)
	}
	if backend.calls != 0 || backend.writes != 0 || hasher.calls != 0 || identities != 0 {
		t.Fatal("declaration inspection or binding performed work")
	}
}
