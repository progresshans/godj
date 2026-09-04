//go:build darwin || linux

package projectcheck

import "github.com/progresshans/godj/internal/projectcheck/showmigrationsprotocol"

type showMigrationsArguments struct {
	explicitDescriptor string
}

func parseShowMigrationsArguments(argv []string) (showMigrationsArguments, *ShowMigrationsFailure) {
	switch {
	case len(argv) == 1 && argv[0] == "showmigrations":
		return showMigrationsArguments{}, nil
	case len(argv) == 3 && argv[0] == "showmigrations" && argv[1] == "--project" && argv[2] != "":
		return showMigrationsArguments{explicitDescriptor: argv[2]}, nil
	default:
		failure := ShowMigrationsFailure{
			Category: showmigrationsprotocol.CategoryCommand,
			Code:     showmigrationsprotocol.CodeInvalidArguments,
		}
		return showMigrationsArguments{}, &failure
	}
}
