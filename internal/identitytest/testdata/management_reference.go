package identityreference

import (
	"embed"
	"errors"
)

// Keep the original native captures and the independent producer unchanged.
//
//go:embed management-django61-*.json
var managementReferences embed.FS

func Management(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown user management reference backend")
	}
	return managementReferences.ReadFile("management-django61-" + backend + ".json")
}
