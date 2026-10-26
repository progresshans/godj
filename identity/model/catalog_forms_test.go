package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func TestCatalogFormAndChoiceDeclarationsRetainSourceBeforeIO(t *testing.T) {
	backend := &untouchedCatalogBackend{}
	sessions, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config := unitCatalogConfig()
	config.Group.Permissions.Additional = []auth.Permission{"catalog.extra"}
	catalog, err := project.NewAccountCatalog(sessions, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	config.Group.Permissions.Additional[0] = "forged.permission"
	description, err := catalog.Groups().Description()
	if err != nil || description.Identity.ModelName != "group" || description.KeyField != "id" || description.LabelField != "name" || description.RevisionField != "revision" || description.CollectionField != "permissions" || description.Permissions.Additional[0] != "catalog.extra" {
		t.Fatal("catalog description lost owned role policy", err)
	}
	description.Permissions.Additional[0] = "forged.permission"
	again, err := catalog.Groups().Description()
	if err != nil || again.Permissions.Additional[0] != "catalog.extra" {
		t.Fatal("description aliases catalog")
	}
	for _, action := range []admin.Action{admin.ActionAdd, admin.ActionChange} {
		if source, err := catalog.GroupPermissionChoiceSource(action); err != nil || source == nil {
			t.Fatal("valid choice declaration performed I/O", err)
		}
	}
	if source, err := catalog.GroupPermissionChoiceSource(admin.ActionDelete); err == nil || source != nil {
		t.Fatal("delete declared mutable choices")
	}
	var missing *accountmodel.Catalog[models.Group, models.Permission]
	if _, err := missing.Groups().Description(); err == nil {
		t.Fatal("zero catalog described")
	}
	if _, err := missing.GroupPermissionChoiceSource(admin.ActionAdd); err == nil {
		t.Fatal("zero catalog supplied choices")
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "actor", Active: true, Permissions: []auth.Permission{"catalog.add_group", "catalog.change_group", "catalog.extra"}})
	if err != nil {
		t.Fatal(err)
	}
	resource := catalog.Groups()
	for _, mode := range []string{"zero", "foreign_model", "wrong_key", "wrong_revision", "create_existing", "nil_context", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			form, err := formmodel.NewSpecForFields(models.GroupDescriptor{}.Metadata(), []string{"name"})
			if err != nil {
				t.Fatal(err)
			}
			current := models.Group{ID: 7, Name: "Before", Revision: 2}
			(models.GroupDescriptor{}).SetPrimaryKey(&current, 7)
			bound, err := formmodel.BindInstance(t.Context(), models.GroupObjects, form, forms.NewData(map[string][]string{"name": {"Changed"}}), &current, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			candidate := bound.BoundForm()
			ctx, id, revision := t.Context(), int64(7), int64(2)
			switch mode {
			case "zero":
				candidate = formmodel.BoundForm{}
			case "foreign_model":
				other, err := formmodel.NewSpecForFields(models.PermissionDescriptor{}.Metadata(), []string{"code", "name"})
				if err != nil {
					t.Fatal(err)
				}
				foreign, err := formmodel.BindInstance(t.Context(), models.PermissionObjects, other, forms.NewData(map[string][]string{"code": {"catalog.code"}, "name": {"Name"}}), nil, formmodel.PostClean{})
				if err != nil {
					t.Fatal(err)
				}
				candidate = foreign.BoundForm()
			case "wrong_key":
				id++
			case "wrong_revision":
				revision++
			case "nil_context":
				ctx = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if mode == "create_existing" {
				err = resource.CheckCreateForm(ctx, actor, candidate)
			} else {
				err = resource.CheckChangeForm(ctx, actor, id, revision, candidate)
			}
			if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidInput}) {
				t.Fatal("foreign/invalid form reached reader", err)
			}
		})
	}
	if backend.calls != 0 || backend.writes != 0 {
		t.Fatal("pure declaration or invalid form performed I/O")
	}
}
