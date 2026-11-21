package accountstoretest

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

type identityObservationReference map[string]json.RawMessage

func loadCatalogReference(t *testing.T, vendor string) identityObservationReference {
	t.Helper()
	raw, err := identityreference.Catalog(vendor)
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, Python, Backend string
		Sources                 map[string]string `json:"source_sha256"`
		Observations            identityObservationReference
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 3 || len(reference.Observations) != 7 {
		t.Fatal("catalog reference is incomplete or belongs to another runtime")
	}
	for _, name := range []string{"admin_options", "auth_admin", "auth_models"} {
		value := reference.Sources[name]
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			t.Fatal("independent catalog source digest is missing", name)
		}
	}
	for _, name := range []string{"group_created", "group_edited", "permission_renamed", "rollback", "permission_deleted", "group_deleted", "admission"} {
		if _, present := reference.Observations[name]; !present {
			t.Fatal("independent catalog observation is missing", name)
		}
	}
	return reference.Observations
}

func (reference identityObservationReference) assert(t *testing.T, name string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(reference[name], &expected); err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("selected identity differs from the independent native observation", name, err)
	}
}

type catalogHostState struct {
	account accountNativeState
	notes   []hostmodels.AccessNote
	guards  []hostmodels.AccessGuard
}

func catalogHostSnapshot(t *testing.T, backend Backend) catalogHostState {
	t.Helper()
	var state catalogHostState
	visits := 0
	err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		visits++
		if visits != 1 {
			return errors.New("catalog host observation repeated its callback")
		}
		state.account = accountNativeRows(t, scope)
		state.notes = deletionRows(t, scope, hostmodels.AccessNoteObjects)
		state.guards = deletionRows(t, scope, hostmodels.AccessGuardObjects)
		return nil
	})
	if err != nil || visits != 1 {
		t.Fatal("native catalog host observation did not complete", err)
	}
	return state
}

func assertCatalogHostState(t *testing.T, backend Backend, expected catalogHostState, actor auth.Principal, events ...catalogExpectedEvent) {
	t.Helper()
	actual := catalogHostSnapshot(t, backend)
	assertCatalogRows(t, expected.account, actual.account, actor, events...)
	if !reflect.DeepEqual(expected.notes, actual.notes) || !reflect.DeepEqual(expected.guards, actual.guards) {
		t.Fatal("catalog mutation changed unexpected host rows")
	}
}

func catalogReferenceText[M any](t *testing.T, value orm.ModelView[M], name string) string {
	t.Helper()
	field, loaded := value.Get(name)
	text, valid := field.String()
	if !loaded || !valid {
		t.Fatal("catalog observation lost its selected text", name)
	}
	return text
}

func catalogReferenceCodes(t *testing.T, values []auth.Permission) []string {
	t.Helper()
	result := make([]string, len(values))
	for index, value := range values {
		var found bool
		result[index], found = strings.CutPrefix(string(value), "helpdesk.ticket.")
		if !found {
			t.Fatal("catalog reference member inherited unrelated authority")
		}
	}
	slices.Sort(result)
	return result
}

func catalogReferenceKeys(t *testing.T, backend Backend, ids []int64) []string {
	t.Helper()
	rows, err := models.PermissionObjects.Using(backend).Filter(models.PermissionFields.ID.In(ids...)).OrderBy(models.PermissionFields.Code.Asc()).All(t.Context())
	if err != nil || len(rows) != len(ids) {
		t.Fatal("catalog reference selection lost a permission", err)
	}
	values := make([]auth.Permission, len(rows))
	for index, row := range rows {
		values[index] = auth.Permission(row.Code)
	}
	return catalogReferenceCodes(t, values)
}

// Run the unchanged public Django catalog actions through selected owners.
// Native host rows, revisions, session bytes and semantic audits have an
// independent full-state oracle in addition to the shared observations.
func selectedCatalogReference(t *testing.T, vendor string, backend Backend, reopen func(*testing.T) Backend) {
	reference := loadCatalogReference(t, vendor)
	f := provisionCatalogHost(t, backend)
	actor, groups, permissions := f.credential.Principal(), f.catalog.Groups(), f.catalog.Permissions()
	check := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	store, err := hostproject.NewAccountSessionStore(backend, sessiondb.Config{})
	check(t, err)
	editor, err := hostproject.NewAccountEditor(store, auth.PrincipalAuthorizer{}, roleConfig())
	check(t, err)
	reader, err := hostproject.NewAccountProfileReader(backend, auth.PrincipalAuthorizer{}, accountmodel.ProfileReadConfig{Fields: []string{"username", "groups", "permissions"}, ViewPermission: "account.direct"})
	check(t, err)
	view, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	check(t, err)
	member, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("catalog-member", "member", f.account.user.EncodedPassword, joined).WithStaff(true))
	check(t, err)
	credential := func(t *testing.T) auth.Credential {
		t.Helper()
		value, present, err := f.account.store.CredentialByID(t.Context(), member.PrincipalID)
		if err != nil || !present {
			t.Fatal("catalog reference lost its actual member", err)
		}
		return value
	}
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return joined }})
	check(t, err)
	for _, marker := range []string{"first", "second", "third"} {
		passwordSession(t, manager, credential(t), marker)
	}
	passwordSession(t, manager, f.credential, "actor")
	_, err = manager.Create(t.Context(), map[string]string{"anonymous": "preserved"})
	check(t, err)
	var permission, group accountmodel.CatalogChange
	var held auth.Credential
	var beforeUser models.User
	if !t.Run("group_created", func(t *testing.T) {
		permission, err = permissions.Create(t.Context(), actor, catalogPermissionInput(t, map[string]query.Value{"code": query.String("helpdesk.ticket.change"), "name": query.String("Change ticket")}))
		check(t, err)
		if permission.Revision != 1 || !slices.Equal(permission.ChangedFields, []string{"code", "name"}) {
			t.Fatal("selected permission creation lost its mutation metadata")
		}
		keys := []int64{permission.ID, view.ID, permission.ID}
		input := catalogGroupInput(t, map[string]query.Value{"name": query.String("Editors")}, map[string][]int64{"permissions": keys})
		keys[0] = -1
		group, err = groups.Create(t.Context(), actor, input)
		check(t, err)
		if group.Revision != 1 || !slices.Equal(group.ChangedFields, []string{"name", "permissions"}) {
			t.Fatal("selected group creation lost its mutation metadata")
		}
		changed, err := editor.Update(t.Context(), actor, member.ID, 1, roleInput(t, nil, map[string][]int64{"groups": {group.ID}, "permissions": {permission.ID}}))
		check(t, err)
		if changed.Revision != 2 || changed.RevokedSessions != 0 || !slices.Equal(changed.ChangedFields, []string{"groups", "permissions"}) {
			t.Fatal("selected account did not attach its catalog memberships")
		}
		beforeUser, held = profileRow(t, reopen(t), member.ID), credential(t)
		detail, err := groups.Get(t.Context(), actor, group.ID)
		check(t, err)
		selected, loaded := detail.View().Collection("permissions")
		if !loaded || len(selected) != 2 {
			t.Fatal("catalog creation lost its deduplicated collection")
		}
		members, err := models.UserGroupsLinkObjects.Using(reopen(t)).Filter(models.UserGroupsLinkFields.TargetID.Exact(group.ID)).Count(t.Context())
		check(t, err)
		reference.assert(t, "group_created", map[string]any{"name": catalogReferenceText(t, detail.View(), "name"), "permissions": catalogReferenceKeys(t, backend, selected), "members": members, "effective": catalogReferenceCodes(t, held.Principal().Permissions())})
		selected[0] = -1
		again, err := groups.Get(t.Context(), actor, group.ID)
		check(t, err)
		owned, loaded := again.View().Collection("permissions")
		if !loaded || !slices.Equal(owned, []int64{view.ID, permission.ID}) {
			t.Fatal("catalog output collection retained caller mutation")
		}
	}) {
		return
	}
	if !t.Run("group_edited", func(t *testing.T) {
		expected := catalogHostSnapshot(t, reopen(t))
		for index := range expected.account.groups {
			if expected.account.groups[index].ID == group.ID {
				expected.account.groups[index].Name, expected.account.groups[index].Revision = "Operators", 2
			}
		}
		expected.account.grants = slices.DeleteFunc(expected.account.grants, func(row models.GroupPermissionsLink) bool { return row.SourceID == group.ID && row.TargetID == view.ID })
		changed, err := groups.Update(t.Context(), actor, group.ID, 1, catalogGroupInput(t, map[string]query.Value{"name": query.String("Operators")}, map[string][]int64{"permissions": {permission.ID}}))
		check(t, err)
		if changed.Revision != 2 || !slices.Equal(changed.ChangedFields, []string{"name", "permissions"}) {
			t.Fatal("selected group edit lost its mutation metadata")
		}
		assertCatalogHostState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", group.ID, admin.ActionChange, []string{"name", "permissions"}})
		detail, err := groups.Get(t.Context(), actor, group.ID)
		check(t, err)
		keys, loaded := detail.View().Collection("permissions")
		if !loaded {
			t.Fatal("edited catalog collection is unloaded")
		}
		reference.assert(t, "group_edited", map[string]any{"name": catalogReferenceText(t, detail.View(), "name"), "permissions": catalogReferenceKeys(t, backend, keys), "effective": catalogReferenceCodes(t, credential(t).Principal().Permissions()), "held_effective": catalogReferenceCodes(t, held.Principal().Permissions()), "same_password": profileRow(t, backend, member.ID).EncodedPassword == beforeUser.EncodedPassword})
		before := catalogHostSnapshot(t, reopen(t))
		same, err := groups.Update(t.Context(), actor, group.ID, 2, catalogGroupInput(t, nil, map[string][]int64{"permissions": {permission.ID, permission.ID}}))
		if err != nil || same.Revision != 2 || len(same.ChangedFields) != 0 || !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog group no-op changed native state", err)
		}
	}) {
		return
	}
	if !t.Run("permission_renamed", func(t *testing.T) {
		expected := catalogHostSnapshot(t, reopen(t))
		for index := range expected.account.permissions {
			if expected.account.permissions[index].ID == permission.ID {
				expected.account.permissions[index].Code, expected.account.permissions[index].Name, expected.account.permissions[index].Revision = "helpdesk.ticket.manage", "Manage ticket", 2
			}
		}
		input := catalogPermissionInput(t, map[string]query.Value{"code": query.String("helpdesk.ticket.manage"), "name": query.String("Manage ticket")})
		changed, err := permissions.Update(t.Context(), actor, permission.ID, 1, input)
		check(t, err)
		if changed.Revision != 2 || !slices.Equal(changed.ChangedFields, []string{"code", "name"}) {
			t.Fatal("selected permission rename lost its mutation metadata")
		}
		assertCatalogHostState(t, reopen(t), expected, actor, catalogExpectedEvent{"permission", permission.ID, admin.ActionChange, []string{"code", "name"}})
		detail, err := permissions.Get(t.Context(), actor, permission.ID)
		check(t, err)
		groupDetail, err := groups.Get(t.Context(), actor, group.ID)
		check(t, err)
		keys, loaded := groupDetail.View().Collection("permissions")
		if !loaded {
			t.Fatal("catalog permission rename lost the group selection")
		}
		profile, err := reader.Get(t.Context(), actor, member.ID)
		check(t, err)
		directKeys, directLoaded := profile.View().Collection("permissions")
		memberships, groupsLoaded := profile.View().Collection("groups")
		if profile.ID() != member.ID || profile.Revision() != 2 || !directLoaded || !groupsLoaded || !slices.Equal(memberships, []int64{group.ID}) {
			t.Fatal("selected account read lost the catalog membership or revision")
		}
		reference.assert(t, "permission_renamed", map[string]any{"name": catalogReferenceText(t, detail.View(), "name"), "code": strings.TrimPrefix(catalogReferenceText(t, detail.View(), "code"), "helpdesk.ticket."), "direct": catalogReferenceKeys(t, backend, directKeys), "group": catalogReferenceKeys(t, backend, keys), "effective": catalogReferenceCodes(t, credential(t).Principal().Permissions())})
		before := catalogHostSnapshot(t, reopen(t))
		same, err := permissions.Update(t.Context(), actor, permission.ID, 2, input)
		if err != nil || same.Revision != 2 || len(same.ChangedFields) != 0 || !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog permission no-op changed native state", err)
		}
	}) {
		return
	}
	if !t.Run("rollback", func(t *testing.T) {
		before := catalogHostSnapshot(t, reopen(t))
		fault := errors.New("private catalog reference audit failure")
		owner := catalogFaultOwner(t, backend, "audit_failure", fault, func() {})
		failedStore, err := hostproject.NewAccountSessionStore(owner, sessiondb.Config{})
		check(t, err)
		failed, err := hostproject.NewAccountCatalog(failedStore, auth.PrincipalAuthorizer{}, catalogConfig())
		check(t, err)
		groupResult, groupErr := failed.Groups().Update(t.Context(), actor, group.ID, 2, catalogGroupInput(t, map[string]query.Value{"name": query.String("rollback")}, map[string][]int64{"permissions": nil}))
		permissionResult, permissionErr := failed.Permissions().Update(t.Context(), actor, permission.ID, 2, catalogPermissionInput(t, map[string]query.Value{"name": query.String("rollback")}))
		if !errors.Is(groupErr, fault) || !errors.Is(permissionErr, fault) || owner.calls != 2 || !reflect.DeepEqual(groupResult, accountmodel.CatalogChange{}) || !reflect.DeepEqual(permissionResult, accountmodel.CatalogChange{}) || !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog rollback did not reach both audit writes or retained partial native state")
		}
		groupDetail, err := groups.Get(t.Context(), actor, group.ID)
		check(t, err)
		permissionDetail, err := permissions.Get(t.Context(), actor, permission.ID)
		check(t, err)
		reference.assert(t, "rollback", map[string]any{"group_name": catalogReferenceText(t, groupDetail.View(), "name"), "permission_name": catalogReferenceText(t, permissionDetail.View(), "name"), "effective": catalogReferenceCodes(t, credential(t).Principal().Permissions())})
	}) {
		return
	}
	survivor, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("surviving host", view.ID).WithGroupID(group.ID))
	check(t, err)
	cascade, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("cascaded host", permission.ID).WithGroupID(group.ID))
	check(t, err)
	if !t.Run("permission_deleted", func(t *testing.T) {
		expected := catalogHostSnapshot(t, reopen(t))
		expected.account.permissions = slices.DeleteFunc(expected.account.permissions, func(row models.Permission) bool { return row.ID == permission.ID })
		expected.account.grants = slices.DeleteFunc(expected.account.grants, func(row models.GroupPermissionsLink) bool {
			return row.SourceID == group.ID && row.TargetID == permission.ID
		})
		expected.account.direct = slices.DeleteFunc(expected.account.direct, func(row models.UserPermissionsLink) bool {
			return row.SourceID == member.ID && row.TargetID == permission.ID
		})
		for index := range expected.account.users {
			if expected.account.users[index].ID == member.ID {
				expected.account.users[index].Revision++
			}
		}
		for index := range expected.account.groups {
			if expected.account.groups[index].ID == group.ID {
				expected.account.groups[index].Revision++
			}
		}
		expected.notes = slices.DeleteFunc(expected.notes, func(row hostmodels.AccessNote) bool { return row.ID == cascade.ID })
		deleted, err := permissions.Delete(t.Context(), actor, permission.ID, 2)
		if err != nil || deleted.ID != permission.ID || deleted.Revision != 2 || deleted.DeletedRows != 4 || deleted.RevokedSessions != 0 {
			t.Fatal("catalog permission deletion omitted its host graph", err)
		}
		assertCatalogHostState(t, reopen(t), expected, actor, catalogExpectedEvent{"permission", permission.ID, admin.ActionDelete, nil})
		direct, err := models.UserPermissionsLinkObjects.Using(reopen(t)).Filter(models.UserPermissionsLinkFields.SourceID.Exact(member.ID)).Count(t.Context())
		check(t, err)
		grants, err := models.GroupPermissionsLinkObjects.Using(reopen(t)).Filter(models.GroupPermissionsLinkFields.SourceID.Exact(group.ID)).Count(t.Context())
		check(t, err)
		actual := catalogHostSnapshot(t, reopen(t))
		reference.assert(t, "permission_deleted", map[string]any{"user_exists": slices.ContainsFunc(actual.account.users, func(row models.User) bool { return row.ID == member.ID }), "group_exists": slices.ContainsFunc(actual.account.groups, func(row models.Group) bool { return row.ID == group.ID }), "direct_permissions": direct, "group_permissions": grants, "effective": catalogReferenceCodes(t, credential(t).Principal().Permissions())})
		staleGroup, err := groups.Update(t.Context(), actor, group.ID, 2, catalogGroupInput(t, map[string]query.Value{"name": query.String("stale")}, nil))
		requireProfileCode(t, err, accountmodel.CodeConflict)
		staleUser, err := editor.Update(t.Context(), actor, member.ID, beforeUser.Revision, roleInput(t, map[string]query.Value{"staff": query.Boolean(false)}, nil))
		requireProfileCode(t, err, accountmodel.CodeConflict)
		if !reflect.DeepEqual(staleGroup, accountmodel.CatalogChange{}) || !reflect.DeepEqual(staleUser, accountmodel.AccountChange{}) || !reflect.DeepEqual(actual, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog owner revision did not reject a stale group or account write")
		}
	}) {
		return
	}
	if !t.Run("group_deleted", func(t *testing.T) {
		expected := catalogHostSnapshot(t, reopen(t))
		expected.account.groups = slices.DeleteFunc(expected.account.groups, func(row models.Group) bool { return row.ID == group.ID })
		expected.account.memberships = slices.DeleteFunc(expected.account.memberships, func(row models.UserGroupsLink) bool { return row.SourceID == member.ID && row.TargetID == group.ID })
		for index := range expected.account.users {
			if expected.account.users[index].ID == member.ID {
				expected.account.users[index].Revision++
			}
		}
		for index := range expected.notes {
			if expected.notes[index].ID == survivor.ID {
				expected.notes[index].GroupID = nil
			}
		}
		deleted, err := groups.Delete(t.Context(), actor, group.ID, 3)
		if err != nil || deleted.ID != group.ID || deleted.Revision != 3 || deleted.DeletedRows != 2 || deleted.RevokedSessions != 0 {
			t.Fatal("catalog group deletion omitted its host graph", err)
		}
		assertCatalogHostState(t, reopen(t), expected, actor, catalogExpectedEvent{"group", group.ID, admin.ActionDelete, nil})
		memberships, err := models.UserGroupsLinkObjects.Using(reopen(t)).Filter(models.UserGroupsLinkFields.SourceID.Exact(member.ID)).Count(t.Context())
		check(t, err)
		actual := catalogHostSnapshot(t, reopen(t))
		reference.assert(t, "group_deleted", map[string]any{"user_exists": slices.ContainsFunc(actual.account.users, func(row models.User) bool { return row.ID == member.ID }), "memberships": memberships, "permission_exists": slices.ContainsFunc(actual.account.permissions, func(row models.Permission) bool { return row.ID == view.ID }), "effective": catalogReferenceCodes(t, credential(t).Principal().Permissions())})
	}) {
		return
	}
	t.Run("reopen", func(t *testing.T) {
		before := catalogHostSnapshot(t, reopen(t))
		beforeUser.Revision += 2
		if !reflect.DeepEqual(beforeUser, profileRow(t, reopen(t), member.ID)) {
			t.Fatal("catalog lifecycle changed unrelated account or credential values")
		}
		verifier, err := hostproject.NewAccountAuthenticator(t.Context(), reopen(t), f.account.hasher)
		check(t, err)
		current, err := verifier.Authenticate(t.Context(), member.Username, password)
		if err != nil || !current.MatchesSessionStamp(held.SessionStamp()) || len(current.Principal().Permissions()) != 0 {
			t.Fatal("catalog reopen changed the credential or retained stale grants", err)
		}
		for _, want := range []struct {
			model  string
			id     int64
			fields []string
		}{{"group", group.ID, []string{"name", "permissions"}}, {"permission", permission.ID, []string{"code", "name"}}} {
			entries := catalogAudit(t, reopen(t), actor, want.model, want.id)
			if len(entries) != 3 || entries[0].Action() != admin.ActionAdd || entries[1].Action() != admin.ActionChange || entries[2].Action() != admin.ActionDelete || !slices.Equal(entries[1].ChangedFields(), want.fields) {
				t.Fatal("catalog lifecycle lost its exact audit actions")
			}
			for _, event := range entries {
				if event.ActorID() != actor.ID() || !event.At().Equal(joined) {
					t.Fatal("catalog audit lost its current actor or timestamp")
				}
			}
		}
		if !reflect.DeepEqual(before, catalogHostSnapshot(t, reopen(t))) {
			t.Fatal("catalog restart and history observations changed native state")
		}
	})
}
