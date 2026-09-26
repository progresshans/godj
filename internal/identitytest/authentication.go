package identitytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
)

func RunAuthentication(t *testing.T, backend DirectoryBackend) {
	t.Helper()
	ctx := t.Context()
	loaded, _, err := definition.Load(identity.MigrationSources()...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(ctx, "original password")
	if err != nil {
		t.Fatal(err)
	}
	directory, err := identity.NewDirectory(backend)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := identity.NewAuthenticator(ctx, directory, hasher)
	if err != nil {
		t.Fatal(err)
	}
	joined := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	user, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("http-member", "member", encoded, joined))
	if err != nil {
		t.Fatal(err)
	}
	group, err := models.GroupObjects.Create(ctx, backend, models.NewGroupCreate("HTTP readers"))
	if err != nil {
		t.Fatal(err)
	}
	permission, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.view", "View"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(ctx, backend, models.NewUserGroupsLinkCreate(user.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	grant, err := models.GroupPermissionsLinkObjects.Create(ctx, backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		for _, staff := range []bool{false, true} {
			for _, superuser := range []bool{false, true} {
				key := fmt.Sprintf("%d%d%d", boolNumber(active), boolNumber(staff), boolNumber(superuser))
				t.Run("roles_"+key, func(t *testing.T) {
					if _, err := models.UserObjects.Update(ctx, backend, user, models.UserPatch{}.WithActive(active).WithStaff(staff).WithSuperuser(superuser)); err != nil {
						t.Fatal(err)
					}
					credential, err := authenticator.Authenticate(ctx, "member", "original password")
					assertReferenceRole(t, key, credential, err)
					resolved, resolveErr := authenticator.Resolve(ctx, user.PrincipalID)
					assertReferenceRole(t, key, resolved, resolveErr)
					if active && (resolved.Principal().Staff() != staff || resolved.Principal().Superuser() != superuser) {
						t.Fatal("stored role flags were lost")
					}
				})
			}
		}
	}
	if _, err := models.UserObjects.Update(ctx, backend, user, models.UserPatch{}.WithActive(true).WithStaff(false).WithSuperuser(false)); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"password", "rehash", "username", "inactive", "deleted", "authorization"} {
		t.Run("verify_interleave_"+mode, func(t *testing.T) {
			// A single SQLite connection would deadlock if password verification kept
			// the read transaction or its admission lease. All mutation uses its ctx.
			target, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("interleave-"+mode, "interleave-"+mode, encoded, joined))
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := hasher.Hash(ctx, "new password")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "rehash" {
				replacement, err = hasher.Hash(ctx, "original password")
				if err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			probe := verificationMutation{PasswordHasher: hasher, mutate: func(ctx context.Context) error {
				calls++
				if mode == "deleted" {
					deleters, err := project.BindRelationDeleters()
					if err != nil {
						return err
					}
					_, err = deleters.IdentityUser.Delete(ctx, backend, &target)
					return err
				}
				return backend.Atomic(ctx, func(session db.Session) error {
					patch := models.UserPatch{}
					switch mode {
					case "password", "rehash":
						patch = patch.WithEncodedPassword(replacement)
					case "username":
						patch = patch.WithUsername("renamed-" + mode)
					case "inactive":
						patch = patch.WithActive(false)
					case "authorization":
						patch = patch.WithStaff(true).WithSuperuser(true)
					}
					_, err := models.UserObjects.Update(ctx, session, target, patch)
					return err
				})
			}}
			reader, err := identity.NewAuthenticator(ctx, directory, probe)
			if err != nil {
				t.Fatal(err)
			}
			operation, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			credential, err := reader.Authenticate(operation, target.Username, "original password")
			if mode == "authorization" {
				if err != nil || !credential.Principal().Staff() || !credential.Principal().Superuser() {
					t.Fatal("fresh roles were not admitted", err)
				}
			} else if !errors.Is(err, auth.ErrInvalidCredentials) || credential.Principal().ID() != "" {
				t.Fatal("changed credential authenticated or password work retained database ownership", err)
			}
			if calls != 1 {
				t.Fatal("password verification was retried", calls)
			}
		})
	}
	runAuthenticationHTTP(t, backend, directory, hasher, user, grant, permission, encoded, joined)
}

func boolNumber(value bool) int {
	if value {
		return 1
	}
	return 0
}

type verificationMutation struct {
	auth.PasswordHasher
	mutate func(context.Context) error
}

func (hasher verificationMutation) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if err := hasher.mutate(ctx); err != nil {
		return false, err
	}
	return hasher.PasswordHasher.Verify(ctx, password, encoded)
}

func assertReferenceRole(t *testing.T, key string, credential auth.Credential, failure error) {
	t.Helper()
	for _, backend := range []string{"sqlite", "postgres"} {
		payload, err := identityReferences.ReadFile("testdata/identity-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Observations struct {
				Roles map[string]struct {
					Authenticates bool `json:"backend_may_authenticate"`
					Registered    bool `json:"registered_grant"`
					Ungranted     bool `json:"ungranted_permission"`
					Unregistered  bool `json:"unregistered_permission"`
				}
			}
		}
		if err := json.Unmarshal(payload, &reference); err != nil {
			t.Fatal(err)
		}
		row, found := reference.Observations.Roles[key]
		if !found {
			t.Fatal("missing pinned role observation")
		}
		principal := credential.Principal()
		if !row.Authenticates {
			if !errors.Is(failure, auth.ErrInvalidCredentials) || principal.ID() != "" {
				t.Fatal("inactive account authenticated", key, failure)
			}
			continue
		}
		if failure != nil || principal.Has("helpdesk.ticket.view") != row.Registered || principal.Has("helpdesk.ticket.change") != row.Ungranted || principal.Has("unregistered.permission") != row.Unregistered {
			t.Fatal("stored authentication/permission differs from pinned Django", backend, key, failure)
		}
		// GoDj validates its permission grammar before superuser evaluation. This
		// deliberate difference must not be hidden by oracle normalization.
		if principal.Has("not-a-permission") {
			t.Fatal("superuser accepted malformed permission")
		}
	}
}
