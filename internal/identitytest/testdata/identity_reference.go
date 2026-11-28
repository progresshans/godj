package identityreference

import (
	"embed"
	"errors"
)

// The same untouched captures serve historical and selected-account consumers.
// Live Python validation owns their independent producer and mutation controls.
//
//go:embed identity-django61-*.json
var identityReferences embed.FS

func Identity(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown identity reference backend")
	}
	return identityReferences.ReadFile("identity-django61-" + backend + ".json")
}
