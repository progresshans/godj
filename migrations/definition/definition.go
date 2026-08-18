// Package definition loads caller-provided migration definition documents into
// bounded, immutable-by-contract migration snapshots. It performs no I/O and
// deliberately leaves source discovery to the caller.
package definition

import (
	"context"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/internal/definitionhandoff"
)

const (
	DefinitionFormatVersion       int64 = 1
	LoaderABIVersion              int64 = 1
	OperationCodecVersion         int64 = 1
	SchemaIRVersion               int64 = 2
	RelationLoaderABIVersion      int64 = 2
	RelationOperationCodecVersion int64 = 2
	RelationSchemaIRVersion       int64 = 3

	EmptySetDigest = "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"
)

// Source is one explicitly supplied migration definition document. SourceID is
// diagnostic ordering metadata, not a migration identity or filesystem path.
// Load snapshots both fields synchronously before retaining any input.
type Source struct {
	SourceID string
	Document []byte
}

// Producer records non-semantic generator provenance from a source envelope.
type Producer struct {
	Name    string
	Version string
}

// SourceInfo is the immutable-by-contract inventory entry published for one
// successfully loaded definition.
type SourceInfo struct {
	SourceID  string
	Producer  Producer
	Migration migrations.MigrationKey
}

// Set owns a canonical migration-definition snapshot. Its zero value is the
// canonical empty set. It never retains raw source documents.
type Set struct {
	definitions []migrations.Migration
	digest      string
	sources     []SourceInfo
	handoff     definitionhandoff.Handoff
}

// Digest returns the canonical semantic definition-set fingerprint.
func (s Set) Digest() string {
	if s.digest == "" {
		return EmptySetDigest
	}
	return s.digest
}

// Definitions returns a fresh deep copy of the loaded migration definitions.
func (s Set) Definitions() []migrations.Migration {
	return cloneMigrations(s.definitions)
}

// Sources returns a fresh copy of the canonical source inventory.
func (s Set) Sources() []SourceInfo {
	return append([]SourceInfo(nil), s.sources...)
}

// Migrate hands a fresh definition snapshot and the caller's immutable request
// value to the existing revision-fenced lifecycle exactly once. Request
// validation and snapshotting remain owned by migrations.Executor.
func (s Set) Migrate(
	ctx context.Context,
	executor migrations.Executor,
	request migrations.LifecycleRequest,
) (migrations.ProjectState, error) {
	if ctx != nil && !s.handoff.IsZero() {
		ctx = definitionhandoff.WithContext(ctx, s.handoff.Clone())
	}
	return executor.Migrate(ctx, cloneMigrations(s.definitions), request)
}

func newSet(
	definitions []migrations.Migration,
	digest string,
	sources []SourceInfo,
	handoff definitionhandoff.Handoff,
) Set {
	return Set{
		definitions: cloneMigrations(definitions),
		digest:      digest,
		sources:     append([]SourceInfo(nil), sources...),
		handoff:     handoff.Clone(),
	}
}
