package consumer

import (
	"testing"
	"time"

	"example.com/godj-project-bundle/models"
	"example.com/godj-project-bundle/project"
)

func TestDefaultAccountBindingOwnsTypedRows(t *testing.T) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	role, err := binding.Role()
	if err != nil || role.Model.AppLabel != "godj_identity" || role.Model.ModelName != "user" || role.Login != "username" || role.Staff.Field != "staff" || role.Staff.Constant != nil {
		t.Fatal("built-in model was not explicitly bound", err)
	}
	backend := accountDatabase(t, models.GoDjRelationSchema())
	hasher, encoded := accountPassword(t)
	objects, err := binding.Objects()
	if err != nil {
		t.Fatal(err)
	}
	joined := time.Date(2026, time.October, 24, 0, 0, 0, 0, time.UTC)
	row := createBoundAccount(t, backend, binding, models.NewUserCreate("user-1", "alice", encoded, joined).WithFirstName("Alice"))
	found, exists, err := objects.Using(backend).Filter(models.UserFields.Username.Exact("alice")).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !exists || found.ID != row.ID || found.FirstName != "Alice" || !found.DateJoined.Equal(joined) {
		t.Fatal("default binding did not use its declared typed model", err)
	}
	credential := assertStoredAccount(t, &storedAccount[models.User]{backend: backend, binding: binding,
		byLogin: models.UserFields.Username.Exact, byPrincipal: models.UserFields.PrincipalID.Exact, order: models.UserFields.ID.Asc()}, hasher, "alice", "user-1")
	if credential.Principal().Staff() || credential.Principal().Superuser() {
		t.Fatal("default account unexpectedly changed its stored roles")
	}
}
