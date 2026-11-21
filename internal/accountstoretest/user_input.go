package accountstoretest

import (
	"testing"

	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func userInputRefusals(t *testing.T, open Open) {
	backend, reopen := open(t)
	f := provisionSelfPassword(t, backend)
	for _, test := range []struct {
		name, field, text string
		keys              []int64
	}{
		{"username_space", "username", " bad", nil},
		{"username_nul", "username", "bad\x00name", nil},
		{"email_nul", "email", "bad\x00email", nil},
		{"email_utf8", "email", "name@\xff.example", nil},
		{"profile_utf8", "first_name", "\xff", nil},
		{"missing_negative_group", "groups", "", []int64{-999}},
		{"group_limit", "groups", "", make([]int64, accountmodel.MaximumAccountGroups+1)},
		{"permission_limit", "permissions", "", make([]int64, auth.MaximumPermissions+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, collections := map[string]query.Value{}, map[string][]int64{}
			if test.keys == nil {
				values[test.field] = query.String(test.text)
			} else {
				collections[test.field] = test.keys
			}
			before := userReferenceSnapshot(t, reopen(t))
			entropy := f.entropy.count()
			result, err := f.runtime.Accounts().Editor().Update(t.Context(), f.actor.Principal(), f.target.ID, f.target.Revision, roleInput(t, values, collections))
			if result.ID != 0 || result.Revision != 0 || result.RevokedSessions != 0 || len(result.ChangedFields) != 0 {
				t.Fatal("invalid selected user input published a mutation")
			}
			if failures, rejected := validation.Rejected(err); !rejected || failures.ByField(validation.Field(test.field)).Empty() {
				t.Fatal("invalid selected user input lost field diagnostics", test.name, err)
			}
			if f.owner.hasher.hashes.Load() != 0 || f.owner.hasher.verifies.Load() != 0 || f.entropy.count() != entropy {
				t.Fatal("invalid profile input performed credential or bearer work")
			}
			assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
		})
	}
}
