package modeldef_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/identity/modeldef"
	"github.com/progresshans/godj/schema/ir"
)

func TestDefaultAccountRoleMatchesActualDeclaredModel(t *testing.T) {
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	role := modeldef.AccountRole()
	actual, err := ir.NormalizeAccountRole(role, definition)
	if err != nil || !reflect.DeepEqual(actual, role) {
		t.Fatalf("default account role = %#v, %v", actual, err)
	}
	// Default management normalizes stored usernames, while authentication
	// looks up the explicitly entered username. The role must not silently
	// merge those existing policies when it is shared with custom accounts.
	if actual.WriteLoginNormalization != ir.AccountUnicode16NFKC || actual.AuthenticateNormalization != ir.AccountExact {
		t.Fatal("built-in login policies changed")
	}
}
