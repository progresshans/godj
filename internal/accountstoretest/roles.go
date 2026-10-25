package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/validation"
)

func roleConfig() accountmodel.AccountEditConfig[models.User] {
	return accountmodel.AccountEditConfig[models.User]{Fields: []string{"active", "staff", "superuser", "groups", "permissions"}, ChangePermission: "account.direct", Now: func() time.Time { return joined }}
}
func roleInput(t *testing.T, values map[string]query.Value, collections map[string][]int64) orm.ModelInput[models.User] {
	t.Helper()
	input := profileInput(t, values)
	input, err := input.WithCollections(collections)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func roleTarget(t *testing.T, backend Backend, f *fixture, name string) models.User {
	t.Helper()
	target, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("principal-"+name, name, f.user.EncodedPassword, joined))
	if err != nil {
		t.Fatal(err)
	}
	return target
}
func roleEditor(t *testing.T, store *accountmodel.SessionStore[models.User]) *accountmodel.AccountEditor[models.User] {
	t.Helper()
	editor, err := project.NewAccountEditor(store, auth.PrincipalAuthorizer{}, roleConfig())
	if err != nil {
		t.Fatal(err)
	}
	return editor
}
func roleReader(t *testing.T, backend db.SnapshotReader) *accountmodel.ProfileReader[models.User] {
	t.Helper()
	reader, err := project.NewAccountProfileReader(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: []string{"active", "staff", "superuser", "groups", "permissions"}, ViewPermission: "account.direct"})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}
func assertRoleKeys(t *testing.T, reader *accountmodel.ProfileReader[models.User], actor auth.Principal, id int64, groups, permissions []int64) {
	t.Helper()
	row, err := reader.Get(t.Context(), actor, id)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]int64{"groups": groups, "permissions": permissions} {
		keys, loaded := row.View().Collection(name)
		if !loaded || !slices.Equal(keys, want) {
			t.Fatal("role membership differs", name, keys, want)
		}
	}
}

func roleEditAndDeactivation(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	target := roleTarget(t, backend, f, "role-target")
	before, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present {
		t.Fatal(err)
	}
	now := joined
	store, manager, _ := accountSessions(t, backend, &now)
	editor := roleEditor(t, store)
	reader := roleReader(t, backend)
	targetSessions := make([]sessions.Record, 2)
	for i := range targetSessions {
		targetSessions[i], err = manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: target.PrincipalID})
		if err != nil {
			t.Fatal(err)
		}
	}
	actorSession, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.user.PrincipalID})
	if err != nil {
		t.Fatal(err)
	}
	anonymous, err := manager.Create(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := sessiondb.New(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := independent.Store("historical-other-account-domain")
	if err != nil {
		t.Fatal(err)
	}
	if created, err := foreign.Create(t.Context(), targetSessions[0]); err != nil || !created {
		t.Fatal(err)
	}
	calls := 0
	config := roleConfig()
	config.Validate = func(_ context.Context, _ db.Queryer, current, candidate models.User, selection accountmodel.RoleMemberships) (validation.Errors, error) {
		calls++
		if current.ID != target.ID || candidate.ID != target.ID {
			t.Fatal("validator lost typed target")
		}
		if keys := selection.Groups(); len(keys) > 0 {
			keys[0] = mathMaxKey
		}
		if keys := selection.Permissions(); len(keys) > 0 {
			keys[0] = mathMaxKey
		}
		current.PrincipalID = "mutated-copy"
		candidate.EncodedPassword = "mutated-copy"
		return validation.Errors{}, nil
	}
	editor, err = project.NewAccountEditor(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	input := roleInput(t, map[string]query.Value{"staff": query.Boolean(true)}, map[string][]int64{"groups": {f.group.ID, f.group.ID}, "permissions": {f.grants[0].ID}})
	changed, err := editor.Update(t.Context(), actor, target.ID, 1, input)
	if err != nil || changed.Revision != 2 || changed.RevokedSessions != 0 || !slices.Equal(changed.ChangedFields, []string{"staff", "groups", "permissions"}) {
		t.Fatal("role update", changed, err)
	}
	assertRoleKeys(t, reader, actor, target.ID, []int64{f.group.ID}, []int64{f.grants[0].ID})
	fresh, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present || fresh.SessionStamp() != before.SessionStamp() || !fresh.Principal().Staff() || len(fresh.Principal().Permissions()) != 3 {
		t.Fatal("role edit changed stamp or lost grants", err)
	}
	for _, record := range targetSessions {
		accountSessionRecord(t, store, record, true)
	}
	noop, err := editor.Update(t.Context(), actor, target.ID, 2, input)
	if err != nil || noop.Revision != 2 || len(noop.ChangedFields) != 0 || calls != 2 {
		t.Fatal("no-op skipped validation or changed revision", err)
	}
	if result, err := editor.Update(t.Context(), actor, target.ID, 1, input); result.Revision != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
		t.Fatal("stale role edit", err)
	}
	changed, err = editor.Update(t.Context(), actor, target.ID, 2, roleInput(t, nil, map[string][]int64{"permissions": nil}))
	if err != nil || changed.Revision != 3 || !slices.Equal(changed.ChangedFields, []string{"permissions"}) {
		t.Fatal("explicit direct clear", err)
	}
	assertRoleKeys(t, reader, actor, target.ID, []int64{f.group.ID}, nil)
	// A selected detail owns memberships; a list loads no per-target grants.
	counter := &roleReadCounter{Backend: backend}
	pageReader := roleReader(t, counter)
	page, err := pageReader.List(t.Context(), actor, 0, 100)
	if err != nil || len(page.Items) != 2 || counter.groups != 1 || counter.permissions != 1 {
		t.Fatal("role list ran target membership queries", counter.groups, counter.permissions, err)
	}
	for _, row := range page.Items {
		if len(row.View().Collections()) != 0 || len(row.View().Values()) != 3 {
			t.Fatal("list exposed detail-only membership or hidden fields")
		}
	}
	changed, err = editor.Update(t.Context(), actor, target.ID, 3, roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, map[string][]int64{"groups": nil}))
	if err != nil || changed.Revision != 4 || changed.RevokedSessions != 2 {
		t.Fatal("deactivation did not revoke target sessions", changed, err)
	}
	for _, record := range targetSessions {
		accountSessionRecord(t, store, record, false)
	}
	accountSessionRecord(t, store, actorSession, true)
	accountSessionRecord(t, store, anonymous, true)
	accountSessionRecord(t, foreign, targetSessions[0], true)
	row := profileRow(t, reopen(t), target.ID)
	if row.Active || !row.Staff || row.Superuser || row.Revision != 4 || row.PrincipalID != target.PrincipalID || row.EncodedPassword != target.EncodedPassword || row.LastLogin != nil || !row.DateJoined.Equal(target.DateJoined) {
		t.Fatal("role edit changed protected state")
	}
	history := profileHistory(t, reopen(t), actor, target.ID)
	if len(history) != 3 || history[0].ActorID() != actor.ID() {
		t.Fatal("role audit missing")
	}
	if changed, err = editor.Update(t.Context(), actor, target.ID, 4, roleInput(t, map[string]query.Value{"active": query.Boolean(true)}, nil)); err != nil || changed.Revision != 5 || changed.RevokedSessions != 0 {
		t.Fatal("reactivation", err)
	}
	for _, record := range targetSessions {
		accountSessionRecord(t, store, record, false)
	}
	restored, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present || restored.SessionStamp() != before.SessionStamp() {
		t.Fatal("reactivation changed identity/stamp", err)
	}
}

type roleReadCounter struct {
	Backend
	groups, permissions int
}

func (owner *roleReadCounter) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	return owner.Backend.ReadSnapshot(ctx, func(scope db.Queryer) error { return read(&roleCountQuery{Queryer: scope, owner: owner}) })
}

type roleCountQuery struct {
	db.Queryer
	owner *roleReadCounter
}

func (scope *roleCountQuery) ValidateSession(ctx context.Context) error {
	return scope.Queryer.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *roleCountQuery) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	switch plan.Table() {
	case "godj_identity_group":
		scope.owner.groups++
	case "godj_identity_permission":
		scope.owner.permissions++
	}
	return scope.Queryer.Query(ctx, plan)
}

func roleAuthorityAndDomain(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	actor := credential.Principal()
	target := roleTarget(t, backend, f, "role-authority")
	now := joined
	store, _, _ := accountSessions(t, backend, &now)
	editor := roleEditor(t, store)
	input := roleInput(t, map[string]query.Value{"superuser": query.Boolean(true)}, nil)
	if _, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &f.direct); err != nil {
		t.Fatal(err)
	}
	if result, err := editor.Update(t.Context(), actor, target.ID, 1, input); result.Revision != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("stale grant edited role", err)
	}
	restored, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(f.user.ID, f.grants[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	f.direct = restored
	config := roleConfig()
	config.AdditionalPermissions = []auth.Permission{"account.missing"}
	restricted, err := project.NewAccountEditor(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restricted.Update(t.Context(), actor, target.ID, 1, input); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("missing additional permission admitted", err)
	}
	denied, err := project.NewAccountEditor(store, overlayFunc(func(context.Context, auth.Principal, auth.Permission) (bool, error) { return false, nil }), roleConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Update(t.Context(), actor, target.ID, 1, input); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodePermission}) {
		t.Fatal("deny overlay bypassed", err)
	}
	transitionSessionDomain(t, backend, f.binding, f.ownership)
	if result, err := editor.Update(t.Context(), actor, target.ID, 1, input); result.Revision != 0 || err == nil {
		t.Fatal("old domain edited current role")
	}
	current := profileActor(t, f)
	if result, err := editor.Update(t.Context(), current.Principal(), target.ID, 1, input); result.Revision != 2 || err != nil {
		t.Fatal("new domain role write", err)
	}
	if old := profileHistory(t, backend, actor, target.ID); len(old) != 0 {
		t.Fatal("role history crossed domain")
	}
	if fresh := profileHistory(t, reopen(t), current.Principal(), target.ID); len(fresh) != 1 {
		t.Fatal("current role audit missing")
	}
}

func roleBounds(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	target := roleTarget(t, backend, f, "role-bounds")
	now := joined
	store, _, _ := accountSessions(t, backend, &now)
	editor := roleEditor(t, store)
	reader := roleReader(t, backend)
	var groups, permissions []int64
	var wide models.Group
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for i := range 256 {
			group, err := models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate(fmt.Sprintf("role-%03d", i)))
			if err != nil {
				return err
			}
			groups = append(groups, group.ID)
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), scope, models.NewGroupPermissionsLinkCreate(group.ID, f.grants[0].ID)); err != nil {
				return err
			}
			permission, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(fmt.Sprintf("role.p%03d", i), "Role permission"))
			if err != nil {
				return err
			}
			permissions = append(permissions, permission.ID)
		}
		var err error
		wide, err = models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate("role-wide"))
		if err != nil {
			return err
		}
		for _, key := range permissions {
			if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), scope, models.NewGroupPermissionsLinkCreate(wide.ID, key)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := editor.Update(t.Context(), actor, target.ID, 1, roleInput(t, nil, map[string][]int64{"groups": groups})); err != nil || result.Revision != 2 {
		t.Fatal("256 overlapping groups rejected", err)
	}
	assertRoleKeys(t, reader, actor, target.ID, groups, nil)
	if credential, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID); err != nil || !present || len(credential.Principal().Permissions()) != 1 {
		t.Fatal("overlap not deduplicated", err)
	}
	if result, err := editor.Update(t.Context(), actor, target.ID, 2, roleInput(t, nil, map[string][]int64{"groups": {wide.ID}})); err != nil || result.Revision != 3 {
		t.Fatal("256 effective grants rejected", err)
	}
	for _, input := range []orm.ModelInput[models.User]{
		roleInput(t, nil, map[string][]int64{"permissions": {f.grants[0].ID}}),
		roleInput(t, nil, map[string][]int64{"groups": {mathMaxKey}}),
		roleInput(t, nil, map[string][]int64{"permissions": {mathMaxKey}}),
		roleInput(t, nil, map[string][]int64{"groups": make([]int64, 257)}),
		roleInput(t, nil, map[string][]int64{"permissions": make([]int64, 257)}),
	} {
		result, err := editor.Update(t.Context(), actor, target.ID, 3, input)
		if _, rejected := validation.Rejected(err); !rejected || result.Revision != 0 {
			t.Fatal("role choice/bound accepted", err)
		}
	}
	assertRoleKeys(t, reader, actor, target.ID, []int64{wide.ID}, nil)
	if result, err := editor.Update(t.Context(), actor, target.ID, 3, roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, map[string][]int64{"permissions": {f.grants[0].ID}})); err == nil || result.Revision != 0 {
		t.Fatal("inactive candidate bypassed permission bound")
	}
	if result, err := editor.Update(t.Context(), actor, target.ID, 3, roleInput(t, nil, map[string][]int64{"groups": nil, "permissions": permissions})); err != nil || result.Revision != 4 {
		t.Fatal("256 direct grants rejected", err)
	}
	assertRoleKeys(t, reader, actor, target.ID, nil, permissions)
}

func roleTargetIntegrity(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	store, manager, _ := accountSessions(t, backend, &now)
	editor := roleEditor(t, store)
	for _, mode := range []string{"revision", "principal", "group_revision", "permission_code", "permission_revision", "group_overflow"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "role-invalid-"+mode)
			if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
				switch mode {
				case "revision":
					target.Revision = 0
					return models.UserObjects.Save(t.Context(), scope, &target, orm.UpdateFieldNames[models.User]("revision"))
				case "principal":
					target.PrincipalID = ""
					return models.UserObjects.Save(t.Context(), scope, &target, orm.UpdateFieldNames[models.User]("principal_id"))
				case "group_revision", "group_overflow":
					count := 1
					if mode == "group_overflow" {
						count = 257
					}
					for i := range count {
						g, err := models.GroupObjects.Create(t.Context(), scope, models.NewGroupCreate(fmt.Sprintf("invalid-%s-%03d", mode, i)))
						if err != nil {
							return err
						}
						if mode == "group_revision" {
							g.Revision = 0
							if err := models.GroupObjects.Save(t.Context(), scope, &g, orm.UpdateFieldNames[models.Group]("revision")); err != nil {
								return err
							}
						}
						if _, err := models.UserGroupsLinkObjects.Create(t.Context(), scope, models.NewUserGroupsLinkCreate(target.ID, g.ID)); err != nil {
							return err
						}
					}
				default:
					code := "role." + mode
					if mode == "permission_code" {
						code = "invalid permission"
					}
					p, err := models.PermissionObjects.Create(t.Context(), scope, models.NewPermissionCreate(code, "invalid stored role"))
					if err != nil {
						return err
					}
					if mode == "permission_revision" {
						p.Revision = 0
						if err := models.PermissionObjects.Save(t.Context(), scope, &p, orm.UpdateFieldNames[models.Permission]("revision")); err != nil {
							return err
						}
					}
					if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), scope, models.NewUserPermissionsLinkCreate(target.ID, p.ID)); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			record, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: "principal-role-invalid-" + mode})
			if err != nil {
				t.Fatal(err)
			}
			before := profileRow(t, backend, target.ID)
			result, err := editor.Update(t.Context(), actor, target.ID, 1, roleInput(t, map[string]query.Value{"active": query.Boolean(false)}, map[string][]int64{"groups": nil, "permissions": nil}))
			if result.Revision != 0 || err == nil {
				t.Fatal("corrupt current role repaired by ordinary write")
			}
			requireProfileCode(t, err, accountmodel.CodePersistence)
			if after := profileRow(t, reopen(t), target.ID); !reflect.DeepEqual(before, after) || len(profileHistory(t, backend, actor, target.ID)) != 0 {
				t.Fatal("corrupt role target changed")
			}
			accountSessionRecord(t, store, record, true)
		})
	}
}

func roleNonpositiveKeys(t *testing.T, backend Backend, fixtureKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	now := joined
	store, _, _ := accountSessions(t, backend, &now)
	editor := roleEditor(t, store)
	reader := roleReader(t, backend)
	for _, key := range []int64{0, -7} {
		target := roleTarget(t, backend, f, fmt.Sprintf("role-key-%d", key))
		fixtureKey(t, backend, "user", target.ID, key)
		group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate(fmt.Sprintf("role-key-%d", key)))
		if err != nil {
			t.Fatal(err)
		}
		fixtureKey(t, backend, "group", group.ID, key)
		permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(fmt.Sprintf("role.key%d", key), "Signed role key"))
		if err != nil {
			t.Fatal(err)
		}
		fixtureKey(t, backend, "permission", permission.ID, key)
		input := roleInput(t, map[string]query.Value{"staff": query.Boolean(true)}, map[string][]int64{"groups": {key}, "permissions": {key}})
		input, err = input.WithTargetKey(query.Integer(key))
		if err != nil {
			t.Fatal(err)
		}
		result, err := editor.Update(t.Context(), actor, key, 1, input)
		if err != nil || result.ID != key || result.Revision != 2 {
			t.Fatal("stored signed role key rejected", err)
		}
		assertRoleKeys(t, reader, actor, key, []int64{key}, []int64{key})
		if result, err := editor.Update(t.Context(), actor, key, 2, roleInput(t, nil, map[string][]int64{"groups": nil, "permissions": nil})); err != nil || result.Revision != 3 {
			t.Fatal("signed role membership clear", err)
		}
	}
}
