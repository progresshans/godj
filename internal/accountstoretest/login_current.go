package accountstoretest

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
)

func runLoginCurrentFences(t *testing.T, open Open) {
	t.Run("login_current_fences", func(t *testing.T) {
		for _, change := range []string{"password", "inactive", "nonstaff", "grant", "deleted", "profile"} {
			t.Run(change, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionLogin(t, backend, true)
				if err := loginAdmission(t.Context(), f.credential.Principal()); err != nil {
					t.Fatal("current fence lacks its admitted control", err)
				}
				previous := loginPrevious(t, f, "rotate")
				writer := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, f.config(t))
				before := userReferenceSnapshot(t, reopen(t))
				expected, row := before, f.target
				event := catalogExpectedEvent{"user", row.ID, admin.ActionChange, nil}
				revoke := change == "password" || change == "inactive" || change == "deleted"
				if change == "password" {
					result, err := writer.runtime.Accounts().Passwords().SetPassword(t.Context(), f.actor.Principal(), row.ID, row.Revision, userReferencePassword)
					if err != nil || result.ID != row.ID || result.Revision != 2 || result.RevokedSessions != len(f.targetSessions) || writer.hasher.hashes.Load() != 1 || writer.policies.Load() != 2 {
						t.Fatal("password fence preparation did not commit its exact change", err)
					}
					row.EncodedPassword, row.Revision = writer.encoded.Load().(string), 2
					event.fields = []string{"encoded_password"}
					userReferenceReplace(&expected.account, row)
				} else if change == "deleted" {
					result, err := writer.runtime.Accounts().Deleter().Delete(t.Context(), f.actor.Principal(), row.ID, row.Revision)
					if err != nil || result.ID != row.ID || result.Revision != 1 || result.DeletedRows != 2 || result.RevokedSessions != len(f.targetSessions) {
						t.Fatal("deleted fence preparation lost its graph or session effect", err)
					}
					expected.account.users = slices.DeleteFunc(slices.Clone(expected.account.users), func(item models.User) bool { return item.ID == row.ID })
					expected.account.direct = slices.DeleteFunc(slices.Clone(expected.account.direct), func(link models.UserPermissionsLink) bool { return link.SourceID == row.ID })
					event.action = admin.ActionDelete
				} else {
					values, collections := map[string]query.Value{}, map[string][]int64{}
					switch change {
					case "inactive":
						values["active"], row.Active, event.fields = query.Boolean(false), false, []string{"active"}
					case "nonstaff":
						values["staff"], row.Staff, event.fields = query.Boolean(false), false, []string{"staff"}
					case "grant":
						collections["permissions"], event.fields = []int64{}, []string{"permissions"}
					case "profile":
						values["first_name"], row.FirstName, event.fields = query.String("Concurrent edit"), "Concurrent edit", []string{"first_name"}
					}
					result, err := writer.runtime.Accounts().Editor().Update(t.Context(), f.actor.Principal(), row.ID, row.Revision, roleInput(t, values, collections))
					wantRevoked := 0
					if revoke {
						wantRevoked = len(f.targetSessions)
					}
					if err != nil || result.ID != row.ID || result.Revision != 2 || result.RevokedSessions != wantRevoked || !slices.Equal(result.ChangedFields, event.fields) {
						t.Fatal("current fence preparation lost its exact typed edit", err)
					}
					row.Revision = 2
					userReferenceReplace(&expected.account, row)
					if change == "grant" {
						expected.account.direct = slices.DeleteFunc(slices.Clone(expected.account.direct), func(link models.UserPermissionsLink) bool { return link.SourceID == row.ID })
					}
				}
				if revoke {
					for _, record := range f.targetSessions {
						loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), record.ID())
					}
				}
				assertUserReferenceState(t, expected, userReferenceSnapshot(t, reopen(t)), f.actor.Principal(), event)
				f.instant.Store(nativeLoginInstant.Add(time.Second).UnixNano())
				before = userReferenceSnapshot(t, reopen(t))
				entropy, writes := f.entropy.count(), f.owner.boundary.calls
				result, err := f.runtime.LoginPersistence().Login(t.Context(), auth.SessionLogin{Credential: f.credential, Previous: previous, At: f.clock(), Admit: loginAdmission})
				candidate := loginExpectedRecord(t, f.credential, previous, "rotate", f.entropy.one(t, entropy), f.clock())
				if f.owner.boundary.calls != writes+1 || f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.owner.policies.Load() != 0 {
					t.Fatal("current login fence replayed a command or password work")
				}
				after := userReferenceSnapshot(t, reopen(t))
				if change == "profile" {
					if err != nil || !reflect.DeepEqual(result.Record.Snapshot(), candidate.Snapshot()) || !result.Credential.MatchesSessionStamp(f.credential.SessionStamp()) {
						t.Fatal("unrelated profile edit blocked current login", err)
					}
					assertUserReferenceState(t, loginExpectedState(t, before, after, f.credential, row, previous, candidate, f.clock()), after, f.actor.Principal())
				} else {
					loginNoResult(t, result, err)
					if err != auth.ErrInvalidCredentials {
						t.Fatal("current admission refusal changed classification", err)
					}
					assertUserReferenceState(t, before, after, f.actor.Principal())
				}
				accountSessionRecord(t, f.runtime.SessionStore(), candidate, change == "profile")
				accountSessionRecord(t, f.runtime.SessionStore(), previous, change != "profile")
				for _, record := range f.targetSessions {
					accountSessionRecord(t, f.runtime.SessionStore(), record, !revoke)
				}
			})
		}
	})
}
