package identityreference

import (
	"embed"
	"errors"
)

//go:embed user-creation-*.json
var creationReferences embed.FS

func UserCreationInputs() ([]byte, error) {
	return creationReferences.ReadFile("user-creation-inputs.json")
}

// UserCreation returns the unchanged independent input and lifecycle capture.
func UserCreation(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown user creation reference backend")
	}
	return creationReferences.ReadFile("user-creation-django61-" + backend + ".json")
}
