package identityreference

import (
	"embed"
	"errors"
)

// Keep the native captures byte-for-byte; the independent Python producer and
// its mutation controls retain their existing ownership.
//
//go:embed catalog-django61-*.json
var catalogReferences embed.FS

func Catalog(backend string) ([]byte, error) {
	if backend != "sqlite" && backend != "postgres" {
		return nil, errors.New("unknown catalog reference backend")
	}
	return catalogReferences.ReadFile("catalog-django61-" + backend + ".json")
}
