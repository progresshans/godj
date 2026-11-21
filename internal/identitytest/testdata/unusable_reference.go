package identityreference

import (
	"embed"
	"errors"
)

//go:embed unusable-password-django61-*.json
var unusableReferences embed.FS

// UnusablePassword returns the original pinned Django observation unchanged.
// Generated consumers copy these bytes into their independent modules; they
// do not import the legacy identity runtime or manufacture expected results.
func UnusablePassword(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown unusable password reference backend")
	}
	return unusableReferences.ReadFile("unusable-password-django61-" + backend + ".json")
}
