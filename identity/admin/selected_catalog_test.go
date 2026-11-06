package identityadmin

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type catalogRegistrationProbe struct{ calls int }

func (probe *catalogRegistrationProbe) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	probe.calls++
	return errors.New("unexpected registration read")
}
func (probe *catalogRegistrationProbe) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	probe.calls++
	return errors.New("unexpected registration write")
}
func (probe *catalogRegistrationProbe) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	probe.calls++
	return errors.New("unexpected registration relation write")
}

func TestSelectedCatalogRegistrationPreservesExposureAndPolicyBeforeIO(t *testing.T) {
	probe := &catalogRegistrationProbe{}
	store, err := project.NewAccountSessionStore(probe, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := apps.New(project.InstalledApps())
	if err != nil {
		t.Fatal(err)
	}
	policy := accountmodel.CatalogPermissions{View: "catalog.view", Add: "catalog.add", Change: "catalog.change", Delete: "catalog.delete", Additional: []auth.Permission{"catalog.manage"}}
	config := accountmodel.CatalogConfig[models.Group, models.Permission]{
		Group:              accountmodel.CatalogModelConfig[models.Group]{ReadFields: []string{"name", "permissions"}, WriteFields: []string{"name", "permissions"}, Permissions: policy},
		Permission:         accountmodel.CatalogModelConfig[models.Permission]{ReadFields: []string{"code", "name"}, WriteFields: []string{"code", "name"}, Permissions: policy},
		AccountPermissions: accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"},
	}
	owner, err := project.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	presentation := CatalogRegistration{Groups: CatalogPresentation{Slug: "teams", ListFields: []string{"id", "name"}}, Permissions: CatalogPresentation{Slug: "grants"}}
	builder := admin.NewBuilder(installed)
	if err := RegisterCatalog(builder, owner, presentation); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	presentation.Groups.ListFields[0] = "forged"
	group, present := registry.Lookup("godj_identity", "group")
	if !present || group.Slug != "teams" || !slices.Equal(group.ListFields, []string{"id", "name"}) || !slices.Equal(group.AdditionalPermissions, []auth.Permission{"catalog.manage"}) || len(group.FormFields) != 2 || group.RevisionField != "revision" {
		t.Fatal("registration lost selected model/policy")
	}
	for _, mode := range []string{"nil_owner", "nil_builder", "unreadable_form", "missing_label", "collection_column", "unknown_column", "duplicate_slug", "missing_slug"} {
		t.Run(mode, func(t *testing.T) {
			selected := owner
			builder := admin.NewBuilder(installed)
			registration := CatalogRegistration{Groups: CatalogPresentation{Slug: "teams"}, Permissions: CatalogPresentation{Slug: "grants"}}
			switch mode {
			case "nil_owner":
				selected = nil
			case "nil_builder":
				builder = nil
			case "unreadable_form", "missing_label":
				copy := config
				copy.Group.ReadFields = []string{"name"}
				if mode == "missing_label" {
					copy.Group.ReadFields = []string{"permissions"}
				}
				var err error
				selected, err = project.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, copy)
				if err != nil {
					t.Fatal(err)
				}
			case "collection_column":
				registration.Groups.ListFields = []string{"permissions"}
			case "unknown_column":
				registration.Groups.ListFields = []string{"hidden"}
			case "duplicate_slug":
				registration.Permissions.Slug = "teams"
			case "missing_slug":
				registration.Groups.Slug = ""
			}
			if err := RegisterCatalog(builder, selected, registration); err == nil {
				t.Fatal("invalid catalog registration published")
			}
		})
	}
	if probe.calls != 0 {
		t.Fatal("pure registration performed I/O")
	}
}
