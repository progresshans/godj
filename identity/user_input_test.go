package identity

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
)

func TestUserInputOwnsMembershipSnapshotsAndNormalizesNames(t *testing.T) {
	keys := []int64{2, 1, 2}
	input := UserPatch{}.WithUsername("Ｆｒｅｄ").WithEmail("Mixed@EXAMPLE.COM").WithGroups(keys...).WithPermissions(4, 3, 4)
	keys[0] = 999
	normalized, err := input.normalize()
	if err != nil {
		t.Fatal(err)
	}
	name, _ := normalized.username.Get()
	email, _ := normalized.email.Get()
	groups, _ := normalized.groups.Get()
	permissions, _ := normalized.permissions.Get()
	if name != "Fred" || email != "Mixed@example.com" || !reflect.DeepEqual(groups, []int64{1, 2}) || !reflect.DeepEqual(permissions, []int64{3, 4}) {
		t.Fatal("input normalization or ownership")
	}
	old, _ := input.groups.Get()
	if !reflect.DeepEqual(old, []int64{2, 1, 2}) {
		t.Fatal("normalization mutated source snapshot")
	}
	cleared := input.WithGroups()
	if values, present := cleared.groups.Get(); !present || len(values) != 0 {
		t.Fatal("empty set lost presence")
	}
	if (UserPatch{}).groups.IsSet() {
		t.Fatal("omitted set marked present")
	}
}

func TestUserInputRejectsInvalidAndExcessiveData(t *testing.T) {
	for _, input := range []UserPatch{UserPatch{}.WithUsername(" bad"), UserPatch{}.WithUsername("bad\x00name"), UserPatch{}.WithEmail("bad\x00email"), UserPatch{}.WithEmail("name@\xff.example"), UserPatch{}.WithFirstName(string([]byte{0xff})), UserPatch{}.WithGroups(-1), UserPatch{}.WithPermissions(make([]int64, auth.MaximumPermissions+1)...), UserPatch{}.WithGroups(make([]int64, MaximumUserGroups+1)...)} {
		if _, err := input.normalize(); err == nil {
			t.Fatal("invalid input accepted")
		} else if fields, rejected := validation.Rejected(err); !rejected || fields.Empty() {
			t.Fatal("invalid input lost diagnostics", err)
		}
	}
}
