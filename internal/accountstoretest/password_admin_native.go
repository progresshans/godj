package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	websession "github.com/progresshans/godj/web/sessionauth"
)

const passwordAdminRaw = "  administrator native λ password  "

func passwordAdminNativeOwner(t *testing.T, f *loginFixture, backend Backend) *userReferenceOwner {
	t.Helper()
	config := f.config(t)
	config.Accounts.Now = f.clock
	return newUserReferenceOwner(t, backend, f.userReferenceFixture, config)
}

// The previous rows and the hasher's pre-persistence result determine every
// changed value. Fresh audit row keys are checked by the shared native oracle.
func passwordAdminExpected(t *testing.T, f *loginFixture, owner *userReferenceOwner, before userReferenceState) (userReferenceState, accountmodel.PasswordEditResult) {
	t.Helper()
	encoded, ok := owner.encoded.Load().(string)
	if !ok || encoded == "" || encoded == f.target.EncodedPassword {
		t.Fatal("administrative password oracle lacks its fresh upstream hash")
	}
	row := f.target
	row.EncodedPassword, row.Revision = encoded, row.Revision+1
	userReferenceReplace(&before.account, row)
	for _, record := range f.targetSessions {
		loginRemoveRecord(t, &before.account, loginNamespace(t, f.credential), record.ID())
	}
	return before, accountmodel.PasswordEditResult{ID: row.ID, Revision: row.Revision, RevokedSessions: len(f.targetSessions)}
}

func runPasswordAdminNative(t *testing.T, open Open) {
	t.Run("password_admin_native", func(t *testing.T) {
		for _, mode := range []string{"many_sessions_and_http", "corrupt_late_session", "current_group_grant", "group_grant_removed"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionSelfPassword(t, backend)
				var controls [2]sessions.Record
				if mode == "many_sessions_and_http" {
					controls[0] = passwordSession(t, f.runtime.Sessions(), f.actor, "preserved administrative HTTP actor")
					var err error
					controls[1], err = f.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": "preserved administrative HTTP browser"})
					if err != nil {
						t.Fatal(err)
					}
				}
				if mode == "many_sessions_and_http" || mode == "corrupt_late_session" {
					for len(f.targetSessions) < 258 {
						f.targetSessions = append(f.targetSessions, passwordSession(t, f.runtime.Sessions(), f.credential, fmt.Sprintf("admin-batch-%d", len(f.targetSessions))))
					}
				}
				owner := passwordAdminNativeOwner(t, f, backend)
				actor := f.actor.Principal()
				var grant models.GroupPermissionsLink
				if mode == "current_group_grant" || mode == "group_grant_removed" {
					var err error
					current, err := models.UserObjects.Patch(t.Context(), backend, f.base.user, models.UserPatch{}.WithSuperuser(false).WithRevision(f.base.user.Revision+1))
					if err != nil {
						t.Fatal(err)
					}
					config := f.config(t)
					permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate(string(config.Accounts.Permissions.Change), "Change password"))
					if err != nil {
						t.Fatal(err)
					}
					group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Native password editors"))
					if err != nil {
						t.Fatal(err)
					}
					grant, err = models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID))
					if err != nil {
						t.Fatal(err)
					}
					if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(current.ID, group.ID)); err != nil {
						t.Fatal(err)
					}
					credential, err := owner.runtime.Authenticator().Resolve(t.Context(), current.PrincipalID)
					if err != nil || credential.Principal().Superuser() || !credential.Principal().Has(config.Accounts.Permissions.Change) {
						t.Fatal("native password fixture lacks its current group permission", err)
					}
					if mode == "current_group_grant" {
						denied, err := hostproject.NewAccountPasswordEditor(owner.runtime.SessionStore(), owner.runtime.Authenticator(), denyAccountPermission{}, accountmodel.PasswordEditConfig[models.User]{ChangePermission: config.Accounts.Permissions.Change, ValidatePassword: config.Accounts.ValidatePassword, Now: f.clock})
						if err != nil {
							t.Fatal(err)
						}
						before := userReferenceSnapshot(t, reopen(t))
						result, err := denied.SetPassword(t.Context(), actor, f.target.ID, f.target.Revision, passwordAdminRaw)
						passwordEditNoResult(t, result, err)
						requireProfileCode(t, err, accountmodel.CodePermission)
						if owner.hasher.hashes.Load() != 0 || owner.boundary.calls != 0 || owner.policies.Load() != 0 {
							t.Fatal("deny overlay admitted password work")
						}
						assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
						// Supply the same bound identity with no cached privileges.
						bare, err := auth.NewPrincipal(auth.PrincipalConfig{ID: actor.ID(), Active: true})
						if err != nil {
							t.Fatal(err)
						}
						domain, _ := actor.CredentialDomain()
						input, err := auth.NewCredentialForDomain(domain, current.Username, current.EncodedPassword, bare)
						if err != nil {
							t.Fatal(err)
						}
						actor = input.Principal()
					}
				}
				if mode == "corrupt_late_session" {
					last := f.targetSessions[len(f.targetSessions)-1]
					namespace := loginNamespace(t, f.credential)
					row, found, err := sessionmodels.SessionObjects.Using(backend).Filter(sessionmodels.SessionFields.Namespace.Exact(namespace), sessionmodels.SessionFields.Digest.Exact(loginDigest(namespace, last.ID()))).OrderBy(sessionmodels.SessionFields.ID.Asc()).First(t.Context())
					if err != nil || !found {
						t.Fatal("late corrupt session fixture missing", err)
					}
					if _, err := sessionmodels.SessionObjects.Patch(t.Context(), backend, row, sessionmodels.SessionPatch{}.WithPayload("private invalid late payload")); err != nil {
						t.Fatal(err)
					}
				}
				before := userReferenceSnapshot(t, reopen(t))
				entropy := f.entropy.count()
				expected := before
				if mode == "group_grant_removed" {
					expected.account.grants = slices.DeleteFunc(slices.Clone(expected.account.grants), func(row models.GroupPermissionsLink) bool { return row.ID == grant.ID })
					writer := reopen(t)
					owner.hasher.beforeHash = func(ctx context.Context) error {
						ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
						defer cancel()
						return writer.CoordinatedAtomic(ctx, func(scope db.Session) error {
							count, err := models.GroupPermissionsLinkObjects.Delete(ctx, scope, &grant)
							if err != nil || count != 1 {
								return errors.Join(err, errors.New("password hash did not remove current group permission"))
							}
							return nil
						})
					}
				}
				effects := []string{}
				faults := 0
				owner.boundary.decorate = func(scope db.Session) db.Session {
					return &passwordWriteFaultScope{Session: scope, effects: &effects, faults: &faults}
				}
				result, err := owner.runtime.Accounts().Passwords().SetPassword(t.Context(), actor, f.target.ID, f.target.Revision, passwordAdminRaw)
				if owner.hasher.hashes.Load() != 1 || owner.hasher.verifies.Load() != 0 || owner.boundary.calls != 1 || f.entropy.count() != entropy || faults != 0 {
					t.Fatal("administrative command repeated preparation or changed session entropy")
				}
				after := userReferenceSnapshot(t, reopen(t))
				if mode == "group_grant_removed" || mode == "corrupt_late_session" {
					passwordEditNoResult(t, result, err)
					code := accountmodel.CodePersistence
					policies := int64(2)
					if mode == "group_grant_removed" {
						code, policies = accountmodel.CodePermission, 1
						if len(effects) != 0 {
							t.Fatal("revoked group permission reached native writes")
						}
					} else if len(effects) < 2 || effects[0] != "password" || slices.Contains(effects, "audit") || slices.ContainsFunc(effects[1:], func(effect string) bool { return effect != "revoke" }) {
						t.Fatal("late corruption did not roll back an actual password update and earlier deletions", effects)
					}
					requireProfileCode(t, err, code)
					if owner.policies.Load() != policies {
						t.Fatal("administrative final policy ordering changed")
					}
					assertUserReferenceState(t, expected, after, actor)
					selfPasswordPrivate(t, err, "private invalid late payload", passwordAdminRaw, f.target.EncodedPassword)
					return
				}
				expected, wanted := passwordAdminExpected(t, f, owner, before)
				if err != nil || result != wanted || owner.policies.Load() != 2 {
					t.Fatal("administrative result lost its exact committed effect", err)
				}
				currentActor, err := owner.runtime.Authenticator().Resolve(t.Context(), actor.ID())
				if err != nil {
					t.Fatal(err)
				}
				assertUserReferenceStateAt(t, expected, after, currentActor.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				selfPasswordPrivate(t, result, passwordAdminRaw, owner.encoded.Load().(string), f.credential.SessionStamp())
				if mode == "many_sessions_and_http" {
					passwordAdminHTTP(t, f, reopen(t), after, owner.encoded.Load().(string), controls)
				}
			})
		}
	})
	t.Run("password_admin_commit_failures", func(t *testing.T) { passwordAdminCommitFailures(t, open) })
}

func passwordAdminHTTP(t *testing.T, f *loginFixture, backend Backend, before userReferenceState, encoded string, controls [2]sessions.Record) {
	t.Helper()
	owner := passwordAdminNativeOwner(t, f, backend)
	credential, err := owner.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, passwordAdminRaw)
	if err != nil || credential.MatchesSessionStamp(f.credential.SessionStamp()) {
		t.Fatal("administrative replacement did not survive reconnection", err)
	}
	if _, err := owner.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, password); err != auth.ErrInvalidCredentials {
		t.Fatal("administrative replacement retained the old password", err)
	}
	for _, mode := range []string{"target", "actor", "anonymous"} {
		browser := newUserReferenceHTTPConfigured(t, owner.runtime, f.clock, 204)
		address, err := url.Parse(browser.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		want := 403
		id := f.targetSessions[0].ID()
		if mode != "target" {
			id = controls[0].ID()
			if mode == "actor" {
				want = 200
			} else {
				id = controls[1].ID()
			}
		}
		browser.client.Jar.SetCookies(address, []*http.Cookie{{Name: websession.DefaultSessionCookieName, Value: id.Encoded(), Path: "/"}})
		browser.expect(t, "/view/", want)
		browser.close()
	}
	assertUserReferenceState(t, before, userReferenceSnapshot(t, backend), f.actor.Principal())
	browser := newUserReferenceHTTPConfigured(t, owner.runtime, f.clock, 204)
	defer browser.close()
	browser.loginAttempt(t, f.target.Username, password, false, 401)
	assertUserReferenceState(t, before, userReferenceSnapshot(t, backend), f.actor.Principal())
	entropy := f.entropy.count()
	id := browser.login(t, f.target.Username, passwordAdminRaw)
	if id != f.entropy.one(t, entropy) {
		t.Fatal("administrative HTTP login published a different bearer")
	}
	row := f.target
	row.EncodedPassword, row.Revision = encoded, row.Revision+1
	expectedRecord := loginExpectedRecord(t, credential, sessions.Record{}, "create", id, f.clock())
	actual := userReferenceSnapshot(t, backend)
	expected := loginExpectedState(t, before, actual, credential, row, sessions.Record{}, expectedRecord, f.clock())
	assertUserReferenceState(t, expected, actual, f.actor.Principal())
	browser.expect(t, "/view/", 200)
	if !reflect.DeepEqual(actual, userReferenceSnapshot(t, backend)) {
		t.Fatal("same-time HTTP permission read changed unrelated state")
	}
}
