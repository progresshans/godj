// Package definition loads caller-provided migration definition documents into
// bounded, immutable-by-contract migration snapshots. It performs no I/O and
// deliberately leaves source discovery to the caller.
package definition

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/internal/loadeddefinition"
)

const (
	DefinitionFormatVersion int64 = 1

	EmptySetDigest = "sha256:1412c48d7da2299b6f2be7a614c5bb9ce510027328f6baed72ae05cbecc9b494"
)

// Source is one explicitly supplied migration definition document. SourceID is
// diagnostic ordering metadata, not a migration identity or filesystem path.
// Load snapshots both fields synchronously before retaining any input.
type Source struct {
	SourceID string
	Document []byte
}

// Producer records non-semantic generator provenance from a source envelope.
type Producer = migrations.DefinitionProducer

// SourceInfo is the immutable-by-contract inventory entry published for one
// successfully loaded definition.
type SourceInfo = migrations.DefinitionSourceInfo

func newSet(
	definitions []migrations.Migration,
	digest string,
	sources []SourceInfo,
) migrations.LoadedDefinitionSet {
	published := loadeddefinition.New(
		definitions,
		digest,
		sources,
		cloneMigrations,
		func(values []SourceInfo) []SourceInfo { return append([]SourceInfo(nil), values...) },
	)
	return migrations.LoadedDefinitionSet(published)
}
