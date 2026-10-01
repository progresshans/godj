package identitytest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

type DirectoryBackend interface {
	ProductBackend
	db.SnapshotReader
	db.Atomic
}

// RunDirectory observes real model queries on both sides of an independently
// committed update. An ordinary READ COMMITTED scope fails the second read.
func RunDirectory(t *testing.T, backend DirectoryBackend, writer db.Atomic) {
	t.Helper()
	ctx := t.Context()
	loaded, _, err := definition.Load(identity.MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	const secret = "private-encoded-password-never-serialize"
	when := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	user, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("directory-member", "member", secret, when).WithLastLogin(when))
	if err != nil {
		t.Fatal(err)
	}
	group, err := models.GroupObjects.Create(ctx, backend, models.NewGroupCreate("Readers"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.view", "View"))
	if err != nil {
		t.Fatal(err)
	}
	change, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.change", "Change"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(ctx, backend, models.NewUserGroupsLinkCreate(user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(user.ID, view.ID)); err != nil {
		t.Fatal(err)
	}
	for _, permission := range []int64{view.ID, change.ID} {
		if _, err := models.GroupPermissionsLinkObjects.Create(ctx, backend, models.NewGroupPermissionsLinkCreate(group.ID, permission)); err != nil {
			t.Fatal(err)
		}
	}
	updates := 0
	interleaved := &afterUserRead{backend: backend, after: func() error {
		updates++
		return writer.Atomic(ctx, func(session db.Session) error {
			if _, err := models.UserObjects.Update(ctx, session, user, models.UserPatch{}.WithActive(false).WithStaff(true).WithSuperuser(true).WithUsername("renamed").WithRevision(2)); err != nil {
				return err
			}
			_, err := models.PermissionObjects.Update(ctx, session, change, models.PermissionPatch{}.WithCode("helpdesk.ticket.export"))
			return err
		})
	}}
	directory, err := identity.NewDirectory(interleaved)
	if err != nil {
		t.Fatal(err)
	}
	account, found, err := directory.ByPrincipalID(ctx, user.PrincipalID)
	if err != nil || !found || updates != 1 {
		t.Fatal("read interleaved identity", found, updates, err)
	}
	profile := account.Profile()
	if !profile.Active || profile.Staff || profile.Superuser || profile.Username != "member" || profile.Revision != 1 {
		t.Fatal("user state was not the first snapshot")
	}
	want := []auth.Permission{"helpdesk.ticket.change", "helpdesk.ticket.view"}
	if !reflect.DeepEqual(account.Permissions(), want) {
		t.Fatal("mixed user and permission snapshots", account.Permissions())
	}
	assertReferencePermissions(t, "grant_union", "effective", account.Permissions())
	permissions := account.Permissions()
	permissions[0] = "unrelated.permission"
	*profile.LastLogin = when.Add(time.Hour)
	if !reflect.DeepEqual(account.Permissions(), want) || !account.Profile().LastLogin.Equal(when) {
		t.Fatal("account accessors shared mutable state")
	}
	encoded, err := json.Marshal(account)
	if err != nil || !strings.Contains(string(encoded), `"username":"member"`) || strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "encoded_password") {
		t.Fatal("account encoding exposed storage credential or omitted profile", err)
	}
	for _, display := range []string{fmt.Sprint(account), fmt.Sprintf("%+v", account), fmt.Sprintf("%#v", account)} {
		if display != "identity.Account{redacted}" {
			t.Fatal("account diagnostic was not redacted")
		}
	}
	fresh, err := identity.NewDirectory(backend)
	if err != nil {
		t.Fatal(err)
	}
	current, found, err := fresh.ByUsername(ctx, "renamed")
	if err != nil || !found || current.Profile().Active || !current.Profile().Staff || !current.Profile().Superuser || current.Profile().Revision != 2 {
		t.Fatal("fresh lookup did not observe committed identity", found, err)
	}
	if !reflect.DeepEqual(current.Permissions(), []auth.Permission{"helpdesk.ticket.export", "helpdesk.ticket.view"}) || !reflect.DeepEqual(account.Permissions(), want) {
		t.Fatal("current permissions were cached or prior account was changed")
	}
	assertReferencePermissions(t, "instance_permission_cache", "fresh", current.Permissions())
	assertReferencePermissions(t, "instance_permission_cache", "held", account.Permissions())
	if _, found, err := fresh.ByUsername(ctx, "member"); err != nil || found {
		t.Fatal("renamed username still resolved", err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleters.IdentityGroup.Delete(ctx, backend, &group); err != nil {
		t.Fatal(err)
	}
	afterDeletion, found, err := fresh.ByUsername(ctx, "renamed")
	if err != nil || !found {
		t.Fatal("group deletion removed user", found, err)
	}
	assertReferencePermissions(t, "group_deletion", "effective", afterDeletion.Permissions())
}

type afterUserRead struct {
	backend db.SnapshotReader
	after   func() error
}

func (value *afterUserRead) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	return value.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		return callback(&afterReadQueryer{Queryer: reader, after: value.after})
	})
}

type afterReadQueryer struct {
	db.Queryer
	after func() error
	used  bool
}

func (value *afterReadQueryer) ValidateSession(ctx context.Context) error {
	return value.Queryer.(db.SessionValidator).ValidateSession(ctx)
}

func (value *afterReadQueryer) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := value.Queryer.Query(ctx, plan)
	if err != nil || value.used {
		return rows, err
	}
	value.used = true
	return &afterReadRows{Rows: rows, after: value.after}, nil
}

type afterReadRows struct {
	db.Rows
	after func() error
	done  bool
}

func (rows *afterReadRows) Close() error {
	if err := rows.Rows.Close(); err != nil {
		return err
	}
	if rows.done {
		return nil
	}
	rows.done = true
	return rows.after()
}
