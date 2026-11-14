package accountstoretest

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	websession "github.com/progresshans/godj/web/sessionauth"
)

func runUnusableReference(t *testing.T, open Open) {
	t.Run("unusable_reference", func(t *testing.T) {
		backend, reopen := open(t)
		f := provisionUnusable(t, backend, 3)
		actor := f.actor.Principal()
		observed := map[string]any{}
		if !t.Run("create_and_resolve", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			writes := f.owner.boundary.calls
			creator := f.owner.runtime.Accounts().Creator()
			input := roleInput(t, map[string]query.Value{"username": query.String("without"), "staff": query.Boolean(true)}, map[string][]int64{"permissions": {f.permission.ID}})
			candidate, err := creator.PrepareUnusable(t.Context(), actor, input)
			if err != nil || f.owner.hasher.hashes.Load() != 0 || f.owner.policies.Load() != 0 || f.owner.boundary.calls != writes {
				t.Fatal("unusable preparation invoked raw password work or wrote", err)
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
			result, err := creator.Commit(t.Context(), actor, candidate)
			if err != nil || result.ID <= 0 || result.Revision != 1 || result.RevokedSessions != 0 || !reflect.DeepEqual(result.ChangedFields, userReferenceCreationFields) || f.owner.boundary.calls != writes+1 {
				t.Fatal("selected unusable creation did not commit once", err)
			}
			row := profileRow(t, backend, result.ID)
			assertUnusableEncoding(t, row.EncodedPassword)
			expected := userReferenceExpectedRow(result.ID, "without", "", row.EncodedPassword)
			expected.Staff = true
			userReferenceReplace(&before.account, expected)
			after := userReferenceSnapshot(t, reopen(t))
			userReferenceRelations(t, &before.account, after.account, result.ID, nil, []int64{f.permission.ID})
			assertUserReferenceState(t, before, after, actor, catalogExpectedEvent{"user", result.ID, admin.ActionAdd, userReferenceCreationFields})
			read, found, err := f.owner.runtime.Credentials().CredentialByID(t.Context(), expected.PrincipalID)
			if err != nil || !found || read.HasUsablePassword() {
				t.Fatal("selected credential reader lost unusable state", err)
			}
			credential, err := f.owner.runtime.Authenticator().Resolve(t.Context(), expected.PrincipalID)
			if err != nil {
				t.Fatal(err)
			}
			observed["created"] = map[string]any{"usable": credential.HasUsablePassword(), "active": credential.Principal().Active(), "staff": credential.Principal().Staff(), "superuser": credential.Principal().Superuser(), "resolved": err == nil, "has_view": credential.Principal().Has("helpdesk.ticket.view")}
			assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), actor)
		}) {
			return
		}
		if !t.Run("authentication_rejections", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			checks := []bool{}
			for _, raw := range []string{"", "wrong", "!marker", "godj-unmatchable-dummy-password"} {
				_, err := f.owner.runtime.Authenticator().Authenticate(t.Context(), "without", raw)
				if !errors.Is(err, auth.ErrInvalidCredentials) {
					t.Fatal("unusable password was accepted or became a store failure", err)
				}
				checks = append(checks, err == nil)
			}
			observed["checks"] = checks
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
		}) {
			return
		}
		if !t.Run("initial_disable", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			writes := f.owner.boundary.calls
			result, err := f.owner.runtime.Accounts().Passwords().SetUnusablePassword(t.Context(), actor, f.target.ID, 1)
			if err != nil || result.ID != f.target.ID || result.Revision != 2 || result.RevokedSessions != 3 || f.owner.boundary.calls != writes+1 {
				t.Fatal("initial disablement lost its atomic session revocation", err)
			}
			f.target = f.changed(t, before, f.owner, true)
			f.keys = map[int64]bool{}
			f.records = nil
		}) {
			return
		}
		var rotations []map[string]any
		for _, mode := range []string{"repeat_disable_first", "repeat_disable_second"} {
			if !t.Run(mode, func(t *testing.T) {
				old, err := f.owner.runtime.Authenticator().Resolve(t.Context(), f.target.PrincipalID)
				if err != nil || old.HasUsablePassword() {
					t.Fatal("repeat disablement did not start from a resolvable unusable credential", err)
				}
				// A trusted non-password mechanism may issue this session.
				// Password authentication above never produced it.
				record := f.addTargetSession(t, old, mode)
				browser := newUserReferenceHTTP(t, f.owner.runtime)
				defer browser.close()
				address, err := url.Parse(browser.server.URL)
				if err != nil {
					t.Fatal(err)
				}
				browser.client.Jar.SetCookies(address, []*http.Cookie{{Name: websession.DefaultSessionCookieName, Value: record.ID().Encoded(), Path: "/"}})
				beforeStatus, _ := browser.exchange(t, "GET", "/view/", nil)
				if beforeStatus != 200 {
					t.Fatal("trusted non-password session did not resolve current permission", beforeStatus)
				}
				before := userReferenceSnapshot(t, reopen(t))
				previous, writes := f.target, f.owner.boundary.calls
				result, err := f.owner.runtime.Accounts().Passwords().SetUnusablePassword(t.Context(), actor, f.target.ID, f.target.Revision)
				if err != nil || result.ID != f.target.ID || result.Revision != f.target.Revision+1 || result.RevokedSessions != 1 || f.owner.boundary.calls != writes+1 {
					t.Fatal("repeat disablement failed to rotate once", err)
				}
				f.target = f.changed(t, before, f.owner, true)
				after := userReferenceSnapshot(t, reopen(t))
				afterStatus, _ := browser.exchange(t, "GET", "/view/", nil)
				if afterStatus != 403 {
					t.Fatal("rotated unusable credential retained its browser", afterStatus)
				}
				current, err := f.owner.runtime.Authenticator().Resolve(t.Context(), f.target.PrincipalID)
				if err != nil {
					t.Fatal(err)
				}
				_, retained, err := f.owner.runtime.SessionStore().Load(t.Context(), record.ID())
				if err != nil {
					t.Fatal(err)
				}
				rotations = append(rotations, map[string]any{"before": map[string]bool{"authenticated": beforeStatus == 200, "has_view": beforeStatus == 200}, "after": map[string]bool{"authenticated": afterStatus == 200, "has_view": afterStatus == 200}, "marker_changed": previous.EncodedPassword != f.target.EncodedPassword, "stamp_changed": !current.MatchesSessionStamp(old.SessionStamp()), "old_session_retained": retained, "usable": current.HasUsablePassword()})
				assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), actor)
				f.keys = map[int64]bool{}
				f.records = nil
			}) {
				return
			}
		}
		observed["rotations"] = rotations
		if f.owner.hasher.hashes.Load() != 0 || f.owner.policies.Load() != 0 {
			t.Fatal("unusable creation or rotation invoked raw password policy/hash")
		}
		if !t.Run("restore_and_reopen", func(t *testing.T) {
			before := userReferenceSnapshot(t, reopen(t))
			writes := f.owner.boundary.calls
			result, err := f.owner.runtime.Accounts().Passwords().SetPassword(t.Context(), actor, f.target.ID, f.target.Revision, userReferencePassword)
			if err != nil || result.ID != f.target.ID || result.Revision != 5 || result.RevokedSessions != 0 || f.owner.boundary.calls != writes+1 || f.owner.hasher.hashes.Load() != 1 || f.owner.policies.Load() != 2 {
				t.Fatal("restoration omitted policy phases or repeated hash/write", err)
			}
			f.target = f.changed(t, before, f.owner, false)
			current := userReferenceRuntime(t, reopen(t), f.base.hasher)
			credential, err := current.Authenticator().Authenticate(t.Context(), "member", userReferencePassword)
			if err != nil {
				t.Fatal("restored selected password did not survive reopening", err)
			}
			browser := newUserReferenceHTTP(t, current)
			defer browser.close()
			before = userReferenceSnapshot(t, reopen(t))
			id := browser.login(t, "member", userReferencePassword)
			status, _ := browser.exchange(t, "GET", "/view/", nil)
			if status != 200 {
				t.Fatal("restored browser did not recover the target permission", status)
			}
			expected := f.target
			at := joined
			expected.LastLogin = &at
			userReferenceReplace(&before.account, expected)
			userReferenceSession(t, current, credential, id, before, userReferenceSnapshot(t, reopen(t)), actor)
			f.target = profileRow(t, backend, f.target.ID)
			observed["restored"] = map[string]any{"usable": credential.HasUsablePassword(), "login": id.Valid(), "identity": map[string]bool{"authenticated": status == 200, "has_view": status == 200}, "active": f.target.Active, "staff": f.target.Staff, "email_preserved": f.target.Email == "member@example.test"}
			if len(profileHistory(t, reopen(t), actor, f.target.ID)) != 4 {
				t.Fatal("unusable lifecycle lost one of its four password audits")
			}
		}) {
			return
		}
		t.Run("django_reference", func(t *testing.T) { assertUnusableLifecycleReference(t, observed) })
	})
}
