//go:build darwin || linux

package projectcheck

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
)

type createsuperuserArguments struct {
	explicitDescriptor string
}

func parseCreatesuperuserArguments(argv []string) (createsuperuserArguments, *CreatesuperuserFailure) {
	arguments := createsuperuserArguments{}
	switch {
	case len(argv) == 1 && argv[0] == "createsuperuser":
		return arguments, nil
	case len(argv) == 3 && argv[0] == "createsuperuser" && argv[1] == "--project" && validCreatesuperuserDescriptorToken(argv[2]):
		arguments.explicitDescriptor = argv[2]
		return arguments, nil
	default:
		failure := CreatesuperuserFailure{
			Category: createsuperuserprotocol.CategoryCommand,
			Code:     createsuperuserprotocol.CodeInvalidArguments,
		}
		return createsuperuserArguments{}, &failure
	}
}

func validCreatesuperuserDescriptorToken(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.HasPrefix(value, "-") || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
