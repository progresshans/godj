package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

func accountEditConfig() accountmodel.AccountEditConfig[models.User] {
	config := roleConfig()
	config.Fields = append(config.Fields, "username", "email", "first_name")
	return config
}

func combinedProfileAndRoles(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	actor := profileActor(t, f).Principal()
	target := roleTarget(t, backend, f, "combined-target")
	credential, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present {
		t.Fatal(err)
	}
	now := joined
	store, manager, _ := accountSessions(t, backend, &now)
	targetSession, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: target.PrincipalID})
	if err != nil {
		t.Fatal(err)
	}
	actorSession, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: actor.ID()})
	if err != nil {
		t.Fatal(err)
	}
	config := accountEditConfig()
	config.AdditionalPermissions = []auth.Permission{"account.team"}
	validated := 0
	config.Validate = func(ctx context.Context, reader db.Queryer, current, candidate models.User, memberships accountmodel.RoleMemberships) (validation.Errors, error) {
		validated++
		if current.ID != target.ID || candidate.ID != target.ID || candidate.Username != "edited" || candidate.Email != "Local@example.test" {
			return validation.Errors{}, errors.New("validator did not receive the complete normalized candidate")
		}
		if candidate.Staff && candidate.FirstName == "" {
			return validation.NewErrors(validation.New("first_name", "staff_requires_name")), nil
		}
		// The selection and model copies cannot mutate the actual write.
		if keys := memberships.Groups(); len(keys) > 0 {
			keys[0] = mathMaxKey
		}
		candidate.PrincipalID, candidate.EncodedPassword = "private copy", "private copy"
		return validation.Errors{}, nil
	}
	editor, err := project.NewAccountEditor(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	input := roleInput(t, map[string]query.Value{"username": query.String("ｅｄｉｔｅｄ"), "email": query.String("Local@EXAMPLE.TEST"), "first_name": query.String("Editor"), "staff": query.Boolean(true)}, map[string][]int64{"groups": {f.group.ID, f.group.ID}, "permissions": {f.grants[0].ID}})
	input, err = input.WithTargetKey(query.Integer(target.ID))
	if err != nil {
		t.Fatal(err)
	}
	changed, err := editor.Update(t.Context(), actor, target.ID, 1, input)
	if err != nil || changed.Revision != 2 || changed.RevokedSessions != 0 || validated != 1 {
		t.Fatal("combined edit did not commit once", changed, err)
	}
	wantFields := []string{"email", "first_name", "groups", "permissions", "staff", "username"}
	fields := slices.Clone(changed.ChangedFields)
	slices.Sort(fields)
	if !slices.Equal(fields, wantFields) {
		t.Fatal("combined audit fields incomplete", fields)
	}
	if raw, _ := input.Values()["username"].String(); raw != "ｅｄｉｔｅｄ" {
		t.Fatal("normalization mutated caller intent")
	}
	current := profileRow(t, reopen(t), target.ID)
	if current.Username != "edited" || current.Email != "Local@example.test" || current.FirstName != "Editor" || !current.Staff || current.Revision != 2 {
		t.Fatal("combined scalar state lost")
	}
	assertRoleKeys(t, roleReader(t, backend), actor, target.ID, []int64{f.group.ID}, []int64{f.grants[0].ID})
	fresh, present, err := f.store.CredentialByID(t.Context(), target.PrincipalID)
	if err != nil || !present || fresh.SessionStamp() != credential.SessionStamp() {
		t.Fatal("profile/role changed stable credential", err)
	}
	accountSessionRecord(t, store, targetSession, true)
	accountSessionRecord(t, store, actorSession, true)
	history := profileHistory(t, reopen(t), actor, target.ID)
	if len(history) != 1 || !slices.Equal(history[0].ChangedFields(), changed.ChangedFields) {
		t.Fatal("combined edit did not append one complete audit")
	}
	noop, err := editor.Update(t.Context(), actor, target.ID, 2, input)
	if err != nil || noop.Revision != 2 || len(noop.ChangedFields) != 0 || validated != 2 || len(profileHistory(t, backend, actor, target.ID)) != 1 {
		t.Fatal("combined no-op lost validation or wrote revision/audit", err)
	}
	for _, name := range []string{"cross_field_policy", "unique", "stale"} {
		t.Run(name, func(t *testing.T) {
			request := roleInput(t, map[string]query.Value{"first_name": query.String(""), "active": query.Boolean(false)}, map[string][]int64{"groups": {}})
			revision := int64(2)
			owner := editor
			if name == "unique" {
				// The model unique constraint and collection/deactivation share
				// the same owner even without an application callback.
				plain := accountEditConfig()
				owner, err = project.NewAccountEditor(store, auth.PrincipalAuthorizer{}, plain)
				if err != nil {
					t.Fatal(err)
				}
				request = roleInput(t, map[string]query.Value{"username": query.String(f.user.Username), "first_name": query.String("Duplicate"), "active": query.Boolean(false)}, map[string][]int64{"groups": {}, "permissions": {}})
			}
			if name == "stale" {
				revision = 1
			}
			result, err := owner.Update(t.Context(), actor, target.ID, revision, request)
			if result.Revision != 0 || err == nil {
				t.Fatal("invalid combined edit published metadata")
			}
			if name == "stale" {
				if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeConflict}) {
					t.Fatal(err)
				}
			} else {
				diagnostics, rejected := validation.Rejected(err)
				field := validation.Field("first_name")
				if name == "unique" {
					field = "username"
				}
				if !rejected || diagnostics.ByField(field).Empty() {
					t.Fatal("combined policy/unique failure lost its field", err)
				}
			}
			if !reflect.DeepEqual(profileRow(t, reopen(t), target.ID), current) || len(profileHistory(t, backend, actor, target.ID)) != 1 {
				t.Fatal("rejection left partial profile/role/revision/audit")
			}
			assertRoleKeys(t, roleReader(t, backend), actor, target.ID, []int64{f.group.ID}, []int64{f.grants[0].ID})
			accountSessionRecord(t, store, targetSession, true)
		})
	}
	changed, err = editor.Update(t.Context(), actor, target.ID, 2, roleInput(t, map[string]query.Value{"first_name": query.String("Suspended"), "active": query.Boolean(false)}, map[string][]int64{"groups": {}, "permissions": {}}))
	if err != nil || changed.Revision != 3 || changed.RevokedSessions != 1 {
		t.Fatal("combined deactivation", changed, err)
	}
	current = profileRow(t, reopen(t), target.ID)
	if current.Active || current.FirstName != "Suspended" || current.Revision != 3 || current.PrincipalID != target.PrincipalID || current.EncodedPassword != target.EncodedPassword || current.LastLogin != nil || !current.DateJoined.Equal(target.DateJoined) {
		t.Fatal("combined write lost lifecycle/protected fields")
	}
	assertRoleKeys(t, roleReader(t, backend), actor, target.ID, nil, nil)
	accountSessionRecord(t, store, targetSession, false)
	accountSessionRecord(t, store, actorSession, true)
	if entries := profileHistory(t, reopen(t), actor, target.ID); len(entries) != 2 || !slices.Equal(entries[1].ChangedFields(), changed.ChangedFields) {
		t.Fatal("combined deactivation did not have one final audit")
	}
}
