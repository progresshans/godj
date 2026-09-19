// Package relationpolicy defines the canonical incoming-ForeignKey policy
// fingerprint shared by generated expectations and runtime binding checks.
package relationpolicy

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"slices"
	"strconv"

	"github.com/progresshans/godj/schema/ir"
)

type ModelKey struct {
	Identity         ir.ModelIdentity
	Table            string
	PrimaryKeyName   string
	PrimaryKeyColumn string
}

type Edge struct {
	Source      ModelKey
	Field       string
	Column      string
	Nullable    bool
	Cardinality ir.RelationCardinality
	OnDelete    ir.DeletePolicy
}

// Fingerprint encodes already-validated model policy. Structural validation
// belongs to each caller; this function owns field order, edge ordering and
// unambiguous length framing. It never changes the supplied edge slice.
func Fingerprint(target ModelKey, incoming []Edge) string {
	edges := slices.Clone(incoming)
	slices.SortFunc(edges, func(left, right Edge) int {
		return cmp.Or(cmp.Compare(left.Source.Identity.AppLabel, right.Source.Identity.AppLabel),
			cmp.Compare(left.Source.Identity.ModelName, right.Source.Identity.ModelName), cmp.Compare(left.Field, right.Field))
	})
	digest := sha256.New()
	writeValues(digest, "godj-relation-delete-policy-v1", target.Identity.AppLabel, target.Identity.ModelName,
		target.Table, target.PrimaryKeyName, target.PrimaryKeyColumn, strconv.Itoa(len(edges)))
	for _, edge := range edges {
		nullable := "0"
		if edge.Nullable {
			nullable = "1"
		}
		writeValues(digest, edge.Source.Identity.AppLabel, edge.Source.Identity.ModelName, edge.Source.Table,
			edge.Source.PrimaryKeyName, edge.Source.PrimaryKeyColumn, edge.Field, edge.Column,
			nullable, string(edge.Cardinality), string(edge.OnDelete))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeValues(digest hash.Hash, values ...string) {
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
}
