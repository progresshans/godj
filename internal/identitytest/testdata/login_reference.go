package identityreference

import (
	"embed"
	"errors"
)

//go:embed login-lifecycle-django61-*.json
var loginReferences embed.FS

// LoginLifecycle returns the original independent observation, without
// depending on the historical fixed identity runtime.
func LoginLifecycle(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown login reference backend")
	}
	return loginReferences.ReadFile("login-lifecycle-django61-" + backend + ".json")
}
