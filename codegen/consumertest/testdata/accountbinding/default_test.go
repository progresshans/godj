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
	objects, err := binding.Objects()
	if err != nil {
		t.Fatal(err)
	}
	joined := time.Date(2026, time.October, 24, 0, 0, 0, 0, time.UTC)
	row, err := objects.Create(t.Context(), backend, models.NewUserCreate("user-1", "alice", "!", joined).WithFirstName("Alice"))
	if err != nil {
		t.Fatal(err)
	}
	found, exists, err := objects.Using(backend).Filter(models.UserFields.Username.Exact("alice")).First(t.Context())
	if err != nil || !exists || found.ID != row.ID || found.FirstName != "Alice" || !found.DateJoined.Equal(joined) {
		t.Fatal("default binding did not use its declared typed model", err)
	}
}
