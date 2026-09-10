//go:build darwin || linux

package projectcheck

import (
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
)

type migrateArguments struct {
	explicitDescriptor string
	request            migrateprotocol.Request
	requestDocument    []byte
}

func parseMigrateArguments(argv []string) (migrateArguments, *MigrateFailure) {
	arguments := migrateArguments{
		request: migrateprotocol.Request{
			Mode:   migrateprotocol.ModeExecute,
			Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest},
		},
	}
	valid := false
	switch {
	case len(argv) == 1 && argv[0] == "migrate":
		valid = true
	case len(argv) == 2 && argv[0] == "migrate" && argv[1] == "--plan":
		arguments.request.Mode = migrateprotocol.ModePlan
		valid = true
	case len(argv) == 3 && argv[0] == "migrate" && argv[1] == "--project" && validMigrateDescriptorToken(argv[2]):
		arguments.explicitDescriptor = argv[2]
		valid = true
	case len(argv) == 4 && argv[0] == "migrate" && argv[1] == "--plan" && argv[2] == "--project" && validMigrateDescriptorToken(argv[3]):
		arguments.request.Mode = migrateprotocol.ModePlan
		arguments.explicitDescriptor = argv[3]
		valid = true
	case len(argv) == 3 && argv[0] == "migrate" && validMigrateTargetToken(argv[1]) && validMigrateTargetToken(argv[2]):
		arguments.request.Target = migrateWireTarget(argv[1], argv[2])
		valid = true
	case len(argv) == 4 && argv[0] == "migrate" && validMigrateTargetToken(argv[1]) && validMigrateTargetToken(argv[2]) && argv[3] == "--plan":
		arguments.request.Mode = migrateprotocol.ModePlan
		arguments.request.Target = migrateWireTarget(argv[1], argv[2])
		valid = true
	case len(argv) == 5 && argv[0] == "migrate" && validMigrateTargetToken(argv[1]) && validMigrateTargetToken(argv[2]) && argv[3] == "--project" && validMigrateDescriptorToken(argv[4]):
		arguments.request.Target = migrateWireTarget(argv[1], argv[2])
		arguments.explicitDescriptor = argv[4]
		valid = true
	case len(argv) == 6 && argv[0] == "migrate" && validMigrateTargetToken(argv[1]) && validMigrateTargetToken(argv[2]) && argv[3] == "--plan" && argv[4] == "--project" && validMigrateDescriptorToken(argv[5]):
		arguments.request.Mode = migrateprotocol.ModePlan
		arguments.request.Target = migrateWireTarget(argv[1], argv[2])
		arguments.explicitDescriptor = argv[5]
		valid = true
	}
	if !valid {
		return invalidMigrateArguments()
	}
	document, err := migrateprotocol.EncodeRequest(arguments.request)
	if err != nil {
		return invalidMigrateArguments()
	}
	arguments.requestDocument = document
	return arguments, nil
}

func migrateWireTarget(app, name string) migrateprotocol.Target {
	if name == "zero" {
		return migrateprotocol.Target{Kind: migrateprotocol.TargetZero, App: app}
	}
	return migrateprotocol.Target{Kind: migrateprotocol.TargetNamed, App: app, Name: name}
}

func validMigrateTargetToken(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.HasPrefix(value, "-")
}

func validMigrateDescriptorToken(value string) bool {
	return value != "" && utf8.ValidString(value) && !strings.HasPrefix(value, "-")
}

func invalidMigrateArguments() (migrateArguments, *MigrateFailure) {
	failure := MigrateFailure{Category: migrateprotocol.CategoryCommand, Code: migrateprotocol.CodeInvalidArguments}
	return migrateArguments{}, &failure
}
