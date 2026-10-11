package identitytest

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	identityadmin "github.com/progresshans/godj/identity/admin"
	identityapi "github.com/progresshans/godj/identity/api"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web/sessionauth"
)

//go:embed testdata/unusable-password-django61-*.json
var unusablePasswordReferences embed.FS

func assertUnusableReference(t *testing.T, key string, actual any) {
	t.Helper()
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var observation any
	if err := json.Unmarshal(encoded, &observation); err != nil {
		t.Fatal(err)
	}
	for _, backend := range []string{"sqlite", "postgres"} {
		data, err := unusablePasswordReferences.ReadFile("testdata/unusable-password-django61-" + backend + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django       string
			Observations map[string]any
		}
		if err := json.Unmarshal(data, &reference); err != nil || reference.Django != "6.1" {
			t.Fatal("invalid unusable password reference", err)
		}
		if !reflect.DeepEqual(reference.Observations[key], observation) {
			t.Fatalf("%s differs from %s reference: %#v", key, backend, observation)
		}
	}
}

func RunUnusablePasswords(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("creation_resolution_rotation_and_restore", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 6)
		validatorCalls := 0
		validator := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors { validatorCalls++; return validation.NewErrors() })
		manager, err := identity.NewManager(f.runtime, f.hasher, auth.PrincipalAuthorizer{}, identity.WithPasswordValidators(validator))
		if err != nil {
			t.Fatal(err)
		}
		permission, found, err := models.PermissionObjects.Using(backend).OrderBy(models.PermissionFields.ID.Asc()).First(t.Context())
		if err != nil || !found {
			t.Fatal("missing view grant", err)
		}
		created, err := manager.CreateUserWithUnusablePassword(t.Context(), f.actor, identity.NewUserCreate("without-password", "without").WithStaff(true).WithPermissions(permission.ID))
		if err != nil || created.Revision != 1 {
			t.Fatal("unusable creation failed", err)
		}
		directory, err := identity.NewDirectory(f.runtime)
		if err != nil {
			t.Fatal(err)
		}
		account, found, err := directory.ByPrincipalID(t.Context(), created.PrincipalID)
		if err != nil || !found || account.HasUsablePassword() {
			t.Fatal("directory lost unusable state", err)
		}
		credential, err := f.runtime.Authenticator().Resolve(t.Context(), created.PrincipalID)
		assertUnusableReference(t, "created", map[string]any{"usable": credential.HasUsablePassword(), "active": credential.Principal().Active(), "staff": credential.Principal().Staff(), "superuser": credential.Principal().Superuser(), "resolved": err == nil, "has_view": credential.Principal().Has("helpdesk.ticket.view")})
		checks := []bool{}
		for _, password := range []string{"", "wrong", "!marker", "godj-unmatchable-dummy-password"} {
			_, err := f.runtime.Authenticator().Authenticate(t.Context(), "without", password)
			if err != nil && !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatal("deliberate unusable state became a storage error", err)
			}
			checks = append(checks, err == nil)
		}
		assertUnusableReference(t, "checks", checks)
		loginAt := time.Now().UTC().Truncate(time.Microsecond)
		h := newIdentityHTTPConfigured(t, f.runtime.Authenticator(), f.runtime.SessionStore(), f.runtime.LoginPersistence, nil, func() time.Time { return loginAt })
		address, err := url.Parse(h.server.URL)
		if err != nil {
			t.Fatal(err)
		}
		sessionManager, err := sessions.NewManager(f.runtime.SessionStore(), sessions.Config{})
		if err != nil {
			t.Fatal(err)
		}
		// Begin with a disabled target, then observe two repeated disablements.
		if _, err := manager.SetUnusablePassword(t.Context(), f.actor, f.user.ID, 1); err != nil {
			t.Fatal(err)
		}
		rotations := []map[string]any{}
		for revision := int64(2); revision <= 3; revision++ {
			before := f.stored(t)
			old, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
			if err != nil {
				t.Fatal(err)
			}
			// Model another trusted authentication mechanism. No password login
			// may create this session; current-stamp resolution may admit it.
			record, err := sessionManager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: f.user.PrincipalID, auth.SessionCredentialStampKey: old.SessionStamp()})
			if err != nil {
				t.Fatal(err)
			}
			client := h.newClient(t)
			client.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: record.ID().Encoded(), Path: "/"}})
			first, _ := h.request(t, client, "GET", "/view/", nil)
			profile, err := manager.SetUnusablePassword(t.Context(), f.actor, f.user.ID, revision)
			if err != nil || profile.Revision != revision+1 {
				t.Fatal("repeated disablement failed", err)
			}
			last, _ := h.request(t, client, "GET", "/view/", nil)
			current, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
			if err != nil {
				t.Fatal(err)
			}
			_, retained, err := f.runtime.SessionStore().Load(t.Context(), record.ID())
			if err != nil {
				t.Fatal(err)
			}
			rotations = append(rotations, map[string]any{"before": map[string]bool{"authenticated": first.StatusCode == 200, "has_view": first.StatusCode == 200}, "after": map[string]bool{"authenticated": last.StatusCode == 200, "has_view": last.StatusCode == 200}, "marker_changed": before.EncodedPassword != f.stored(t).EncodedPassword, "stamp_changed": !current.MatchesSessionStamp(old.SessionStamp()), "old_session_retained": retained, "usable": current.HasUsablePassword()})
		}
		assertUnusableReference(t, "rotations", rotations)
		if validatorCalls != 0 || f.hasher.calls.Load() != 0 {
			t.Fatal("unusable operation invoked raw password policy/hash")
		}
		if _, err := manager.SetPassword(t.Context(), f.actor, f.user.ID, 4, managementNewPassword); err != nil {
			t.Fatal(err)
		}
		if validatorCalls != 2 || f.hasher.calls.Load() != 1 {
			t.Fatal("restoration bypassed usable password policy")
		}
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		current, err := reopened.Authenticator().Authenticate(t.Context(), "member", managementNewPassword)
		if err != nil {
			t.Fatal("restored password did not survive runtime reopen", err)
		}
		h.login(t, h.client, "member", managementNewPassword, 200)
		response, _ := h.request(t, h.client, "GET", "/view/", nil)
		stored := f.stored(t)
		assertUnusableReference(t, "restored", map[string]any{"usable": current.HasUsablePassword(), "login": err == nil, "identity": map[string]bool{"authenticated": response.StatusCode == 200, "has_view": response.StatusCode == 200}, "active": stored.Active, "staff": stored.Staff, "email_preserved": stored.Email == f.user.Email})
		requireLoginTime(t, stored.LastLogin, loginAt)
		stored.LastLogin = f.user.LastLogin
		stored.EncodedPassword, stored.Revision = f.user.EncodedPassword, f.user.Revision
		if !reflect.DeepEqual(stored, f.user) {
			t.Fatal("password lifecycle changed unrelated profile")
		}
		for index, id := range f.ids {
			_, found, err := f.runtime.SessionStore().Load(t.Context(), id)
			if err != nil || found != (index%3 != 0) {
				t.Fatal("wrong original session removed", index, err)
			}
		}
		history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
		if err != nil || len(history) != 4 {
			t.Fatal("password event inventory changed", err)
		}
		for _, event := range history {
			if !reflect.DeepEqual(event.ChangedFields, []string{"password"}) || event.DisplayLabel != "" {
				t.Fatal("unusable audit exposes marker or loses meaning")
			}
		}
	})
	for _, mode := range []string{"update_failure", "second_delete_failure", "audit_failure", "callback_cancel", "unknown_rollback", "unknown_commit", "late_cancel", "read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure", "write_zero", "write_nil", "write_twice", "write_swallowed_failure"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 6)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			boundary := &managementBoundary{ManagementBackend: f.runtime, mode: mode, afterCommit: cancel}
			profile, err := f.manager(t, boundary).SetUnusablePassword(ctx, f.actor, f.user.ID, 1)
			if mode == "late_cancel" {
				if err != nil || profile.ID == 0 {
					t.Fatal("known commit lost", err)
				}
			} else if err == nil || profile.ID != 0 {
				t.Fatal("failed or unknown disablement published success")
			}
			if strings.HasPrefix(mode, "unknown_") && !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) {
				t.Fatal("unknown outcome downgraded", err)
			}
			if f.hasher.calls.Load() != 0 {
				t.Fatal("disablement hashed")
			}
			if mode == "unknown_commit" || mode == "late_cancel" {
				if f.stored(t).Revision != 2 || !strings.HasPrefix(f.stored(t).EncodedPassword, "!") {
					t.Fatal("committed unusable state absent")
				}
				for index, id := range f.ids {
					_, found, e := f.runtime.SessionStore().Load(t.Context(), id)
					if e != nil || found != (index%3 != 0) {
						t.Fatal("partial commit sessions", e)
					}
				}
				history, e := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
				if e != nil || len(history) != 1 {
					t.Fatal("partial commit audit", e)
				}
			} else {
				f.assertOutcome(t, false)
			}
		})
	}
	for _, mode := range []string{"actor_deactivated", "group_grant_removed", "target_revision", "target_hash_without_revision"} {
		t.Run("fence_"+mode, func(t *testing.T) {
			backend, writer := open(t)
			f := newManagementFixture(t, backend, 3)
			_, link := managementGroupActor(t, f)
			boundary := beforePasswordWrite{ManagementBackend: f.runtime, before: func(ctx context.Context) error {
				return writer.CoordinatedAtomic(ctx, func(session db.Session) error {
					switch mode {
					case "actor_deactivated":
						_, err := models.UserObjects.Patch(ctx, session, f.root, models.UserPatch{}.WithActive(false).WithRevision(3))
						return err
					case "group_grant_removed":
						_, err := models.GroupPermissionsLinkObjects.Delete(ctx, session, &link)
						return err
					case "target_revision":
						_, err := models.UserObjects.Patch(ctx, session, f.user, models.UserPatch{}.WithRevision(2))
						return err
					default:
						_, err := models.UserObjects.Patch(ctx, session, f.user, models.UserPatch{}.WithEncodedPassword("!independent"))
						return err
					}
				})
			}}
			profile, err := f.manager(t, boundary).SetUnusablePassword(t.Context(), f.actor, f.user.ID, 1)
			want := identity.CodePermission
			if strings.HasPrefix(mode, "target_") {
				want = identity.CodeConflict
			}
			if profile.ID != 0 || !errors.Is(err, &identity.Error{Code: want}) {
				t.Fatal("stale password disablement admitted", err)
			}
			history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
			if err != nil || len(history) != 0 {
				t.Fatal("refusal was audited", err)
			}
			for _, id := range f.ids {
				_, found, err := f.runtime.SessionStore().Load(t.Context(), id)
				if err != nil || !found {
					t.Fatal("refusal revoked sessions", err)
				}
			}
		})
	}
	t.Run("competing_disable_and_replace", func(t *testing.T) {
		backend, second := open(t)
		f := newManagementFixture(t, backend, 3)
		other, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		before := func(ctx context.Context) error {
			arrived <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		results := make(chan error, 2)
		for index, backend := range []identity.ManagementBackend{f.runtime, other} {
			manager := f.manager(t, beforePasswordWrite{ManagementBackend: backend, before: before})
			go func() {
				var err error
				if index == 0 {
					_, err = manager.SetUnusablePassword(ctx, f.actor, f.user.ID, 1)
				} else {
					_, err = manager.SetPassword(ctx, f.actor, f.user.ID, 1, managementNewPassword)
				}
				results <- err
			}()
		}
		for range 2 {
			select {
			case <-arrived:
			case <-ctx.Done():
				close(release)
				t.Fatal("preflight retained database scope")
			}
		}
		close(release)
		passed, conflicts := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				passed++
			} else if errors.Is(err, &identity.Error{Code: identity.CodeConflict}) {
				conflicts++
			} else {
				t.Fatal("unexpected competing result", err)
			}
		}
		if passed != 1 || conflicts != 1 || f.stored(t).Revision != 2 {
			t.Fatal("two credential commands committed")
		}
	})
}

type beforePasswordWrite struct {
	identity.ManagementBackend
	before func(context.Context) error
}

func (b beforePasswordWrite) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	if err := b.before(ctx); err != nil {
		return err
	}
	return b.ManagementBackend.CoordinatedAtomic(ctx, callback)
}

func RunUnusablePasswordHTTP(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, channel := range []string{"api", "admin"} {
		t.Run(channel, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			policy := identity.PasswordValidatorFunc(func(string, identity.Profile) validation.Errors {
				return validation.NewErrors(validation.New("password", "blocked_by_host"))
			})
			if channel == "api" {
				h, _ := newManagementHTTP(t, f, f.runtime, policy)
				path := apiPath("users", f.user.ID) + "password/"
				for _, payload := range []string{`{}`, `{"password":""}`, `{"password":false}`, `{"password":null,"extra":true}`} {
					managementCall(t, h, h.client, "POST", path, payload, "1", true, 400)
				}
				managementCall(t, h, h.client, "POST", path, `{"password":null}`, "1", false, 403)
				managementCall(t, h, h.client, "POST", path, `{"password":null}`, "", true, 428)
				managementCall(t, h, h.client, "POST", path, `{"password":null}`, "2", true, 412)
				f.assertOutcome(t, false)
				managementCall(t, h, h.client, "POST", path, `{"password":null}`, "1", true, 200)
				managementCall(t, h, h.client, "POST", path, `{"password":null}`, "2", true, 200)
				h.login(t, h.newClient(t), "member", managementOldPassword, 401)
				_, body := managementCall(t, h, h.client, "POST", identityapi.BasePath+"users/", `{"username":"without","password":null}`, "", true, 201)
				id := apiInteger(t, body, "id")
				created, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(id)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
				if err != nil || !found || !strings.HasPrefix(created.EncodedPassword, "!") || !created.Active {
					t.Fatal("explicit null creation failed", err)
				}
				managementCall(t, h, h.client, "POST", path, `{"password":"valid but rejected"}`, "3", true, 400)
			} else {
				h := newIdentityAdminHTTP(t, f, f.runtime, func(config identityadmin.Config) identityadmin.Config { return config.WithPasswordValidators(policy) })
				h.loginAdmin(t, h.client, "manager", managementOldPassword)
				path := adminObjectPath("users", "command/disable-password", f.user.ID)
				form := h.call(t, h.client, "GET", path, nil, 200)
				h.call(t, h.client, "POST", path, url.Values{"confirm": {"on"}, "expected_revision": {"1"}}, 403)
				h.postForm(t, path, url.Values{}, 200)
				h.call(t, h.client, "POST", path, url.Values{"confirm": {"on"}, "expected_revision": {"2"}, "csrfmiddlewaretoken": {form.token(t)}}, 409)
				f.assertOutcome(t, false)
				h.postForm(t, path, url.Values{"confirm": {"on"}}, 302)
				h.postForm(t, path, url.Values{"confirm": {"on"}}, 302)
			}
			if f.stored(t).Revision != 3 || !strings.HasPrefix(f.stored(t).EncodedPassword, "!") || f.hasher.calls.Load() != 0 {
				t.Fatal("HTTP disablement lost state or invoked password policy/hash")
			}
			if _, err := f.manager(t, f.runtime).SetPassword(t.Context(), f.actor, f.user.ID, 3, managementNewPassword); err != nil {
				t.Fatal(err)
			}
			if _, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", managementNewPassword); err != nil {
				t.Fatal("restore after HTTP disablement failed", err)
			}
			// Self-disable commits, then its own old session cannot administer.
			h, _ := newManagementHTTP(t, f, f.runtime)
			managementCall(t, h, h.client, "POST", apiPath("users", f.root.ID)+"password/", `{"password":null}`, strconv.FormatInt(f.root.Revision, 10), true, 200)
			managementCall(t, h, h.client, "GET", identityapi.BasePath+"users/", "", "", false, 403)
		})
	}
}
