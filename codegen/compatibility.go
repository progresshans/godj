package codegen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ProjectGeneratorABI identifies the complete generator roster used by project
// snapshots and manifests. It is a compatibility token, not a source digest.
func ProjectGeneratorABI() string {
	data, _ := json.Marshal(projectGeneratorABIRoster()) // Concrete strings only.
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
