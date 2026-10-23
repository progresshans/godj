package model_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
)

func TestAccountProfileReadPolicyAndInputRejectBeforeIO(t *testing.T) {
	binding, err := accountmodel.Bind(modeldef.AccountRole(), models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	backend := &untouchedAccountBackend{}
	store, err := accountmodel.NewCredentialStore(backend, binding, models.GroupDescriptor{}, models.PermissionDescriptor{})
	if err != nil {
		t.Fatal(err)
	}
	config := accountmodel.ProfileReadConfig{Fields: []string{"first_name", "date_joined"}, ViewPermission: "account.view", ChangePermission: "account.change"}
	for _, fields := range [][]string{nil, {"first_name", "first_name"}, {"encoded_password"}, {"principal_id"}, {"groups", "groups"}, {"missing"}} {
		invalid := config
		invalid.Fields = fields
		if reader, err := accountmodel.NewProfileReader(store, auth.PrincipalAuthorizer{}, invalid); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) || reader != nil {
			t.Fatal("invalid profile read selection accepted", fields, err)
		}
	}
	for _, invalid := range []accountmodel.ProfileReadConfig{
		{Fields: config.Fields},
		{Fields: config.Fields, ViewPermission: "BAD permission"},
		{Fields: config.Fields, ViewPermission: "account.view", ChangePermission: "account.view"},
		{Fields: config.Fields, ViewPermission: "account.view", AdditionalPermissions: []auth.Permission{"account.view"}},
		{Fields: config.Fields, ViewPermission: "account.view", ChangePermission: "account.change", AdditionalPermissions: []auth.Permission{"account.change"}},
	} {
		if reader, err := accountmodel.NewProfileReader(store, auth.PrincipalAuthorizer{}, invalid); err == nil || reader != nil {
			t.Fatal("invalid read permission policy accepted", err)
		}
	}
	if reader, err := accountmodel.NewProfileReader(store, nil, config); err == nil || reader != nil {
		t.Fatal("nil read overlay accepted")
	}
	if reader, err := accountmodel.NewProfileReader[models.User](nil, auth.PrincipalAuthorizer{}, config); err == nil || reader != nil {
		t.Fatal("nil account store accepted")
	}
	selected, err := accountmodel.NewProfileReader(store, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: []string{"groups", "permissions"}, ViewPermission: "account.view"})
	if err != nil || len(selected.ListFields()) != 0 || !reflect.DeepEqual(selected.Fields(), []string{"groups", "permissions"}) {
		t.Fatal("explicit collection-only detail selection", err)
	}
	reader, err := accountmodel.NewProfileReader(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal("read-only backend should be sufficient", err)
	}
	config.Fields[0] = "encoded_password"
	selection := reader.Fields()
	selection[0] = "encoded_password"
	if !reflect.DeepEqual(reader.Fields(), []string{"first_name", "date_joined"}) {
		t.Fatal("read selection borrowed caller memory")
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*accountmodel.ProfileReader[models.User]{nil, {}} {
		if value, err := candidate.Get(t.Context(), actor, 0); err == nil || value.Revision() != 0 {
			t.Fatal("zero reader admitted")
		}
		if value, err := candidate.List(t.Context(), actor, 0, 1); err == nil || value.Items != nil {
			t.Fatal("zero list reader admitted")
		}
		if value, err := candidate.History(t.Context(), actor, 0, 1); err == nil || value != nil {
			t.Fatal("zero history reader admitted")
		}
	}
	if value, err := reader.Get(nil, actor, 0); err == nil || value.Revision() != 0 {
		t.Fatal("nil read context admitted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if value, err := reader.Get(ctx, actor, 0); !errors.Is(err, context.Canceled) || value.Revision() != 0 {
		t.Fatal("canceled read admitted", err)
	}
	if value, err := reader.Get(t.Context(), auth.Anonymous(), 0); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) || value.Revision() != 0 {
		t.Fatal("anonymous read admitted", err)
	}
	for _, page := range [][2]int{{-1, 1}, {0, 0}, {0, 101}} {
		if value, err := reader.List(t.Context(), actor, page[0], page[1]); err == nil || value.Items != nil {
			t.Fatal("invalid page admitted", err)
		}
	}
	for _, limit := range []int{0, 101} {
		if value, err := reader.History(t.Context(), actor, 0, limit); err == nil || value != nil {
			t.Fatal("invalid history selection admitted", err)
		}
	}
	if backend.calls != 0 {
		t.Fatal("invalid profile read performed I/O", backend.calls)
	}
}
