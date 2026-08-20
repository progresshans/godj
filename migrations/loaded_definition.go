package migrations

import "github.com/progresshans/godj/migrations/internal/loadeddefinition"

// DefinitionProducer records non-semantic generator provenance from one
// loaded definition source.
type DefinitionProducer struct {
	Name    string
	Version string
}

// DefinitionSourceInfo is the immutable inventory entry for one definition.
type DefinitionSourceInfo struct {
	SourceID  string
	Producer  DefinitionProducer
	Migration MigrationKey
}

type loadedDefinitionPublication = loadeddefinition.Set[
	Migration,
	DefinitionSourceInfo,
]

// LoadedDefinitionSet is the only public input to a complete migration
// lifecycle. Its fields and constructor remain private to the migrations
// module; definition.Load is the public way to obtain an initialized value.
type LoadedDefinitionSet loadedDefinitionPublication

// Digest returns the canonical semantic definition-set fingerprint.
func (s LoadedDefinitionSet) Digest() string {
	snapshot, ok := s.snapshot()
	if !ok {
		return ""
	}
	return snapshot.Digest
}

// Definitions returns a fresh deep copy for diagnostics and inspection. The
// returned slice is not accepted as loaded Migrate lifecycle authority.
func (s LoadedDefinitionSet) Definitions() []Migration {
	snapshot, ok := s.snapshot()
	if !ok {
		return nil
	}
	return snapshot.Values
}

// Sources returns a fresh copy of the canonical source inventory.
func (s LoadedDefinitionSet) Sources() []DefinitionSourceInfo {
	snapshot, ok := s.snapshot()
	if !ok {
		return nil
	}
	return snapshot.Sources
}

func (s LoadedDefinitionSet) snapshot() (loadeddefinition.Snapshot[Migration, DefinitionSourceInfo], bool) {
	return loadeddefinition.View(loadedDefinitionPublication(s))
}
