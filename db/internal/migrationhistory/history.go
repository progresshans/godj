// Package migrationhistory owns the database-independent canonical identity
// representation of revision-fenced history. Durable tokens and I/O belong to
// each backend.
package migrationhistory

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"strings"

	"github.com/progresshans/godj/migrations/backend"
)

func Clone(records []backend.AppliedMigration) []backend.AppliedMigration {
	return append([]backend.AppliedMigration{}, records...)
}

func Compare(left, right backend.AppliedMigration) int {
	if order := strings.Compare(left.App, right.App); order != 0 {
		return order
	}
	return strings.Compare(left.Name, right.Name)
}

func Sort(records []backend.AppliedMigration) { slices.SortFunc(records, Compare) }

func Equal(left, right []backend.AppliedMigration) bool { return slices.Equal(left, right) }

// Fingerprint preserves the v1 length-prefixed SHA256 representation. It
// never mutates caller order and treats nil and empty history identically.
func Fingerprint(records []backend.AppliedMigration) [sha256.Size]byte {
	canonical := Clone(records)
	Sort(canonical)
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, record := range canonical {
		for _, value := range []string{record.App, record.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}
