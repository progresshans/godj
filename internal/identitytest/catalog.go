package identitytest

import (
	"context"
	"testing"

	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity/models"
)

func catalogRows[T any](t *testing.T, objects interface {
	All(context.Context) ([]T, error)
}) []T {
	t.Helper()
	rows, err := objects.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func catalogSnapshot(t *testing.T, f *managementFixture) []any {
	t.Helper()
	sessions, audit := snapshotIdentitySystemRows(t, f.backend)
	return []any{
		catalogRows(t, models.UserObjects.Using(f.backend).OrderBy(models.UserFields.ID.Asc())),
		catalogRows(t, models.GroupObjects.Using(f.backend).OrderBy(models.GroupFields.ID.Asc())),
		catalogRows(t, models.PermissionObjects.Using(f.backend).OrderBy(models.PermissionFields.ID.Asc())),
		catalogRows(t, models.UserGroupsLinkObjects.Using(f.backend).OrderBy(models.UserGroupsLinkFields.ID.Asc())),
		catalogRows(t, models.UserPermissionsLinkObjects.Using(f.backend).OrderBy(models.UserPermissionsLinkFields.ID.Asc())),
		catalogRows(t, models.GroupPermissionsLinkObjects.Using(f.backend).OrderBy(models.GroupPermissionsLinkFields.ID.Asc())),
		catalogRows(t, hostmodels.AccessNoteObjects.Using(f.backend).OrderBy(hostmodels.AccessNoteFields.ID.Asc())),
		catalogRows(t, hostmodels.AccessGuardObjects.Using(f.backend).OrderBy(hostmodels.AccessGuardFields.ID.Asc())),
		sessions, audit,
	}
}

func RunCatalogManagement(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	runCatalogBoundaries(t, open)
}
