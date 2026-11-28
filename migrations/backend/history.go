package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// AppliedMigration carries a durable identity. A revision-fenced session also
// returns the definition history digest when one was recorded at apply time.
type AppliedMigration struct {
	App           string
	Name          string
	HistoryDigest string `json:",omitempty"`
}

// ParseDefinitionHistoryDigest decodes the loader's canonical, lowercase
// SHA-256 spelling. Callers handle absent bindings separately.
func ParseDefinitionHistoryDigest(digest string) ([]byte, error) {
	const prefix = "sha256:"
	if len(digest) == len(prefix)+sha256.Size*2 && strings.HasPrefix(digest, prefix) {
		raw, err := hex.DecodeString(digest[len(prefix):])
		if err == nil && hex.EncodeToString(raw) == digest[len(prefix):] {
			return raw, nil
		}
	}
	return nil, errors.New("invalid migration definition history digest")
}

// AppliedMigrationReader reads a snapshot of durable migration identities.
// It is intentionally separate from Recorder, Transaction, and AtomicBackend
// so read-only history does not widen the migration write boundary.
type AppliedMigrationReader interface {
	ReadAppliedMigrations(context.Context) ([]AppliedMigration, error)
}
