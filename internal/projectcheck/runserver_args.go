//go:build darwin || linux

package projectcheck

import (
	"strconv"
	"strings"
)

const defaultRunserverAddress = "127.0.0.1:8000"

type runserverArguments struct {
	explicitDescriptor string
	address            string
}

func parseRunserverArguments(argv []string) (runserverArguments, bool) {
	arguments := runserverArguments{address: defaultRunserverAddress}
	switch {
	case len(argv) == 1 && argv[0] == "runserver":
		return arguments, true
	case len(argv) == 3 && argv[0] == "runserver" && argv[1] == "--addr" && validRunserverAddress(argv[2]):
		arguments.address = argv[2]
		return arguments, true
	case len(argv) == 3 && argv[0] == "runserver" && argv[1] == "--project" && argv[2] != "":
		arguments.explicitDescriptor = argv[2]
		return arguments, true
	case len(argv) == 5 && argv[0] == "runserver" && argv[1] == "--project" && argv[2] != "" && argv[3] == "--addr" && validRunserverAddress(argv[4]):
		arguments.explicitDescriptor = argv[2]
		arguments.address = argv[4]
		return arguments, true
	default:
		return runserverArguments{}, false
	}
}

func validRunserverAddress(address string) bool {
	const prefix = "127.0.0.1:"
	if !strings.HasPrefix(address, prefix) {
		return false
	}
	portText := address[len(prefix):]
	if !canonicalDecimal(portText) {
		return false
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	return err == nil && port <= 65_535
}
