package identityreference

import (
	"embed"
	"errors"
)

//go:embed password-reset-http-django61-*.json
var resetHTTPReferences embed.FS

// ResetHTTP returns the unchanged native view/session observation shared by
// the selected account and remaining historical HTTP consumers.
func ResetHTTP(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown password reset HTTP reference backend")
	}
	return resetHTTPReferences.ReadFile("password-reset-http-django61-" + backend + ".json")
}
