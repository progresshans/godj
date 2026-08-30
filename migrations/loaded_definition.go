package migrations

import (
	"errors"

	"github.com/progresshans/godj/migrations/internal/loadeddefinition"
)

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

// LoadedDefinitionSet is the only public definition input to a complete
// migration lifecycle, its read-only plan/status views, or pure migration SQL
// projection. Its fields and constructor remain private to the migrations
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
// returned slice is not accepted as loaded Migrate or Plan lifecycle
// authority.
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

// Statuses validates and lists one applied-history snapshot against this
// loader-owned complete definition set. It is the read-only inspection
// counterpart to Executor.Migrate and Executor.Plan: callers cannot substitute
// a partial raw definition slice for the loader publication authority.
func (s LoadedDefinitionSet) Statuses(applied AppliedState) ([]MigrationStatusEntry, error) {
	snapshot, ok := s.snapshot()
	if !ok {
		return nil, invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition set is invalid"))
	}
	planner, err := NewPlanner(snapshot.Values...)
	if err != nil {
		return nil, err
	}
	return planner.Statuses(applied)
}

func (s LoadedDefinitionSet) snapshot() (loadeddefinition.Snapshot[Migration, DefinitionSourceInfo], bool) {
	return loadeddefinition.View(loadedDefinitionPublication(s))
}
