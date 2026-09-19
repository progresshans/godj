package orm

import "sync"

// RelationCacheState distinguishes an unloaded relation from a known present
// or absent value. It does not describe the target model's primary-key state.
type RelationCacheState uint8

const (
	RelationUnassigned RelationCacheState = iota
	RelationAssignedPresent
	RelationAssignedAbsent
)

// RelationCache is the common state cell used by generated project models.
// Its fields are private so invalid state/target/pending combinations cannot be
// published by generated or application code. Copy ownership remains explicit:
// Clone creates an independent cell and preserves the target pointer identity.
type RelationCache[T any] struct {
	mu      sync.Mutex
	state   RelationCacheState
	target  *T
	pending bool
}

func NewRelationCache[T any]() *RelationCache[T] { return &RelationCache[T]{} }

func validRelationCache[T any](state RelationCacheState, target *T, pending bool) bool {
	switch state {
	case RelationUnassigned, RelationAssignedAbsent:
		return target == nil && !pending
	case RelationAssignedPresent:
		return target != nil
	default:
		return false
	}
}

// Snapshot returns one consistent state. A pending present value was assigned
// before its target acquired a primary key and still needs save reconciliation.
func (cache *RelationCache[T]) Snapshot() (RelationCacheState, *T, bool, error) {
	if cache == nil {
		return 0, nil, false, relationInvalidPlan("relation cache is nil")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !validRelationCache(cache.state, cache.target, cache.pending) {
		return 0, nil, false, relationInvalidPlan("relation cache state tuple is corrupt")
	}
	return cache.state, cache.target, cache.pending, nil
}

// Store atomically replaces one valid tuple. Invalid input leaves the cell
// unchanged; a missing value can never carry an assignment-pending marker.
func (cache *RelationCache[T]) Store(state RelationCacheState, target *T, pending bool) error {
	if cache == nil || !validRelationCache(state, target, pending) {
		return relationInvalidPlan("relation cache is nil or corrupt")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.state, cache.target, cache.pending = state, target, pending
	return nil
}

func (cache *RelationCache[T]) Clone() (*RelationCache[T], error) {
	state, target, pending, err := cache.Snapshot()
	if err != nil {
		return nil, err
	}
	return &RelationCache[T]{state: state, target: target, pending: pending}, nil
}
