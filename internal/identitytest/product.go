package identitytest

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/identityfixture/modeldef"
	workmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	accounts "github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

type ProductBackend interface {
	db.Session
	db.RelationAtomic
	mb.RevisionFencedBackend
	Close() error
}

func RunProduct(t *testing.T, backend ProductBackend) {
	t.Helper()
	ctx := t.Context()
	local, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]migrations.Operation, 0, len(local.Models))
	for _, model := range local.Models {
		operations = append(operations, migrations.CreateModel{AppLabel: local.AppLabel, Model: model})
	}
	fixture, err := definition.Encode(definition.Producer{Name: "identity-fixture", Version: "1"}, migrations.Migration{
		App: local.AppLabel, Name: "0001_initial", Dependencies: []migrations.MigrationKey{identity.InitialMigrationKey()}, Operations: operations,
	})
	if err != nil {
		t.Fatal(err)
	}
	sources := append(identity.MigrationSources(), definition.Source{SourceID: "identityfixture/0001_initial", Document: fixture})
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	user, err := accounts.UserObjects.Create(ctx, backend, accounts.NewUserCreate("durable-member", "member", "fixture-encoded-password", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	if !user.Active || user.Staff || user.Superuser || user.Revision != 1 || user.LastLogin != nil || user.FirstName != "" || user.LastName != "" || user.Email != "" {
		t.Fatal("identity storage lost safe initial defaults")
	}
	group, err := accounts.GroupObjects.Create(ctx, backend, accounts.NewGroupCreate("Editors"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := accounts.PermissionObjects.Create(ctx, backend, accounts.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	if err != nil {
		t.Fatal(err)
	}
	change, err := accounts.PermissionObjects.Create(ctx, backend, accounts.NewPermissionCreate("helpdesk.ticket.change", "Change ticket"))
	if err != nil {
		t.Fatal(err)
	}
	note, err := workmodels.NoteObjects.Create(ctx, backend, workmodels.NewNoteCreate("owned", user.ID))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := workmodels.GuardObjects.Create(ctx, backend, workmodels.NewGuardCreate(user.ID))
	if err != nil {
		t.Fatal(err)
	}
	bound, err := project.Using(backend)
	if err != nil {
		t.Fatal(err)
	}
	member, found, err := bound.AccountsUser.Filter(accounts.UserFields.ID.Exact(user.ID)).OrderBy(accounts.UserFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("bind imported User", err)
	}
	editors, found, err := bound.AccountsGroup.Filter(accounts.GroupFields.ID.Exact(group.ID)).OrderBy(accounts.GroupFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("bind imported Group", err)
	}
	membership, err := member.Groups()
	if err != nil {
		t.Fatal(err)
	}
	if err := membership.AddKeys(ctx, []int64{group.ID, group.ID}); err != nil {
		t.Fatal(err)
	}
	direct, err := member.Permissions()
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.AddKeys(ctx, []int64{view.ID}); err != nil {
		t.Fatal(err)
	}
	grants, err := editors.Permissions()
	if err != nil {
		t.Fatal(err)
	}
	if err := grants.AddKeys(ctx, []int64{view.ID, change.ID}); err != nil {
		t.Fatal(err)
	}
	owned, found, err := bound.WorkNote.Filter(workmodels.NoteFields.ID.Exact(note.ID)).OrderBy(workmodels.NoteFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	owner, err := owned.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The external relation exposes the library's actual Go model type.
	var libraryValue accounts.User
	libraryValue, err = owner.Unwrap()
	if err != nil || libraryValue.ID != user.ID || libraryValue.PrincipalID != "durable-member" {
		t.Fatal("foreign relation did not reuse library model", err)
	}
	prefetched, err := bound.AccountsUser.PrefetchRelatedPaths("groups__permissions", "permissions")
	if err != nil {
		t.Fatal(err)
	}
	members, err := prefetched.All(ctx)
	if err != nil || len(members) != 1 {
		t.Fatal("prefetch imported graph", err)
	}
	groups, err := members[0].Groups()
	if err != nil {
		t.Fatal(err)
	}
	groupRows, err := groups.All(ctx)
	if err != nil || len(groupRows) != 1 {
		t.Fatal("group membership is not a set", err)
	}
	groupPermissions, err := groupRows[0].Permissions()
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := groupPermissions.All(ctx)
	if err != nil || len(permissions) != 2 {
		t.Fatal("nested imported permission graph", err)
	}
	got := []int64{permissions[0].ID, permissions[1].ID}
	slices.Sort(got)
	if !reflect.DeepEqual(got, []int64{view.ID, change.ID}) {
		t.Fatal("permission membership changed", got)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleters.AccountsUser.Delete(ctx, backend, &user); !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeProtectedForeignKey}) {
		t.Fatal("library delete ignored host PROTECT", err)
	}
	if count, err := workmodels.NoteObjects.Using(backend).Count(ctx); err != nil || count != 1 {
		t.Fatal("PROTECT failure changed host rows", err)
	}
	if _, err := workmodels.GuardObjects.Delete(ctx, backend, &guard); err != nil {
		t.Fatal(err)
	}
	if count, err := deleters.AccountsUser.Delete(ctx, backend, &user); err != nil || count < 1 {
		t.Fatal("host-bound imported deletion", err)
	}
	for name, counted := range map[string]func() (int64, error){
		"notes":         func() (int64, error) { return workmodels.NoteObjects.Using(backend).Count(ctx) },
		"users":         func() (int64, error) { return accounts.UserObjects.Using(backend).Count(ctx) },
		"memberships":   func() (int64, error) { return accounts.UserGroupsLinkObjects.Using(backend).Count(ctx) },
		"direct grants": func() (int64, error) { return accounts.UserPermissionsLinkObjects.Using(backend).Count(ctx) },
	} {
		if count, err := counted(); err != nil || count != 0 {
			t.Fatalf("%s survived deletion: %d, %v", name, count, err)
		}
	}
	if count, err := accounts.GroupObjects.Using(backend).Count(ctx); err != nil || count != 1 {
		t.Fatal("user deletion removed reusable group", err)
	}
	if count, err := accounts.PermissionObjects.Using(backend).Count(ctx); err != nil || count != 2 {
		t.Fatal("user deletion removed permission catalog", err)
	}
}
