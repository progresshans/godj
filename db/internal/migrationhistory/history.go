// Package migrationhistory owns the database-independent canonical identity
// representation of revision-fenced history. Durable tokens and I/O belong to
// each backend.
package migrationhistory

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"
	"strings"

	"github.com/progresshans/godj/migrations/backend"
)

const BoundRecorderFormat = 2

func RecorderFormat(bound bool) int {
	if bound {
		return BoundRecorderFormat
	}
	return 1
}

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

// Fingerprint retains the v1 bytes for entirely unbound history. When any
// binding exists, the distinct domain and per-row length prefixes bind each
// digest (including absence) to its exact recorder identity.
func Fingerprint(records []backend.AppliedMigration) [sha256.Size]byte {
	canonical := Clone(records)
	Sort(canonical)
	hash := sha256.New()
	bound := slices.ContainsFunc(canonical, func(record backend.AppliedMigration) bool { return record.HistoryDigest != "" })
	if bound {
		hash.Write([]byte("godj:bound-migration-recorder:v2\x00"))
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	writeString := func(value string) {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hash.Write(length[:])
		hash.Write([]byte(value))
	}
	for _, record := range canonical {
		writeString(record.App)
		writeString(record.Name)
		if bound {
			writeString(record.HistoryDigest)
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

// StoredDigest scans a nullable, size-bounded native binary column. Empty
// blobs and malformed values are distinct from a legacy SQL NULL.
type StoredDigest string

var ErrInvalidStoredDigest = errors.New("invalid stored migration definition digest")

func (digest *StoredDigest) Scan(value any) error {
	if value == nil {
		*digest = ""
		return nil
	}
	raw, ok := value.([]byte)
	if !ok || len(raw) != sha256.Size {
		return ErrInvalidStoredDigest
	}
	*digest = StoredDigest("sha256:" + hex.EncodeToString(raw))
	return nil
}
