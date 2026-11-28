package orm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/progresshans/godj/schema/ir"
)

// SourceSHA256 identifies the complete immutable relation source, including
// host models and incoming policies. It is declaration metadata for process
// handoffs, not authorization, a signature, or a replacement for capability
// ValidateSource checks. Equivalent independently bound sources have the same
// digest; an unbound zero value has no source.
func (binding ProjectBinding) SourceSHA256() (string, error) {
	if binding.snapshot == nil {
		return "", errors.New("relation binding: unbound source")
	}
	type modelSource struct {
		Identity ir.ModelIdentity
		Model    ir.Model
	}
	models := make([]modelSource, 0, len(binding.snapshot.models))
	for identity, model := range binding.snapshot.models {
		models = append(models, modelSource{identity, model})
	}
	slices.SortFunc(models, func(left, right modelSource) int {
		if order := strings.Compare(left.Identity.AppLabel, right.Identity.AppLabel); order != 0 {
			return order
		}
		return strings.Compare(left.Identity.ModelName, right.Identity.ModelName)
	})
	encoded, err := json.Marshal(struct {
		Models  []modelSource
		Forward []RelationMetadata
		Reverse []ReverseRelationMetadata
		Many    []ir.ManyToManyBinding
	}{models, binding.snapshot.forward, binding.snapshot.reverse, binding.snapshot.manyToMany})
	if err != nil {
		return "", errors.New("relation binding: invalid source encoding")
	}
	digest := sha256.Sum256(append([]byte("godj.orm.project-source.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}
