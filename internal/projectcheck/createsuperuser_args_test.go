//go:build darwin || linux

package projectcheck

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
)

func TestParseCreatesuperuserArgumentsAcceptsOnlyExactPublicForms(t *testing.T) {
	descriptor := "./nested/godj.toml"
	tests := []struct {
		argv       []string
		descriptor string
	}{
		{argv: []string{"createsuperuser"}},
		{argv: []string{"createsuperuser", "--project", descriptor}, descriptor: descriptor},
	}
	for _, test := range tests {
		arguments, failure := parseCreatesuperuserArguments(test.argv)
		if failure != nil || arguments.explicitDescriptor != test.descriptor {
			t.Fatalf("parseCreatesuperuserArguments(%q) = %+v, %+v", test.argv, arguments, failure)
		}
	}
}

func TestParseCreatesuperuserArgumentsRejectsAllOtherShapes(t *testing.T) {
	invalid := [][]string{
		nil,
		{},
		{"createsuperuser", "extra"},
		{"createsuperuser", "--project"},
		{"createsuperuser", "--project", ""},
		{"createsuperuser", "--project", "-descriptor"},
		{"createsuperuser", "--project", " godj.toml"},
		{"createsuperuser", "--project", "godj.toml\x00"},
		{"createsuperuser", "godj.toml", "--project"},
		{"createsuperuser", "--username", "operator"},
		{"createsuperuser", "--password", "secret"},
		{"createsuperuser", "--password-file", "secret.txt"},
		{"createsuperuser", "--noinput"},
		{"createsuperuser", "--project=godj.toml"},
		{"createsuperuser", "--project", "godj.toml", "--noinput"},
	}
	want := CreatesuperuserFailure{
		Category: createsuperuserprotocol.CategoryCommand,
		Code:     createsuperuserprotocol.CodeInvalidArguments,
	}
	for _, argv := range invalid {
		arguments, failure := parseCreatesuperuserArguments(argv)
		if arguments != (createsuperuserArguments{}) || failure == nil || !reflect.DeepEqual(*failure, want) {
			t.Errorf("parseCreatesuperuserArguments(%q) = %+v, %+v; want zero, %+v", argv, arguments, failure, want)
		}
	}
}
