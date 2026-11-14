package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func selfPasswordConfiguration(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	policies := 0
	config := accountmodel.PasswordChangeConfig[models.User]{MaxPasswordBytes: 64, ValidatePassword: func(_ context.Context, _ string, row orm.ModelView[models.User]) (validation.Errors, error) {
		policies++
		if passwordViewString(t, row, "first_name") != f.target.FirstName || passwordViewString(t, row, "encoded_password") != "" {
			return validation.Errors{}, errors.New("password configuration lost its selected private profile")
		}
		return validation.Errors{}, nil
	}}
	owner, err := f.runtime.SessionStore().PasswordChangePersistence(f.runtime.Sessions(), f.runtime.Authenticator(), config)
	if err != nil || owner.Sessions() != f.runtime.Sessions() {
		t.Fatal("selected password composition", err)
	}
	config.MaxPasswordBytes = 1
	config.ValidatePassword = func(context.Context, string, orm.ModelView[models.User]) (validation.Errors, error) {
		return validation.NewErrors(validation.New("password", "caller_mutation")), nil
	}
	previous := f.targetSessions[0]
	before := userReferenceSnapshot(t, reopen(t))
	f.owner.boundary.calls = 0
	old, next := password, selfPassword
	if err := owner.CheckPasswordChange(t.Context(), previous.ID(), &old, &next); err != nil {
		t.Fatal("caller configuration mutation changed an existing password owner", err)
	}
	if policies != 1 || f.owner.hasher.hashes.Load() != 0 || f.owner.boundary.calls != 0 || !reflect.DeepEqual(before, userReferenceSnapshot(t, reopen(t))) {
		t.Fatal("password advisory check published a mutation or skipped the owned policy")
	}
	entropy := f.entropy.count()
	result, err := owner.ChangePassword(t.Context(), previous.ID(), old, next)
	if err != nil || policies != 3 || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 2 || f.owner.boundary.calls != 1 {
		t.Fatal("password owner lost its copied policy or repeated preparation", err)
	}
	after := userReferenceSnapshot(t, reopen(t))
	expected, envelope := selfPasswordExpected(t, f, f.owner, before, after, previous, f.entropy.one(t, entropy), f.clock())
	assertSelfPasswordResult(t, result, envelope)
	assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
	for _, value := range []any{owner, result} {
		selfPasswordPrivate(t, value, old, next, f.target.EncodedPassword, f.owner.encoded.Load().(string), previous.ID().Encoded(), result.Record.ID().Encoded(), f.credential.SessionStamp(), result.Credential.SessionStamp())
	}
}

func selfPasswordCurrent(t *testing.T, open Open) {
	for _, mode := range []string{"profile", "policy", "grant", "password", "inactive", "deleted", "logout", "rotation", "absolute_expiry", "idle_expiry"} {
		t.Run(mode, func(t *testing.T) {
			backend, reopen := open(t)
			f := provisionSelfPassword(t, backend)
			if mode == "deleted" {
				// The competing delete has a valid CASCADE graph. Preserve the
				// unrelated PROTECT control and remove only this setup guard.
				for _, row := range userReferenceSnapshot(t, backend).guards {
					if row.OwnerID == f.target.ID {
						if count, err := hostmodels.GuardObjects.Delete(t.Context(), backend, &row); err != nil || count != 1 {
							t.Fatal("delete fence fixture", err)
						}
					}
				}
			}
			config := f.config(t)
			config.Accounts.ValidatePassword = func(_ context.Context, _ string, row orm.ModelView[models.User]) (validation.Errors, error) {
				if passwordViewString(t, row, "first_name") == "refuse-current" {
					return validation.NewErrors(validation.New("password", "password_too_similar")), nil
				}
				return validation.Errors{}, nil
			}
			f.owner = newUserReferenceOwner(t, backend, f.userReferenceFixture, config)
			f.runtime = f.owner.runtime
			config.Accounts.Now = f.clock
			writer := newUserReferenceOwner(t, reopen(t), f.userReferenceFixture, config)
			previous := f.targetSessions[0]
			f.instant.Store(nativeLoginInstant.Add(time.Minute).UnixNano())
			var checkpoint userReferenceState
			entropyAtCheckpoint := 0
			entered := false
			f.owner.hasher.beforeHash = func(ctx context.Context) error {
				entered = true
				before := userReferenceSnapshot(t, reopen(t))
				expected, row := before, f.target
				event := catalogExpectedEvent{"user", row.ID, admin.ActionChange, nil}
				hasEvent, revoke := false, false
				switch mode {
				case "profile", "policy", "inactive", "grant":
					values, collections := map[string]query.Value{}, map[string][]int64{}
					switch mode {
					case "profile":
						values["username"], values["first_name"] = query.String("renamed"), query.String("current name")
						row.Username, row.FirstName, event.fields = "renamed", "current name", []string{"username", "first_name"}
					case "policy":
						values["first_name"], row.FirstName, event.fields = query.String("refuse-current"), "refuse-current", []string{"first_name"}
					case "inactive":
						values["active"], row.Active, event.fields, revoke = query.Boolean(false), false, []string{"active"}, true
					case "grant":
						collections["permissions"], event.fields = []int64{}, []string{"permissions"}
					}
					result, err := writer.runtime.Accounts().Editor().Update(ctx, f.actor.Principal(), row.ID, row.Revision, roleInput(t, values, collections))
					wantRevoked := 0
					if revoke {
						wantRevoked = len(f.targetSessions)
					}
					if err != nil || result.ID != row.ID || result.Revision != row.Revision+1 || result.RevokedSessions != wantRevoked || !slices.Equal(result.ChangedFields, event.fields) {
						t.Fatal("competing selected profile edit lost its exact effect", err)
					}
					row.Revision++
					userReferenceReplace(&expected.account, row)
					if mode == "grant" {
						expected.account.direct = slices.DeleteFunc(slices.Clone(expected.account.direct), func(link models.UserPermissionsLink) bool { return link.SourceID == row.ID })
						domain, _ := f.credential.Principal().CredentialDomain()
						principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: row.PrincipalID, Active: row.Active, Staff: row.Staff, Superuser: row.Superuser})
						if err != nil {
							return err
						}
						f.credential, err = auth.NewCredentialForDomain(domain, row.Username, row.EncodedPassword, principal)
						if err != nil {
							return err
						}
					}
					hasEvent = true
				case "password":
					result, err := writer.runtime.Accounts().Passwords().SetPassword(ctx, f.actor.Principal(), row.ID, row.Revision, userReferencePassword)
					if err != nil || result.ID != row.ID || result.Revision != row.Revision+1 || result.RevokedSessions != len(f.targetSessions) || writer.hasher.hashes.Load() != 1 {
						t.Fatal("competing selected password change lost its exact effect", err)
					}
					row.Revision++
					row.EncodedPassword = writer.encoded.Load().(string)
					userReferenceReplace(&expected.account, row)
					event.fields, hasEvent, revoke = []string{"encoded_password"}, true, true
				case "deleted":
					result, err := writer.runtime.Accounts().Deleter().Delete(ctx, f.actor.Principal(), row.ID, row.Revision)
					if err != nil || result.ID != row.ID || result.Revision != row.Revision || result.DeletedRows != 3 || result.RevokedSessions != len(f.targetSessions) {
						t.Fatal("competing selected delete lost its complete graph", err)
					}
					expected.account.users = slices.DeleteFunc(slices.Clone(expected.account.users), func(item models.User) bool { return item.ID == row.ID })
					expected.account.direct = slices.DeleteFunc(slices.Clone(expected.account.direct), func(link models.UserPermissionsLink) bool { return link.SourceID == row.ID })
					expected.notes = slices.DeleteFunc(slices.Clone(expected.notes), func(note hostmodels.Note) bool { return note.OwnerID == row.ID })
					event.action, hasEvent, revoke = admin.ActionDelete, true, true
				case "logout":
					if err := writer.runtime.SessionStore().Delete(ctx, previous.ID()); err != nil {
						return err
					}
					loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), previous.ID())
				case "rotation":
					payload, err := previous.WithValue("concurrent-payload", "kept by rotation")
					if err != nil {
						return err
					}
					entropy := f.entropy.count()
					actual, err := writer.runtime.Sessions().Rotate(ctx, payload)
					if err != nil {
						return err
					}
					wanted := loginExpectedRecord(t, f.credential, payload, "rotate", f.entropy.one(t, entropy), f.clock())
					if !reflect.DeepEqual(actual.Snapshot(), wanted.Snapshot()) {
						t.Fatal("competing session rotation lost its independent payload")
					}
					loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), previous.ID())
					loginPutRecord(t, &expected.account, userReferenceSnapshot(t, reopen(t)).account, loginNamespace(t, f.credential), wanted, false)
				case "absolute_expiry":
					f.instant.Store(previous.AbsoluteExpiresAt().UnixNano())
				case "idle_expiry":
					f.instant.Store(previous.IdleExpiresAt().UnixNano())
				}
				if revoke {
					for _, record := range f.targetSessions {
						loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), record.ID())
					}
				}
				checkpoint = userReferenceSnapshot(t, reopen(t))
				entropyAtCheckpoint = f.entropy.count()
				if hasEvent {
					assertUserReferenceStateAt(t, expected, checkpoint, f.actor.Principal(), f.clock(), event)
				} else {
					assertUserReferenceState(t, expected, checkpoint, f.actor.Principal())
				}
				f.target = row
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			result, err := f.runtime.PasswordChangePersistence().ChangePassword(ctx, previous.ID(), password, selfPassword)
			if !entered || f.owner.hasher.hashes.Load() != 1 || f.owner.hasher.verifies.Load() != 1 {
				t.Fatal("self password fence did not follow exactly one outside-scope preparation", err)
			}
			after := userReferenceSnapshot(t, reopen(t))
			if mode == "profile" || mode == "grant" {
				if err != nil || f.owner.policies.Load() != 2 {
					t.Fatal("current profile or grant edit blocked self password change", err)
				}
				expected, envelope := selfPasswordExpected(t, f, f.owner, checkpoint, after, previous, f.entropy.one(t, entropyAtCheckpoint), f.clock())
				assertSelfPasswordResult(t, result, envelope)
				assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
				current, err := writer.runtime.Authenticator().Authenticate(t.Context(), f.target.Username, selfPassword)
				if err != nil || !current.MatchesSessionStamp(envelope.Credential.SessionStamp()) || mode == "grant" && current.Principal().Has("helpdesk.ticket.view") {
					t.Fatal("self password did not publish the current profile and grants", err)
				}
			} else {
				passwordNoResult(t, result, err)
				if mode == "policy" {
					if diagnostics, rejected := validation.Rejected(err); !rejected || diagnostics.ByField("password").Empty() || f.owner.policies.Load() != 2 {
						t.Fatal("current typed policy did not refuse the pending password", err)
					}
				} else if err != auth.ErrInvalidCredentials {
					t.Fatal("current session or credential change lost its refusal", mode, err)
				}
				if !reflect.DeepEqual(checkpoint, after) {
					t.Fatal("stale password attempt changed the competing operation's exact state")
				}
			}
		})
	}
}
