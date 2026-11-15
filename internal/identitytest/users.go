package identitytest

import (
	"testing"

	hostdef "github.com/progresshans/godj/conformance/identityfixture/modeldef"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

func managementHostSources(t *testing.T) []definition.Source {
	t.Helper()
	schema, err := hostdef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]migrations.Operation, len(schema.Models))
	for index, model := range schema.Models {
		operations[index] = migrations.CreateModel{AppLabel: schema.AppLabel, Model: model}
	}
	document, err := definition.Encode(definition.Producer{Name: "identity-management-host", Version: "1"}, migrations.Migration{App: schema.AppLabel, Name: "0001_initial", Dependencies: []migrations.MigrationKey{identity.InitialMigrationKey()}, Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	return append(systemstate.IdentityMigrationSources(), definition.Source{SourceID: "management-host/0001_initial", Document: document})
}

func managementHost(t *testing.T, backend TransitionBackend) hostproject.RelationDeleters {
	t.Helper()
	applyIdentitySources(t, backend, managementHostSources(t)...)
	policy, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func managementUserSelections(t *testing.T, f *historicalIdentityFixture) (models.Group, models.Permission, models.Permission) {
	t.Helper()
	view, found, err := models.PermissionObjects.Using(f.backend).Filter(models.PermissionFields.Code.Exact("helpdesk.ticket.view")).OrderBy(models.PermissionFields.ID.Asc()).First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	change, err := models.PermissionObjects.Create(t.Context(), f.backend, models.NewPermissionCreate("helpdesk.ticket.change", "Change ticket"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := models.GroupObjects.Create(t.Context(), f.backend, models.NewGroupCreate("Editors"))
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range []models.Permission{view, change} {
		if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), f.backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
			t.Fatal(err)
		}
	}
	return group, view, change
}
