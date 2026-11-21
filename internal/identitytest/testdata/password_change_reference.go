package identityreference

import (
	"embed"
	"errors"
)

//go:embed password-change-django61-*.json
var passwordChangeReferences embed.FS

// PasswordChange returns the unchanged independent Django observation to
// consumers of either the selected account model or the historical runtime.
func PasswordChange(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown password change reference backend")
	}
	return passwordChangeReferences.ReadFile("password-change-django61-" + backend + ".json")
}
