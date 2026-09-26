package identitytest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

// RunDirectoryValidation uses stored rows, including rows written outside the
// identity service, so rejection cannot depend on cooperative mutation APIs.
func RunDirectoryValidation(t *testing.T, backend DirectoryBackend) {
	t.Helper()
	ctx := t.Context()
	loaded, _, err := definition.Load(identity.MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	directory, err := identity.NewDirectory(backend)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	createUser := func(name string, alter func(models.UserCreate) models.UserCreate) models.User {
		t.Helper()
		input := models.NewUserCreate(name, name, "private-stored-credential", when)
		if alter != nil {
			input = alter(input)
		}
		user, err := models.UserObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatal(err)
		}
		return user
	}
	group, err := models.GroupObjects.Create(ctx, backend, models.NewGroupCreate("Validation group"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.view", "View"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.GroupPermissionsLinkObjects.Create(ctx, backend, models.NewGroupPermissionsLinkCreate(group.ID, view.ID)); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"none", "direct", "group", "overlap"} {
		t.Run(mode, func(t *testing.T) {
			user := createUser(mode, nil)
			if mode == "direct" || mode == "overlap" {
				if _, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(user.ID, view.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "group" || mode == "overlap" {
				if _, err := models.UserGroupsLinkObjects.Create(ctx, backend, models.NewUserGroupsLinkCreate(user.ID, group.ID)); err != nil {
					t.Fatal(err)
				}
			}
			account, found, err := directory.ByUsername(ctx, mode)
			if err != nil || !found || account.Profile().ID != user.ID {
				t.Fatal("stored identity lookup failed", found, err)
			}
			if mode == "none" {
				if len(account.Permissions()) != 0 {
					t.Fatal("unassigned group granted permissions")
				}
			} else if !reflect.DeepEqual(account.Permissions(), []auth.Permission{"helpdesk.ticket.view"}) {
				t.Fatal("direct/group union duplicated or lost grants", account.Permissions())
			}
		})
	}
	// Re-read after other accounts acquired grants: no cross-account leakage.
	if account, found, err := directory.ByUsername(ctx, "none"); err != nil || !found || len(account.Permissions()) != 0 {
		t.Fatal("permissions leaked from another account", found, err)
	}
	if account, found, err := directory.ByPrincipalID(ctx, "missing"); err != nil || found || account.Profile().ID != 0 {
		t.Fatal("missing identity published an account", found, err)
	}
	for _, mode := range []string{"revision", "credential", "username", "principal", "permission"} {
		t.Run("invalid_"+mode, func(t *testing.T) {
			user := createUser("invalid_"+mode, func(input models.UserCreate) models.UserCreate {
				switch mode {
				case "revision":
					return input.WithRevision(0)
				case "credential":
					return input.WithEncodedPassword("")
				case "username":
					return input.WithUsername(" malformed")
				case "principal":
					return input.WithPrincipalID(" malformed")
				}
				return input
			})
			if mode == "permission" {
				bad, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("Malformed", "Invalid"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(user.ID, bad.ID)); err != nil {
					t.Fatal(err)
				}
			}
			var account identity.Account
			var found bool
			var err error
			if mode == "username" {
				account, found, err = directory.ByPrincipalID(ctx, user.PrincipalID)
			} else {
				account, found, err = directory.ByUsername(ctx, user.Username)
			}
			if err == nil || found || account.Profile().ID != 0 || len(account.Permissions()) != 0 {
				t.Fatal("invalid stored identity published partial state", found, err)
			}
		})
	}
	t.Run("permission_limit", func(t *testing.T) {
		user := createUser("limit", nil)
		add := func(from, to int) error {
			return backend.Atomic(ctx, func(session db.Session) error {
				for index := from; index < to; index++ {
					permission, err := models.PermissionObjects.Create(ctx, session, models.NewPermissionCreate(fmt.Sprintf("limit.p%03d", index), "Limit"))
					if err != nil {
						return err
					}
					if _, err := models.UserPermissionsLinkObjects.Create(ctx, session, models.NewUserPermissionsLinkCreate(user.ID, permission.ID)); err != nil {
						return err
					}
				}
				return nil
			})
		}
		if err := add(0, auth.MaximumPermissions); err != nil {
			t.Fatal(err)
		}
		if account, found, err := directory.ByUsername(ctx, user.Username); err != nil || !found || len(account.Permissions()) != auth.MaximumPermissions {
			t.Fatal("maximum permitted grants did not resolve", found, err)
		}
		if err := add(auth.MaximumPermissions, auth.MaximumPermissions+1); err != nil {
			t.Fatal(err)
		}
		if account, found, err := directory.ByUsername(ctx, user.Username); err == nil || found || account.Profile().ID != 0 {
			t.Fatal("excess permissions silently truncated or published", found, err)
		}
	})
	for _, mode := range []string{"cleanup_failure", "canceled_after_read"} {
		t.Run(mode, func(t *testing.T) {
			readContext, cancel := context.WithCancel(ctx)
			defer cancel()
			fault := errors.New("private snapshot end failure")
			failed, err := identity.NewDirectory(directorySnapshotFunc(func(ctx context.Context, callback func(db.Queryer) error) error {
				if err := backend.ReadSnapshot(ctx, callback); err != nil {
					return err
				}
				if mode == "canceled_after_read" {
					cancel()
					return nil
				}
				return fault
			}))
			if err != nil {
				t.Fatal(err)
			}
			account, found, err := failed.ByUsername(readContext, "overlap")
			if mode == "canceled_after_read" {
				fault = context.Canceled
			}
			if !errors.Is(err, fault) || found || account.Profile().ID != 0 || len(account.Permissions()) != 0 {
				t.Fatal("failed snapshot end published a successful read", found, err)
			}
		})
	}
}

type directorySnapshotFunc func(context.Context, func(db.Queryer) error) error

func (function directorySnapshotFunc) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return function(ctx, callback)
}
