package consumer

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"example.com/godj-project-bundle/models"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
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
	// Direct, group-only and overlapping grants must form one bounded set.
	store, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	grants := make([]models.Permission, 3)
	for i, code := range []string{"account.direct", "account.shared", "account.team"} {
		grants[i], err = models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(code, code))
		if err != nil {
			t.Fatal(err)
		}
	}
	team, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("operators"))
	if err != nil {
		t.Fatal(err)
	}
	direct, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(row.ID, grants[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(row.ID, grants[1].ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(row.ID, team.ID)); err != nil {
		t.Fatal(err)
	}
	for _, grant := range grants[1:] {
		if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(team.ID, grant.ID)); err != nil {
			t.Fatal(err)
		}
	}
	before, foundAccount, err := store.CredentialByUsername(t.Context(), "alice")
	expected := []auth.Permission{"account.direct", "account.shared", "account.team"}
	if err != nil || !foundAccount || !reflect.DeepEqual(before.Principal().Permissions(), expected) {
		t.Fatal("effective permission union", err)
	}
	if count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &direct); err != nil || count != 1 {
		t.Fatal("direct permission removal", err)
	}
	after, foundAccount, err := store.CredentialByID(t.Context(), "user-1")
	if err != nil || !foundAccount || !reflect.DeepEqual(after.Principal().Permissions(), expected[1:]) || !reflect.DeepEqual(before.Principal().Permissions(), expected) {
		t.Fatal("fresh permissions or detached snapshot", err)
	}
	scope := &trackedSnapshots{backend: backend}
	authenticator, err := project.NewAccountAuthenticator(t.Context(), scope, outsideSnapshotHasher{hasher, &scope.reading})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := authenticator.Authenticate(t.Context(), "ａｌｉｃｅ", accountPasswordText)
	if !errors.Is(err, auth.ErrInvalidCredentials) || normalized.SessionStamp() != "" {
		t.Fatal("exact login policy normalized input", err)
	}
	actor, err := authenticator.Authenticate(t.Context(), "alice", accountPasswordText)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(tx db.Session) error {
		current, allowed, err := store.AuthorizeIn(t.Context(), tx, actor.Principal(), auth.PrincipalAuthorizer{}, "account.team")
		if err != nil {
			return err
		}
		if !allowed || current.ID() != "user-1" {
			return errors.New("current account could not authorize a typed profile mutation")
		}
		_, err = models.UserObjects.Patch(t.Context(), tx, row, models.UserPatch{}.WithFirstName("Updated Alice").WithRevision(2))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	edited, present, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(row.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !present || edited.FirstName != "Updated Alice" || edited.Revision != 2 {
		t.Fatal("authorized typed profile mutation did not commit", err)
	}
	credential := assertStoredAccount(t, backend, binding, authenticator, "alice", "user-1")
	if credential.Principal().Staff() || credential.Principal().Superuser() {
		t.Fatal("default account unexpectedly changed its stored roles")
	}
}
