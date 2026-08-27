//go:build darwin || linux

package projectcheck

import "github.com/progresshans/godj/internal/projectcheck/migrateprotocol"

type migrateArguments struct {
	explicitDescriptor string
}

func parseMigrateArguments(argv []string) (migrateArguments, *MigrateFailure) {
	switch {
	case len(argv) == 1 && argv[0] == "migrate":
		return migrateArguments{}, nil
	case len(argv) == 3 && argv[0] == "migrate" && argv[1] == "--project" && argv[2] != "":
		return migrateArguments{explicitDescriptor: argv[2]}, nil
	default:
		failure := MigrateFailure{Category: migrateprotocol.CategoryCommand, Code: migrateprotocol.CodeInvalidArguments}
		return migrateArguments{}, &failure
	}
}
