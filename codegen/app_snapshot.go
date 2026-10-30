package codegen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Each companion declares an intrinsic ABI marker. An importing project
// references all four, so an old or mixed companion cannot compile merely
// because the main model file is current. Host identity is not part of it.
func appSnapshotMarker(schemaHash string, part appCompanion) string {
	payload := strings.Join([]string{
		"godj/generated-app/v1", schemaHash, GeneratorVersion,
		RelationMetadataGeneratorVersion, RelationObjectGeneratorVersion,
		RelationProjectionGeneratorVersion,
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("GoDjAppPart%d_%s", part, hex.EncodeToString(digest[:]))
}

// AppSnapshotMarkers returns the compile-time identities for the four current
// generated app companions, in main/metadata/object/projection order. The
// schema hash comes from normalized IR or a validated generation manifest.
func AppSnapshotMarkers(schemaHash string) [4]string {
	var result [4]string
	for part := appMain; part <= appProjection; part++ {
		result[part] = appSnapshotMarker(schemaHash, part)
	}
	return result
}
