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

// Fingerprint selects the transitive CASCADE closure of an already-validated
// incoming policy graph. Every reached node includes all of its incoming
// policies, including PROTECT and SET_NULL. Unrelated nodes do not contribute.
// Structural validation belongs to callers; this function owns reachability,
// canonical ordering and length framing without mutating the supplied graph.
func Fingerprint(target ModelKey, incoming map[ir.ModelIdentity][]Edge) string {
	reached := map[ir.ModelIdentity]ModelKey{target.Identity: target}
	queue := []ModelKey{target}
	for cursor := 0; cursor < len(queue); cursor++ {
		for _, edge := range incoming[queue[cursor].Identity] {
			if edge.OnDelete != ir.DeleteCascade {
				continue
			}
			if _, exists := reached[edge.Source.Identity]; !exists {
				reached[edge.Source.Identity] = edge.Source
				queue = append(queue, edge.Source)
			}
		}
	}
	slices.SortFunc(queue, func(left, right ModelKey) int { return compareIdentity(left.Identity, right.Identity) })
	digest := sha256.New()
	writeValues(digest, "godj-relation-delete-policy-v2")
	writeModelKey(digest, target)
	writeValues(digest, strconv.Itoa(len(queue)))
	for _, model := range queue {
		edges := slices.Clone(incoming[model.Identity])
		slices.SortFunc(edges, func(left, right Edge) int {
			return cmp.Or(compareIdentity(left.Source.Identity, right.Source.Identity), cmp.Compare(left.Field, right.Field))
		})
		writeModelKey(digest, model)
		writeValues(digest, strconv.Itoa(len(edges)))
		for _, edge := range edges {
			nullable := "0"
			if edge.Nullable {
				nullable = "1"
			}
			writeModelKey(digest, edge.Source)
			writeValues(digest, edge.Field, edge.Column, nullable, string(edge.Cardinality), string(edge.OnDelete))
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func compareIdentity(left, right ir.ModelIdentity) int {
	return cmp.Or(cmp.Compare(left.AppLabel, right.AppLabel), cmp.Compare(left.ModelName, right.ModelName))
}

func writeModelKey(digest hash.Hash, model ModelKey) {
	writeValues(digest, model.Identity.AppLabel, model.Identity.ModelName, model.Table, model.PrimaryKeyName, model.PrimaryKeyColumn)
}

func writeValues(digest hash.Hash, values ...string) {
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
}
