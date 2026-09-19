package migrations

import (
	"context"
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
	Planner,
]

// LoadedDefinitionSet is the only public definition input to a complete
// migration lifecycle, its read-only plan/status views, or pure migration SQL
// projection. Its fields and constructor remain private to the migrations
// module; definition.Load is the public way to obtain an initialized value.
type LoadedDefinitionSet loadedDefinitionPublication

// Digest returns the canonical semantic definition-set fingerprint.
func (s LoadedDefinitionSet) Digest() string {
	return loadeddefinition.Digest(loadedDefinitionPublication(s))
}

// Definitions returns a fresh deep copy for diagnostics and inspection. The
// returned slice is not accepted as loaded Migrate or Plan lifecycle
// authority.
func (s LoadedDefinitionSet) Definitions() []Migration {
	return loadeddefinition.Values(loadedDefinitionPublication(s))
}

// Sources returns a fresh copy of the canonical source inventory.
func (s LoadedDefinitionSet) Sources() []DefinitionSourceInfo {
	return loadeddefinition.Sources(loadedDefinitionPublication(s))
}

// Reconstructor prepares the historical-state core from this loader-owned
// catalog. It reuses the immutable identity graph while checking operation
// resources, chronology and forward/reverse readiness before publication.
// The returned reconstructor can serve repeated requests and never exposes
// aliases to the definition set or to previously returned states.
func (s LoadedDefinitionSet) Reconstructor(ctx context.Context) (StateReconstructor, error) {
	if ctx == nil {
		return StateReconstructor{}, executionContextError(PlanStep{}, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return StateReconstructor{}, executionContextError(PlanStep{}, err)
	}
	view, ok := s.view()
	if !ok {
		return StateReconstructor{}, invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition set is invalid"))
	}
	if err := validateLoadedDefinitionResources(view.Values); err != nil {
		return StateReconstructor{}, err
	}
	core, err := buildLoadedStateReconstructor(ctx, view.Values, view.Prepared)
	if err != nil {
		return StateReconstructor{}, err
	}
	return StateReconstructor{core: core, initialized: true}, nil
}

// Statuses validates and lists one applied-history snapshot against this
// loader-owned complete definition set. It is the read-only inspection
// counterpart to Executor.Migrate and Executor.Plan: callers cannot substitute
// a partial raw definition slice for the loader publication authority.
func (s LoadedDefinitionSet) Statuses(applied AppliedState) ([]MigrationStatusEntry, error) {
	view, ok := s.view()
	if !ok {
		return nil, invalidLoadedState(Migration{}, NoOperation, "", errors.New("loaded definition set is invalid"))
	}
	return view.Prepared.Statuses(applied)
}

func (s LoadedDefinitionSet) view() (loadeddefinition.View[Migration, DefinitionSourceInfo, Planner], bool) {
	view, ok := loadeddefinition.Borrow(loadedDefinitionPublication(s))
	return view, ok && view.Prepared.graph != nil
}
