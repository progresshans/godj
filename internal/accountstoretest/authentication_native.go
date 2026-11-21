package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/validation"
)

func runAuthenticationNative(t *testing.T, open Open) {
	t.Run("stored_credential_integrity", func(t *testing.T) {
		for _, mode := range []string{"revision", "credential", "username", "principal"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionSelfPassword(t, backend)
				store, err := hostproject.NewAccountCredentialStore(backend)
				if err != nil {
					t.Fatal(err)
				}
				patch := models.UserPatch{}
				switch mode {
				case "revision":
					patch = patch.WithRevision(0)
				case "credential":
					patch = patch.WithEncodedPassword("")
				case "username":
					patch = patch.WithUsername(" private-malformed-login")
				case "principal":
					patch = patch.WithPrincipalID(" private-malformed-principal")
				}
				// Write through the typed ORM, outside cooperative account
				// services. These values are representable by both native DBs.
				if _, err := models.UserObjects.Patch(t.Context(), backend, f.target, patch); err != nil {
					t.Fatal(err)
				}
				before := userReferenceSnapshot(t, reopen(t))
				entropy := f.entropy.count()
				var credential auth.Credential
				var found bool
				if mode == "principal" {
					credential, found, err = store.CredentialByUsername(t.Context(), f.target.Username)
				} else {
					credential, found, err = store.CredentialByID(t.Context(), f.target.PrincipalID)
				}
				if !errors.Is(err, &auth.Error{Code: auth.CodeCredential}) || found || !reflect.DeepEqual(credential, auth.Credential{}) {
					t.Fatal("stored damage published a credential or changed classification", err)
				}
				selfPasswordPrivate(t, err, "private-malformed", f.target.EncodedPassword)
				reader := f.runtime.Accounts().Reader()
				profile, failure := reader.Get(t.Context(), f.actor.Principal(), f.target.ID)
				if failure == nil || profile.ID() != 0 || profile.Revision() != 0 || profile.View().Values() != nil {
					t.Fatal("stored damage published a profile")
				}
				if _, rejected := validation.Rejected(failure); rejected {
					t.Fatal("stored damage became ordinary input diagnostics")
				}
				selfPasswordPrivate(t, failure, "private-malformed", f.target.EncodedPassword)
				page, failure := reader.List(t.Context(), f.actor.Principal(), 0, 100)
				if failure == nil || page.Items != nil || page.Total != 0 {
					t.Fatal("stored damage published the valid prefix of a profile list")
				}
				history, failure := reader.History(t.Context(), f.actor.Principal(), f.target.ID, 100)
				if failure == nil || history != nil {
					t.Fatal("stored damage published profile history")
				}
				if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.entropy.count() != entropy {
					t.Fatal("failed stored observation did password or bearer work")
				}
				assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				// A separate connection restores exactly the original row. A
				// failed observation must not poison a later valid read.
				if _, err := models.UserObjects.Patch(t.Context(), reopen(t), f.target, models.UserPatch{}.
					WithRevision(f.target.Revision).WithEncodedPassword(f.target.EncodedPassword).
					WithUsername(f.target.Username).WithPrincipalID(f.target.PrincipalID)); err != nil {
					t.Fatal(err)
				}
				credential, found, err = store.CredentialByID(t.Context(), f.target.PrincipalID)
				if err != nil || !found || !reflect.DeepEqual(credential.Principal(), f.credential.Principal()) || !credential.MatchesSessionStamp(f.credential.SessionStamp()) {
					t.Fatal("restored native account did not resolve", err)
				}
			})
		}
	})
	t.Run("authentication_current_state", func(t *testing.T) {
		for _, mode := range []string{"password", "rehash", "username", "inactive", "deleted", "authorization"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionLogin(t, backend, false)
				expected := userReferenceSnapshot(t, reopen(t))
				row := f.target
				patch := models.UserPatch{}
				var err error
				switch mode {
				case "password", "rehash":
					raw := password + "-replacement"
					if mode == "rehash" {
						raw = password
					}
					row.EncodedPassword, err = f.base.hasher.Hash(t.Context(), raw)
					if err != nil || row.EncodedPassword == f.target.EncodedPassword {
						t.Fatal("credential change lacks an independently salted hash", err)
					}
					patch = patch.WithEncodedPassword(row.EncodedPassword)
				case "username":
					row.Username = "renamed-member"
					patch = patch.WithUsername(row.Username)
				case "inactive":
					row.Active = false
					patch = patch.WithActive(false)
				case "authorization":
					row.Staff, row.Superuser = true, true
					patch = patch.WithStaff(true).WithSuperuser(true)
				}
				if mode == "deleted" {
					expected.account.users = slices.DeleteFunc(slices.Clone(expected.account.users), func(value models.User) bool { return value.ID == row.ID })
					expected.account.direct = slices.DeleteFunc(slices.Clone(expected.account.direct), func(value models.UserPermissionsLink) bool { return value.SourceID == row.ID })
				} else {
					userReferenceReplace(&expected.account, row)
				}
				tracked := &passwordScopeOwner{Backend: backend}
				mutations := 0
				hasher := &passwordChangeHasher{PasswordHasher: f.base.hasher, scopes: &tracked.scopes, afterVerify: func(ctx context.Context) error {
					mutations++
					// Both DB fixtures have one connection. This uses the SAME
					// owner and operation context, so a retained snapshot or
					// admission lease cannot be hidden by a second connection.
					if mode == "deleted" {
						deleters, err := hostproject.BindRelationDeleters()
						if err != nil {
							return err
						}
						return backend.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error {
							_, err := deleters.AccountsUser.DeleteInSession(ctx, scope, row)
							return err
						})
					}
					return backend.CoordinatedAtomic(ctx, func(scope db.Session) error {
						_, err := models.UserObjects.Patch(ctx, scope, f.target, patch)
						return err
					})
				}}
				verifier, err := hostproject.NewAccountAuthenticator(t.Context(), tracked, hasher)
				if err != nil {
					t.Fatal(err)
				}
				hasher.hashes.Store(0) // The constructor's bounded dummy hash.
				entropy := f.entropy.count()
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				credential, failure := verifier.Authenticate(ctx, f.target.Username, password)
				if mode == "authorization" {
					if failure != nil || !credential.Principal().Staff() || !credential.Principal().Superuser() || !credential.MatchesSessionStamp(f.credential.SessionStamp()) || credential.Principal().ID() != f.target.PrincipalID || !reflect.DeepEqual(credential.Principal().Permissions(), f.credential.Principal().Permissions()) {
						t.Fatal("authentication did not publish the current native roles", failure)
					}
				} else if failure != auth.ErrInvalidCredentials || !reflect.DeepEqual(credential, auth.Credential{}) {
					t.Fatal("changed credential authenticated or lost denial classification", failure)
				}
				if mutations != 1 || hasher.verifies.Load() != 1 || hasher.hashes.Load() != 0 || tracked.scopes.Load() != 0 || tracked.writes.Load() != 0 || f.entropy.count() != entropy {
					t.Fatal("authentication retried password work or retained/wrote native state")
				}
				assertUserReferenceState(t, expected, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
			})
		}
	})
	t.Run("authentication_http", func(t *testing.T) { selectedAuthenticationHTTP(t, open) })
}

// The expected credential is assembled from the independently known row,
// permission set and fixture domain, without re-reading the product store.
func authenticationExpected(t *testing.T, f *loginFixture, row models.User, grants ...auth.Permission) auth.Credential {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser, Permissions: grants})
	if err != nil {
		t.Fatal(err)
	}
	domain, bound := f.credential.Principal().CredentialDomain()
	if !bound {
		t.Fatal("native authentication fixture lacks its selected domain")
	}
	credential, err := auth.NewCredentialForDomain(domain, row.Username, row.EncodedPassword, principal)
	if err != nil {
		t.Fatal(err)
	}
	return credential
}
