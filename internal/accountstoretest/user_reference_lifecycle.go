package accountstoretest

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

func userReferenceLifecycle(t *testing.T, vendor string, backend Backend, reopen func(*testing.T) Backend) {
	reference := loadUserReference(t, vendor)
	f := provisionUserReference(t, backend)
	runtime, actor := f.runtime, f.actor.Principal()
	management := runtime.Accounts()
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Editors"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	if err != nil {
		t.Fatal(err)
	}
	change, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.change", "Change ticket"))
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range []models.Permission{view, change} {
		if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
			t.Fatal(err)
		}
	}
	for _, marker := range []string{"first", "second", "third"} {
		passwordSession(t, runtime.Sessions(), f.actor, marker)
	}
	if _, err := runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": "preserved"}); err != nil {
		t.Fatal(err)
	}
	var row models.User
	var original auth.Credential
	if !t.Run("create_and_independent_observation", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		keys := []int64{group.ID, group.ID}
		input := roleInput(t, map[string]query.Value{"username": query.String("Ｆｒｅｄ"), "email": query.String("Mixed@EXAMPLE.COM")}, map[string][]int64{"groups": keys, "permissions": {view.ID, view.ID}})
		keys[0] = -999
		prepared, err := management.Creator().Prepare(t.Context(), actor, input, userReferencePassword)
		if err != nil || f.hasher.hashes.Load() != 1 {
			t.Fatal("selected creation preparation did not hash once outside native scopes", err)
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
		created, err := management.Creator().Commit(t.Context(), actor, prepared)
		if err != nil || created.ID <= 0 || created.Revision != 1 || created.RevokedSessions != 0 || !slices.Equal(created.ChangedFields, userReferenceCreationFields) || f.hasher.hashes.Load() != 1 {
			t.Fatal("selected creation metadata differs", err)
		}
		row = userReferenceExpectedRow(created.ID, "Fred", "Mixed@example.com", f.encoded.Load().(string))
		after := userReferenceSnapshot(t, reopen(t))
		userReferenceReplace(&before.account, row)
		userReferenceRelations(t, &before.account, after.account, row.ID, []int64{group.ID}, []int64{view.ID})
		assertUserReferenceState(t, before, after, actor, catalogExpectedEvent{"user", row.ID, admin.ActionAdd, userReferenceCreationFields})
		original, err = runtime.Authenticator().Authenticate(t.Context(), "Fred", userReferencePassword)
		if err != nil || original.Principal().ID() != row.PrincipalID {
			t.Fatal("selected created account cannot authenticate", err)
		}
		profile, err := management.Reader().Get(t.Context(), actor, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		groups, groupsLoaded := profile.View().Collection("groups")
		direct, directLoaded := profile.View().Collection("permissions")
		if !groupsLoaded || !directLoaded || profile.ID() != row.ID || profile.Revision() != 1 {
			t.Fatal("selected creation read lost envelope")
		}
		actual := profileRow(t, backend, row.ID)
		reference.assert(t, "created", map[string]any{"username": actual.Username, "email": actual.Email, "active": actual.Active, "staff": actual.Staff, "superuser": actual.Superuser, "password_usable": profile.PasswordUsable() && original.Principal().Authenticated(), "groups": len(groups), "direct_permissions": len(direct), "effective": userReferenceGrants(t, runtime, row.PrincipalID)})
	}) {
		return
	}

	beforeSession := userReferenceSnapshot(t, backend)
	targetSession := passwordSession(t, runtime.Sessions(), original, "target")
	targetSessionRow := userReferenceSession(t, runtime, original, targetSession.ID(), beforeSession, userReferenceSnapshot(t, reopen(t)), actor)
	child, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("Keep until deletion", row.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !t.Run("edit_and_detached_noop", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		result, err := management.Editor().Update(t.Context(), actor, row.ID, 1, roleInput(t, map[string]query.Value{"username": query.String("Renamed"), "first_name": query.String("Edited"), "staff": query.Boolean(true)}, map[string][]int64{"groups": {}, "permissions": {change.ID}}))
		fields := []string{"username", "first_name", "staff", "groups", "permissions"}
		if err != nil || result.ID != row.ID || result.Revision != 2 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, fields) {
			t.Fatal("selected edit envelope", err)
		}
		row.Username, row.FirstName, row.Staff, row.Revision = "Renamed", "Edited", true, 2
		userReferenceReplace(&before.account, row)
		after := userReferenceSnapshot(t, reopen(t))
		userReferenceRelations(t, &before.account, after.account, row.ID, nil, []int64{change.ID})
		assertUserReferenceState(t, before, after, actor, catalogExpectedEvent{"user", row.ID, admin.ActionChange, fields})
		credential, err := runtime.Authenticator().Authenticate(t.Context(), "Renamed", userReferencePassword)
		if err != nil {
			t.Fatal(err)
		}
		current, err := management.Reader().Get(t.Context(), actor, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		groups, loaded := current.View().Collection("groups")
		if !loaded {
			t.Fatal("selected groups were not loaded")
		}
		actual := profileRow(t, backend, row.ID)
		reference.assert(t, "edited", map[string]any{"username": actual.Username, "first_name": actual.FirstName, "staff": actual.Staff, "same_identity": credential.Principal().ID() == original.Principal().ID() && current.ID() == row.ID, "same_password": credential.MatchesSessionStamp(original.SessionStamp()), "groups": len(groups), "effective": userReferenceGrants(t, runtime, row.PrincipalID)})
		if _, present, err := runtime.SessionStore().Load(t.Context(), targetSession.ID()); err != nil || !present {
			t.Fatal("selected profile edit revoked credential session", err)
		}
		result, err = management.Editor().Update(t.Context(), actor, row.ID, 2, roleInput(t, nil, map[string][]int64{"permissions": {change.ID, change.ID}}))
		if err != nil || result.ID != row.ID || result.Revision != 2 || result.RevokedSessions != 0 || len(result.ChangedFields) != 0 {
			t.Fatal("selected no-op altered metadata", err)
		}
		keys, loaded := current.View().Collection("permissions")
		if !loaded || !slices.Equal(keys, []int64{change.ID}) {
			t.Fatal("selected permission observation differs")
		}
		keys[0] = -999
		keys, _ = current.View().Collection("permissions")
		fresh, err := management.Reader().Get(t.Context(), actor, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		freshKeys, loaded := fresh.View().Collection("permissions")
		if !loaded || !slices.Equal(keys, []int64{change.ID}) || !slices.Equal(freshKeys, keys) {
			t.Fatal("selected profile relation aliases caller state")
		}
		assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), actor)
	}) {
		return
	}
	if !t.Run("audit_write_rollback", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		fault := errors.New("selected reference audit insert failed after write")
		injected := &profileFaultBackend{Backend: backend, mode: "audit_failure", fault: fault}
		failedRuntime := userReferenceRuntime(t, injected, f.base.hasher)
		result, err := failedRuntime.Accounts().Editor().Update(t.Context(), actor, row.ID, 2, roleInput(t, map[string]query.Value{"first_name": query.String("rollback")}, map[string][]int64{"groups": {group.ID}, "permissions": {}}))
		if !errors.Is(err, fault) || !reflect.DeepEqual(result, accountmodel.AccountChange{}) || injected.calls != 1 {
			t.Fatal("selected audit failure did not cross actual write boundary", err)
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
		actual := profileRow(t, backend, row.ID)
		credential, err := runtime.Authenticator().Resolve(t.Context(), row.PrincipalID)
		if err != nil {
			t.Fatal(err)
		}
		current, err := management.Reader().Get(t.Context(), actor, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		groups, loaded := current.View().Collection("groups")
		if !loaded {
			t.Fatal("rollback observation lost selected groups")
		}
		reference.assert(t, "rollback", map[string]any{"first_name": actual.FirstName, "groups": len(groups), "effective": userReferenceGrants(t, runtime, row.PrincipalID), "same_password": credential.MatchesSessionStamp(original.SessionStamp())})
	}) {
		return
	}
	if !t.Run("deactivate_and_reactivate", func(t *testing.T) {
		for _, active := range []bool{false, true} {
			before := userReferenceSnapshot(t, reopen(t))
			result, err := management.Editor().Update(t.Context(), actor, row.ID, row.Revision, profileInput(t, map[string]query.Value{"active": query.Boolean(active)}))
			wantRevoked := 0
			if !active {
				wantRevoked = 1
			}
			if err != nil || result.ID != row.ID || result.Revision != row.Revision+1 || result.RevokedSessions != wantRevoked || !slices.Equal(result.ChangedFields, []string{"active"}) {
				t.Fatal("selected active edit envelope", err)
			}
			row.Active, row.Revision = active, row.Revision+1
			userReferenceReplace(&before.account, row)
			before.account.sessions = slices.DeleteFunc(slices.Clone(before.account.sessions), func(session sessionmodels.Session) bool { return session.ID == targetSessionRow })
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor, catalogExpectedEvent{"user", row.ID, admin.ActionChange, []string{"active"}})
			if _, present, err := runtime.SessionStore().Load(t.Context(), targetSession.ID()); err != nil || present {
				t.Fatal("target session survived deactivation", err)
			}
			credential, err := runtime.Authenticator().Resolve(t.Context(), row.PrincipalID)
			if !active && !errors.Is(err, auth.ErrInvalidCredentials) || active && (err != nil || !credential.MatchesSessionStamp(original.SessionStamp())) {
				t.Fatal("active edit changed credential contract", err)
			}
		}
	}) {
		return
	}
	var browser *userReferenceHTTP
	var browserRow int64
	// Keep the reopened connection alive through the later deletion request.
	reopened := userReferenceRuntime(t, reopen(t), f.base.hasher)
	t.Cleanup(func() {
		if browser != nil {
			browser.close()
		}
	})
	if !t.Run("reopen_and_http_current_permissions", func(t *testing.T) {
		// The server is owned by the lifecycle parent, so the next deletion
		// stage must invalidate the same browser and durable cookie.
		runtime = reopened
		before := userReferenceSnapshot(t, reopen(t))
		browser = newUserReferenceHTTP(t, runtime)
		browserID := browser.login(t, row.Username, userReferencePassword)
		row.LastLogin = &joined
		userReferenceReplace(&before.account, row)
		browserRow = userReferenceSession(t, runtime, original, browserID, before, userReferenceSnapshot(t, reopen(t)), actor)
		browser.expect(t, "/view/", 403)
		browser.expect(t, "/change/", 200)
	}) {
		return
	}
	if !t.Run("host_cascade_session_and_reference_delete", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		result, err := management.Editor().Update(t.Context(), actor, row.ID, 4, roleInput(t, nil, map[string][]int64{"groups": {group.ID}}))
		if err != nil || result.ID != row.ID || result.Revision != 5 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, []string{"groups"}) {
			t.Fatal("selected group reattachment", err)
		}
		row.Revision = 5
		userReferenceReplace(&before.account, row)
		after := userReferenceSnapshot(t, reopen(t))
		userReferenceRelations(t, &before.account, after.account, row.ID, []int64{group.ID}, []int64{change.ID})
		assertUserReferenceState(t, before, after, actor, catalogExpectedEvent{"user", row.ID, admin.ActionChange, []string{"groups"}})
		deleted, err := management.Deleter().Delete(t.Context(), actor, row.ID, 5)
		if err != nil || deleted.ID != row.ID || deleted.Revision != 5 || deleted.DeletedRows != 4 || deleted.RevokedSessions != 1 {
			t.Fatal("selected host deletion envelope", err)
		}
		encoded, err := json.Marshal(deleted)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) != 4 {
			t.Fatal("deletion disclosed non-metadata values", err)
		}
		for _, name := range []string{"ID", "Revision", "DeletedRows", "RevokedSessions"} {
			if _, found := fields[name]; !found {
				t.Fatal("deletion metadata field missing", name)
			}
		}
		after.account.users = slices.DeleteFunc(slices.Clone(after.account.users), func(user models.User) bool { return user.ID == row.ID })
		after.account.memberships = slices.DeleteFunc(slices.Clone(after.account.memberships), func(link models.UserGroupsLink) bool { return link.SourceID == row.ID })
		after.account.direct = slices.DeleteFunc(slices.Clone(after.account.direct), func(link models.UserPermissionsLink) bool { return link.SourceID == row.ID })
		after.account.sessions = slices.DeleteFunc(slices.Clone(after.account.sessions), func(session sessionmodels.Session) bool { return session.ID == browserRow })
		after.notes = slices.DeleteFunc(slices.Clone(after.notes), func(note hostmodels.Note) bool { return note.ID == child.ID })
		final := userReferenceSnapshot(t, reopen(t))
		assertUserReferenceState(t, after, final, actor, catalogExpectedEvent{"user", row.ID, admin.ActionDelete, nil})
		reference.assert(t, "deleted", map[string]any{"user_exists": slices.ContainsFunc(final.account.users, func(user models.User) bool { return user.ID == row.ID }), "memberships": len(final.account.memberships), "group_exists": slices.ContainsFunc(final.account.groups, func(item models.Group) bool { return item.ID == group.ID }), "permission_exists": slices.ContainsFunc(final.account.permissions, func(item models.Permission) bool { return item.ID == view.ID })})
		browser.expect(t, "/change/", 403)
		assertUserReferenceState(t, final, userReferenceSnapshot(t, reopen(t)), actor)
		if len(final.account.audits) != 6 {
			t.Fatal("selected lifecycle omitted audit or audited rollback/no-op")
		}
	}) {
		return
	}
}
