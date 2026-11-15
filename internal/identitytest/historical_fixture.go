package identitytest

import (
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

// Historical migrations and explicit handoff continue to use the original
// identity domain, credentials, session bytes and native storage owner.
const historicalPassword = "original managed-user password"

type historicalIdentityFixture struct {
	backend TransitionBackend
	runtime *systemstate.Runtime
	hasher  auth.PasswordHasher
	actor   auth.Principal
	user    models.User
	root    models.User
	ids     []sessions.ID
}

func newHistoricalIdentityFixture(t *testing.T, backend TransitionBackend, count int) *historicalIdentityFixture {
	t.Helper()
	ctx := t.Context()
	applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "manager", Active: true, Staff: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemstate.ProvisionIdentity(ctx, backend, systemstate.ProvisionIdentityConfig{Principal: actor, Username: "manager", Password: historicalPassword, PasswordHasher: hasher}); err != nil {
		t.Fatal(err)
	}
	runtime, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(ctx, historicalPassword)
	if err != nil {
		t.Fatal(err)
	}
	user, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("managed-member", "member", encoded, time.Now().UTC()).WithStaff(true).WithFirstName("Original").WithEmail("member@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	permission, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(user.ID, permission.ID)); err != nil {
		t.Fatal(err)
	}
	root, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact(actor.ID())).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("root missing", err)
	}
	f := &historicalIdentityFixture{backend: backend, runtime: runtime, hasher: hasher, actor: actor, user: user, root: root}
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Authenticator().Resolve(ctx, user.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	rootCredential, err := runtime.Authenticator().Resolve(ctx, actor.ID())
	if err != nil {
		t.Fatal(err)
	}
	for index := range count {
		values := map[string]string{"unrelated_application_value": "preserve"}
		if index%3 == 0 {
			values[auth.SessionPrincipalIDKey] = user.PrincipalID
			values[auth.SessionCredentialStampKey] = credential.SessionStamp()
		} else if index%3 == 1 {
			values[auth.SessionPrincipalIDKey] = actor.ID()
			values[auth.SessionCredentialStampKey] = rootCredential.SessionStamp()
		}
		record, err := manager.Create(ctx, values)
		if err != nil {
			t.Fatal(err)
		}
		f.ids = append(f.ids, record.ID())
	}
	return f
}

func (f *historicalIdentityFixture) stored(t *testing.T) models.User {
	t.Helper()
	value, present, err := models.UserObjects.Using(f.backend).Filter(models.UserFields.ID.Exact(f.user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !present {
		t.Fatal("managed user missing", err)
	}
	return value
}
