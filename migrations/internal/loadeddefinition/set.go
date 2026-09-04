// Package loadeddefinition owns the opaque publication container shared by
// the migration root and its definition loader. It deliberately knows
// nothing about migration semantics and exists only to keep the import graph
// acyclic while preventing callers outside the migrations tree from forging
// a loaded definition set.
package loadeddefinition

// Set is an initialized, immutable-by-contract publication. Its fields are
// private so only New can create a usable value. Clone functions are retained
// inside the set and applied at every publication and snapshot boundary.
type Set[M, S any] struct {
	initialized  bool
	values       []M
	digest       string
	sources      []S
	cloneValues  func([]M) []M
	cloneSources func([]S) []S
}

// Snapshot is a fresh view of one initialized publication.
type Snapshot[M, S any] struct {
	Values  []M
	Digest  string
	Sources []S
}

// New publishes one initialized set after cloning every caller-owned value.
func New[M, S any](
	values []M,
	digest string,
	sources []S,
	cloneValues func([]M) []M,
	cloneSources func([]S) []S,
) Set[M, S] {
	if digest == "" || len(values) != len(sources) || cloneValues == nil || cloneSources == nil {
		return Set[M, S]{}
	}
	clonedValues := cloneValues(values)
	clonedSources := cloneSources(sources)
	if len(clonedValues) != len(values) || len(clonedSources) != len(sources) {
		return Set[M, S]{}
	}
	return Set[M, S]{
		initialized:  true,
		values:       clonedValues,
		digest:       digest,
		sources:      clonedSources,
		cloneValues:  cloneValues,
		cloneSources: cloneSources,
	}
}

// View returns false for a zero or malformed set.
func View[M, S any](set Set[M, S]) (Snapshot[M, S], bool) {
	if !set.initialized || set.digest == "" || set.cloneValues == nil || set.cloneSources == nil {
		return Snapshot[M, S]{}, false
	}
	values := set.cloneValues(set.values)
	sources := set.cloneSources(set.sources)
	if len(values) != len(set.values) || len(sources) != len(set.sources) {
		return Snapshot[M, S]{}, false
	}
	return Snapshot[M, S]{
		Values:  values,
		Digest:  set.digest,
		Sources: sources,
	}, true
}
