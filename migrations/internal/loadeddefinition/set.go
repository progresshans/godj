// Package loadeddefinition owns the opaque publication container shared by
// the migration root and its definition loader. It deliberately knows
// nothing about migration semantics and exists only to keep the import graph
// acyclic while preventing callers outside the migrations tree from forging
// a loaded definition set.
package loadeddefinition

// Set is an immutable publication with a prepared immutable value owned by its
// loader. Constructors and borrowed views stay inside the migrations tree.
// Clone functions must be pure and concurrency-safe; only exported mutable
// inspection copies invoke them after publication.
type Set[M, S, P any] struct {
	initialized  bool
	values       []M
	digest       string
	sources      []S
	prepared     P
	cloneValues  func([]M) []M
	cloneSources func([]S) []S
}

// View borrows publication-owned storage. Its slices and prepared value must
// never be mutated or exposed to external callers. Values/Sources own copies.
type View[M, S, P any] struct {
	Values   []M
	Digest   string
	Sources  []S
	Prepared P
}

// New publishes one initialized set after cloning every caller-owned value.
func New[M, S, P any](
	values []M,
	digest string,
	sources []S,
	prepared P,
	cloneValues func([]M) []M,
	cloneSources func([]S) []S,
) Set[M, S, P] {
	if digest == "" || len(values) != len(sources) || cloneValues == nil || cloneSources == nil {
		return Set[M, S, P]{}
	}
	clonedValues := cloneValues(values)
	clonedSources := cloneSources(sources)
	if len(clonedValues) != len(values) || len(clonedSources) != len(sources) {
		return Set[M, S, P]{}
	}
	return Set[M, S, P]{
		initialized:  true,
		values:       clonedValues,
		digest:       digest,
		sources:      clonedSources,
		prepared:     prepared,
		cloneValues:  cloneValues,
		cloneSources: cloneSources,
	}
}

func (set Set[M, S, P]) valid() bool {
	return set.initialized && set.digest != "" && set.cloneValues != nil && set.cloneSources != nil && len(set.values) == len(set.sources)
}

func Borrow[M, S, P any](set Set[M, S, P]) (View[M, S, P], bool) {
	if !set.valid() {
		return View[M, S, P]{}, false
	}
	return View[M, S, P]{
		Values:   set.values,
		Digest:   set.digest,
		Sources:  set.sources,
		Prepared: set.prepared,
	}, true
}

func Digest[M, S, P any](set Set[M, S, P]) string {
	if !set.valid() {
		return ""
	}
	return set.digest
}

func Values[M, S, P any](set Set[M, S, P]) []M {
	if !set.valid() {
		return nil
	}
	return set.cloneValues(set.values)
}

func Sources[M, S, P any](set Set[M, S, P]) []S {
	if !set.valid() {
		return nil
	}
	return set.cloneSources(set.sources)
}
