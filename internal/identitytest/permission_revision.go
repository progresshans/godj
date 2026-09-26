package identitytest

import (
	"errors"
	"reflect"
	"testing"

	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

// The initialized domain is placed at its historical schema, then upgraded
// without replacing credentials, permission keys, memberships or session bytes.
func RunPermissionRevisionMigration(t *testing.T, backend TransitionBackend) {
	t.Helper()
	f := newManagementFixture(t, backend, 3)
	managementHost(t, backend)
	group, permission, _ := managementUserSelections(t, f)
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	note, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("preserve host reference", permission.ID).WithGroupID(group.ID))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := hostmodels.AccessGuardObjects.Create(t.Context(), backend, hostmodels.NewAccessGuardCreate().WithPermissionID(permission.ID).WithGroupID(group.ID))
	if err != nil {
		t.Fatal(err)
	}
	user := f.stored(t)
	credential, err := f.runtime.Authenticator().Resolve(t.Context(), user.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	sessions, audit := snapshotIdentitySystemRows(t, backend)
	loaded, _, err := definition.Load(managementHostSources(t)...)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrations.Executor{Backend: backend}
	if _, err := executor.Migrate(t.Context(), loaded, migrations.TargetedLifecycleRequest(migrations.NamedTarget(identity.InitialMigrationKey()))); err != nil {
		t.Fatal("historical identity schema", err)
	}
	config := systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512}
	if runtime, err := systemstate.OpenIdentity(t.Context(), backend, config); runtime != nil || !errors.Is(err, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) {
		t.Fatal("unmigrated identity was silently adopted", err)
	}
	state, err := executor.Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		t.Fatal("upgrade populated identity", err)
	}
	actual, present := state.Schema(modeldef.AppLabel)
	want, err := modeldef.Schema()
	if err != nil || !present || !reflect.DeepEqual(actual, want) {
		t.Fatal("migration does not reach current identity declaration", err)
	}
	updated, found, err := models.PermissionObjects.Using(backend).Filter(models.PermissionFields.ID.Exact(permission.ID)).OrderBy(models.PermissionFields.ID.Asc()).First(t.Context())
	if err != nil || !found || updated.Code != permission.Code || updated.Name != permission.Name || updated.Revision != 1 {
		t.Fatal("permission identity or revision was lost", err)
	}
	if !reflect.DeepEqual(f.stored(t), user) {
		t.Fatal("upgrade changed credential or user revision")
	}
	afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
	if !reflect.DeepEqual(sessions, afterSessions) || !reflect.DeepEqual(audit, afterAudit) {
		t.Fatal("upgrade changed session/audit bytes")
	}
	storedNote, found, err := hostmodels.AccessNoteObjects.Using(backend).Filter(hostmodels.AccessNoteFields.ID.Exact(note.ID)).OrderBy(hostmodels.AccessNoteFields.ID.Asc()).First(t.Context())
	if err != nil || !found || !reflect.DeepEqual(storedNote, note) {
		t.Fatal("upgrade changed host CASCADE/SET_NULL reference", err)
	}
	storedGuard, found, err := hostmodels.AccessGuardObjects.Using(backend).Filter(hostmodels.AccessGuardFields.ID.Exact(guard.ID)).OrderBy(hostmodels.AccessGuardFields.ID.Asc()).First(t.Context())
	if err != nil || !found || !reflect.DeepEqual(storedGuard, guard) {
		t.Fatal("upgrade changed host PROTECT reference", err)
	}
	runtime, err := systemstate.OpenIdentity(t.Context(), backend, config)
	if err != nil {
		t.Fatal(err)
	}
	current, err := runtime.Authenticator().Resolve(t.Context(), user.PrincipalID)
	if err != nil || current.SessionStamp() != credential.SessionStamp() || !reflect.DeepEqual(current.Principal().Permissions(), credential.Principal().Permissions()) {
		t.Fatal("upgrade changed grants or session binding", err)
	}
	if _, err := runtime.Authenticator().Authenticate(t.Context(), user.Username, managementOldPassword); err != nil {
		t.Fatal("upgrade lost password login", err)
	}
}
