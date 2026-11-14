package accountstoretest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostaccounts "github.com/progresshans/godj/conformance/identityfixture/accounts"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

const userReferencePassword = "original-reference-password"

func loadUserReference(t *testing.T, vendor string) identityObservationReference {
	t.Helper()
	raw, err := identityreference.Management(vendor)
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
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 4 || len(reference.Observations) != 6 {
		t.Fatal("user management reference is incomplete or belongs to another runtime")
	}
	for _, name := range []string{"admin_options", "auth_admin", "auth_models", "base_user"} {
		value := reference.Sources[name]
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != value {
			t.Fatal("missing independent user source digest", name)
		}
	}
	for _, name := range []string{"created", "edited", "rollback", "deleted", "email_normalization", "admission"} {
		if _, found := reference.Observations[name]; !found {
			t.Fatal("missing independent user observation", name)
		}
	}
	return reference.Observations
}

// Observe every account and host table through one original native snapshot.
// None of the management readers or injected write scopes supplies the oracle.
type userReferenceState struct {
	account      accountNativeState
	notes        []hostmodels.Note
	guards       []hostmodels.Guard
	accessNotes  []hostmodels.AccessNote
	accessGuards []hostmodels.AccessGuard
}

func userReferenceSnapshot(t *testing.T, backend Backend) userReferenceState {
	t.Helper()
	var state userReferenceState
	visits := 0
	err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		visits++
		if visits != 1 {
			return errors.New("repeated native user observation")
		}
		state.account = accountNativeRows(t, scope)
		state.notes = deletionRows(t, scope, hostmodels.NoteObjects)
		state.guards = deletionRows(t, scope, hostmodels.GuardObjects)
		state.accessNotes = deletionRows(t, scope, hostmodels.AccessNoteObjects)
		state.accessGuards = deletionRows(t, scope, hostmodels.AccessGuardObjects)
		return nil
	})
	if err != nil || visits != 1 {
		t.Fatal("native user observation did not complete", err)
	}
	return state
}

func assertUserReferenceState(t *testing.T, expected, actual userReferenceState, actor auth.Principal, events ...catalogExpectedEvent) {
	t.Helper()
	assertCatalogRows(t, expected.account, actual.account, actor, events...)
	if !reflect.DeepEqual(expected.notes, actual.notes) || !reflect.DeepEqual(expected.guards, actual.guards) ||
		!reflect.DeepEqual(expected.accessNotes, actual.accessNotes) || !reflect.DeepEqual(expected.accessGuards, actual.accessGuards) {
		t.Fatal("selected user operation changed unexpected host rows")
	}
}

type userReferenceFixture struct {
	base    *fixture
	runtime *accountmodel.Runtime[models.User, models.Group, models.Permission]
	actor   auth.Credential
	hasher  *passwordChangeHasher
	encoded atomic.Value
}

func userReferenceRuntime(t *testing.T, backend Backend, hasher auth.PasswordHasher) *accountmodel.Runtime[models.User, models.Group, models.Permission] {
	t.Helper()
	config, err := hostaccounts.Policy()
	if err != nil {
		t.Fatal(err)
	}
	config.Accounts.Now = func() time.Time { return joined }
	config.Catalog.Now = config.Accounts.Now
	config.Sessions.Clock = config.Accounts.Now
	config.Accounts.PrincipalID = func(context.Context) (string, error) { return "reference-created", nil }
	runtime, err := hostproject.NewAccountRuntime(t.Context(), backend, hasher, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func provisionUserReference(t *testing.T, backend Backend) *userReferenceFixture {
	t.Helper()
	f := &userReferenceFixture{base: provisionCatalogHost(t, backend).account}
	if _, err := models.UserObjects.Patch(t.Context(), backend, f.base.user, models.UserPatch{}.WithStaff(true).WithSuperuser(true).WithRevision(2)); err != nil {
		t.Fatal(err)
	}
	f.base.user = profileRow(t, backend, f.base.user.ID)
	// The reference's global membership count remains meaningful at deletion.
	for _, link := range accountNativeSnapshot(t, backend).memberships {
		if _, err := models.UserGroupsLinkObjects.Delete(t.Context(), backend, &link); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("unrelated note", f.base.user.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(f.base.user.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := hostmodels.AccessNoteObjects.Create(t.Context(), backend, hostmodels.NewAccessNoteCreate("unrelated access", f.base.grants[0].ID).WithGroupID(f.base.group.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := hostmodels.AccessGuardObjects.Create(t.Context(), backend, hostmodels.NewAccessGuardCreate().WithGroupID(f.base.group.ID).WithPermissionID(f.base.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	tracked := &creationOwner{Backend: backend}
	f.hasher = &passwordChangeHasher{PasswordHasher: f.base.hasher, scopes: &tracked.scopes, encode: func(ctx context.Context, raw string) (string, error) {
		encoded, err := f.base.hasher.Hash(ctx, raw)
		if err == nil {
			f.encoded.Store(encoded)
		}
		return encoded, err
	}}
	f.runtime = userReferenceRuntime(t, tracked, f.hasher)
	var err error
	f.actor, err = f.runtime.Authenticator().Authenticate(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	f.hasher.hashes.Store(0)
	return f
}

func userReferenceGrants(t *testing.T, runtime *accountmodel.Runtime[models.User, models.Group, models.Permission], principal string) []string {
	t.Helper()
	credential, err := runtime.Authenticator().Resolve(t.Context(), principal)
	if err != nil {
		t.Fatal(err)
	}
	return catalogReferenceCodes(t, credential.Principal().Permissions())
}

func userReferenceExpectedRow(id int64, login, email, encoded string) models.User {
	row := models.NewUserWithID(id)
	row.PrincipalID, row.Username, row.Email, row.EncodedPassword = "reference-created", login, email, encoded
	row.Active, row.DateJoined, row.Revision = true, joined, 1
	return row
}

var userReferenceCreationFields = []string{"username", "encoded_password", "first_name", "last_name", "email", "active", "staff", "superuser", "groups", "permissions"}

// Only an allocated key is learned from the after-state. All link semantics
// are rebuilt from requested pairs; retained and unrelated links keep their IDs.
func userReferenceLinks[L any](t *testing.T, before, after []L, source int64, targets []int64, parts func(L) (int64, int64, int64), makeLink func(int64, int64, int64) L) []L {
	t.Helper()
	pending, known := map[int64]bool{}, map[int64]bool{}
	for _, target := range targets {
		pending[target] = true
	}
	expected := make([]L, 0, len(before)+len(targets))
	for _, row := range before {
		id, owner, target := parts(row)
		known[id] = true
		if owner != source || pending[target] {
			expected = append(expected, row)
			if owner == source {
				delete(pending, target)
			}
		}
	}
	for _, row := range after {
		id, owner, target := parts(row)
		if known[id] {
			continue
		}
		if id <= 0 || owner != source || !pending[target] {
			t.Fatal("unexpected user relation allocation")
		}
		known[id] = true
		delete(pending, target)
		expected = append(expected, makeLink(id, source, target))
	}
	if len(pending) != 0 {
		t.Fatal("user operation omitted requested relation")
	}
	slices.SortFunc(expected, func(a, b L) int {
		x, _, _ := parts(a)
		y, _, _ := parts(b)
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	})
	return expected
}

func userReferenceRelations(t *testing.T, expected *accountNativeState, after accountNativeState, id int64, groups, permissions []int64) {
	t.Helper()
	expected.memberships = userReferenceLinks(t, expected.memberships, after.memberships, id, groups,
		func(row models.UserGroupsLink) (int64, int64, int64) { return row.ID, row.SourceID, row.TargetID },
		func(key, source, target int64) models.UserGroupsLink {
			row := models.NewUserGroupsLinkWithID(key)
			row.SourceID, row.TargetID = source, target
			return row
		})
	expected.direct = userReferenceLinks(t, expected.direct, after.direct, id, permissions,
		func(row models.UserPermissionsLink) (int64, int64, int64) { return row.ID, row.SourceID, row.TargetID },
		func(key, source, target int64) models.UserPermissionsLink {
			row := models.NewUserPermissionsLinkWithID(key)
			row.SourceID, row.TargetID = source, target
			return row
		})
}

func userReferenceReplace(state *accountNativeState, row models.User) {
	state.users = slices.Clone(state.users)
	for i := range state.users {
		if state.users[i].ID == row.ID {
			state.users[i] = row
			return
		}
	}
	state.users = append(state.users, row)
}

// A setup session is bound through the public store and an exact one-row native
// delta before it becomes an independent revocation target in later operations.
func userReferenceSession(t *testing.T, runtime *accountmodel.Runtime[models.User, models.Group, models.Permission], credential auth.Credential, id sessions.ID, before, after userReferenceState, actor auth.Principal) int64 {
	t.Helper()
	record, present, err := runtime.SessionStore().Load(t.Context(), id)
	if err != nil || !present {
		t.Fatal("session lacks a durable selected owner", err)
	}
	principal, _ := record.Value(auth.SessionPrincipalIDKey)
	stamp, _ := record.Value(auth.SessionCredentialStampKey)
	if principal != credential.Principal().ID() || !credential.MatchesSessionStamp(stamp) {
		t.Fatal("session changed principal or credential")
	}
	known := map[int64]bool{}
	for _, row := range before.account.sessions {
		known[row.ID] = true
	}
	var added []sessionmodels.Session
	for _, row := range after.account.sessions {
		if !known[row.ID] {
			added = append(added, row)
		}
	}
	if len(added) != 1 || added[0].ID <= 0 {
		t.Fatal("session setup did not add exactly one row")
	}
	before.account.sessions = append(slices.Clone(before.account.sessions), added[0])
	slices.SortFunc(before.account.sessions, func(a, b sessionmodels.Session) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	assertUserReferenceState(t, before, after, actor)
	return added[0].ID
}
