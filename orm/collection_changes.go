package orm

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// CollectionChange describes one set replacement without opening a backend.
// Keys are owned. A custom through input must remain safe to call until
// preparation returns; its typed create is evaluated only for missing pairs.
// The change grants no application authority and contains no transaction.
type CollectionChange struct{ state *collectionChange }

type collectionChange struct {
	source    *manyToManyState
	owner     int64
	operation manyChange
	keys      []int64
	clear     bool
	build     func(manyPair) (query.InsertPlan, error)
}

func (CollectionChange) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.CollectionChange{redacted}"))
}

// SetKeysChange prepares the immutable selection for a batch of differently
// typed collections. PrepareCollectionChangesInSession reads all selections
// and collects their complete shared deletion graph before any write.
func (r ManyToMany[O, T, L]) SetKeysChange(owner O, keys []int64, options ...ManyToManySetOptions[L]) (CollectionChange, error) {
	if r.state == nil || r.state.snapshot == nil || interfaceIsNil(r.owner) || interfaceIsNil(r.through) {
		return CollectionChange{}, relationInvalidPlan("collection change has no bound source")
	}
	key, err := manyObjectKey(r.owner, owner)
	if err != nil {
		return CollectionChange{}, err
	}
	if len(options) > 1 {
		return CollectionChange{}, relationInvalidPlan("collection set accepts at most one options value")
	}
	input := r.through.ManyToManyCreateInput()
	clear := false
	if len(options) == 1 {
		clear = options[0].Clear
		if options[0].ThroughDefaults != nil {
			input = options[0].ThroughDefaults
		}
	}
	return newCollectionChange(r.state, r.through, key, manySet, keys, input, clear)
}

func newCollectionChange[L any](source *manyToManyState, through ManyToManyDescriptor[L], owner int64, operation manyChange, keys []int64, input ManyToManyInput[L], clear bool) (CollectionChange, error) {
	if source == nil || source.snapshot == nil || interfaceIsNil(input) || interfaceIsNil(through) {
		return CollectionChange{}, relationInvalidPlan("collection change source or through input is unbound")
	}
	keys = slices.Clone(keys)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	manager := NewManager[L](through)
	state := &collectionChange{source: source, owner: owner, operation: operation, keys: keys, clear: clear}
	state.build = func(pair manyPair) (query.InsertPlan, error) {
		mutation := input.BuildManyToManyCreate(source.source.Clone(), source.target.Clone(), pair.source, pair.target)
		if err := validateMutation(mutation, MutationCreate, manager.prepared, through, nil); err != nil {
			return query.InsertPlan{}, err
		}
		if _, present := through.PrimaryKey(mutation.value); present {
			return query.InsertPlan{}, relationInvalidPlan("collection create input supplied a primary key")
		}
		for _, endpoint := range []struct {
			name string
			key  int64
		}{{source.source.Name, pair.source}, {source.target.Name, pair.target}} {
			for _, assignment := range mutation.assignments {
				if assignment.Field().Name() == endpoint.name && !assignment.Value().Equal(query.Integer(endpoint.key)) {
					return query.InsertPlan{}, relationInvalidPlan("collection create input changed a bound endpoint")
				}
			}
		}
		return query.NewInsertPlanReturningKey(source.model.DBTable, mutation.Assignments(), fieldReference(source.key)), nil
	}
	return CollectionChange{state: state}, nil
}

// CollectionInsertEffect is a proposed typed through insertion. Its key is not
// known until execution. Values contain declared assignments, not an assertion
// that a conflict-tolerant insert actually created a row.
type CollectionInsertEffect struct {
	Model  ir.ModelIdentity
	Values map[string]query.Value
}

func (CollectionInsertEffect) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.CollectionInsertEffect{redacted}"))
}

// CollectionChangeEffects is an immutable description of the whole batch.
// It does not authorize a write or prove final membership. Application owners
// use the common native write fence and verify their final domain invariants.
type CollectionChangeEffects struct {
	deletions  RelationDeleteEffects
	insertions []CollectionInsertEffect
}

func (CollectionChangeEffects) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.CollectionChangeEffects{redacted}"))
}

func (effects CollectionChangeEffects) Deletions() RelationDeleteEffects {
	return RelationDeleteEffects{rows: effects.deletions.Rows()}
}

func (effects CollectionChangeEffects) Insertions() []CollectionInsertEffect {
	result := slices.Clone(effects.insertions)
	for index := range result {
		result[index].Values = maps.Clone(result[index].Values)
	}
	return result
}

// PreparedCollectionChanges belongs to exactly the borrowed session that
// collected it. Copies share one execution attempt. Between preparation and
// Apply the caller must not change observed keys, foreign keys or membership;
// companion scalar/revision/session/audit writes may use the same transaction.
// Propagate every error to the transaction owner: this is not a savepoint.
type PreparedCollectionChanges struct{ state *preparedCollectionChanges }

type preparedCollectionChanges struct {
	session  db.RelationSession
	source   *projectBindingSnapshot
	deletion relationDeletePlan
	changes  []preparedCollectionChange
	effects  CollectionChangeEffects
	consumed atomic.Bool
}
type preparedCollectionChange struct {
	change   *collectionChange
	rows     []manyRow
	desired  []manyPair
	inserts  []query.InsertPlan
	conflict db.ConflictInserter
}

func (PreparedCollectionChanges) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.PreparedCollectionChanges{redacted}"))
}

func (prepared PreparedCollectionChanges) Effects() CollectionChangeEffects {
	if prepared.state == nil {
		return CollectionChangeEffects{}
	}
	return CollectionChangeEffects{deletions: prepared.state.effects.Deletions(), insertions: prepared.state.effects.Insertions()}
}

func (prepared PreparedCollectionChanges) ValidateSource(source ProjectBinding) error {
	if prepared.state == nil || !sameProjectSource(prepared.state.source, source.snapshot) {
		return relationInvalidPlan("prepared collection changes do not match the complete project source")
	}
	return nil
}

func PrepareCollectionChangesInSession(ctx context.Context, session db.RelationSession, changes ...CollectionChange) (PreparedCollectionChanges, error) {
	if interfaceIsNil(session) {
		return PreparedCollectionChanges{}, relationBackendInvalidPlan("collection preparation requires a borrowed relation session")
	}
	if validator, ok := session.(db.SessionValidator); !ok || interfaceIsNil(validator) {
		return PreparedCollectionChanges{}, relationBackendInvalidPlan("collection preparation requires session lifetime validation")
	}
	effective, err := executionBackend(ctx, session)
	if err != nil {
		return PreparedCollectionChanges{}, err
	}
	return prepareCollectionChanges(ctx, effective, changes)
}

func prepareCollectionChanges(ctx context.Context, session db.RelationSession, changes []CollectionChange) (PreparedCollectionChanges, error) {
	if interfaceIsNil(ctx) || interfaceIsNil(session) || len(changes) == 0 {
		return PreparedCollectionChanges{}, relationInvalidPlan("collection preparation context, session or changes are absent")
	}
	if err := validateQuerySession(ctx, session); err != nil {
		return PreparedCollectionChanges{}, err
	}
	state := &preparedCollectionChanges{session: session}
	graph := make(map[ir.ModelIdentity]relationDeleteNode)
	type identity struct {
		model          ir.ModelIdentity
		source, target string
		owner          int64
	}
	seen := make(map[identity]bool)
	// Reject incompatible sources and duplicate selections before any I/O.
	for _, change := range changes {
		current := change.state
		if current == nil || current.source == nil || current.source.snapshot == nil || current.build == nil {
			return PreparedCollectionChanges{}, relationInvalidPlan("collection change is unbound")
		}
		source := current.source
		if state.source == nil {
			state.source = source.snapshot
		} else if !sameProjectSource(state.source, source.snapshot) {
			return PreparedCollectionChanges{}, relationInvalidPlan("collection changes belong to different project sources")
		}
		key := identity{source.through, source.source.Name, source.target.Name, current.owner}
		if seen[key] {
			return PreparedCollectionChanges{}, relationInvalidPlan("collection batch repeats the same owner and storage selection")
		}
		seen[key] = true
		for model, node := range source.graph {
			graph[model] = node
		}
		next := preparedCollectionChange{change: current}
		if len(source.unique) != 0 && (current.operation == manyAdd || current.operation == manySet) {
			var ok bool
			next.conflict, ok = session.(db.ConflictInserter)
			if !ok || interfaceIsNil(next.conflict) {
				return PreparedCollectionChanges{}, relationBackendInvalidPlan("collection unique pair requires native conflict insertion in its transaction session")
			}
		}
		state.changes = append(state.changes, next)
	}
	var roots []relationDeleteRow
	for index := range state.changes {
		current := &state.changes[index]
		var err error
		current.rows, err = current.change.source.rows(ctx, session, current.change.owner)
		if err != nil {
			return PreparedCollectionChanges{}, err
		}
		for _, row := range current.rows {
			partner, present := row.partner(current.change.owner)
			selected := present && slices.Contains(current.change.keys, partner)
			remove := current.change.operation == manyClear || current.change.operation == manySet && (current.change.clear || present && !selected) || current.change.operation == manyRemove && selected
			if remove {
				roots = append(roots, relationDeleteRow{current.change.source.through, row.key})
			}
		}
		if current.change.operation == manyAdd || current.change.operation == manySet {
			pairs := make(map[manyPair]bool)
			for _, key := range current.change.keys {
				values := []manyPair{{current.change.owner, key}}
				if current.change.source.symmetrical {
					values = append(values, manyPair{key, current.change.owner})
				}
				for _, pair := range values {
					if !pairs[pair] {
						pairs[pair] = true
						current.desired = append(current.desired, pair)
					}
				}
			}
		}
	}
	var err error
	state.deletion, err = collectRelationDeleteRoots(ctx, session, graph, roots)
	if err != nil {
		return PreparedCollectionChanges{}, err
	}
	state.effects.deletions = state.deletion.effects()
	deleted := make(map[relationDeleteRow]bool, len(state.deletion.rows))
	for _, row := range state.deletion.rows {
		deleted[row] = true
	}
	// A link retained by one selection may be removed by another selection's
	// cascade. Compute insertions from the complete final surviving link set.
	for index := range state.changes {
		current := &state.changes[index]
		source := current.change.source
		existing := make(map[manyPair]bool)
		for _, row := range current.rows {
			key := relationDeleteRow{source.through, row.key}
			nulls := state.deletion.nulls[key]
			if row.sourcePresent && row.targetPresent && !deleted[key] && !slices.Contains(nulls, source.source.Name) && !slices.Contains(nulls, source.target.Name) {
				existing[row.pair] = true
			}
		}
		// A preceding forward, reverse or aliased selection may already
		// provide this physical pair. Through defaults are only for a new
		// pair; do not insert another explicit-through copy for the same
		// membership merely because the batch names it in two directions.
		for _, insertion := range state.effects.insertions {
			if insertion.Model != source.through {
				continue
			}
			left, leftOK := insertion.Values[source.source.Name].Integer()
			right, rightOK := insertion.Values[source.target.Name].Integer()
			if leftOK && rightOK {
				existing[manyPair{left, right}] = true
			}
		}
		for _, pair := range current.desired {
			if existing[pair] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return PreparedCollectionChanges{}, err
			}
			insert, err := current.change.build(pair)
			if err != nil {
				return PreparedCollectionChanges{}, err
			}
			current.inserts = append(current.inserts, insert)
			values := make(map[string]query.Value)
			for _, assignment := range insert.Assignments() {
				values[assignment.Field().Name()] = assignment.Value()
			}
			state.effects.insertions = append(state.effects.insertions, CollectionInsertEffect{Model: source.through, Values: values})
		}
	}
	if err := validateQuerySession(ctx, session); err != nil {
		return PreparedCollectionChanges{}, err
	}
	return PreparedCollectionChanges{state: state}, nil
}

// Apply executes the collected deletion union once, then every insertion and
// every collection postcondition. Before receives detached effects after all
// reads, PROTECT checks and typed insert preparation. It must obey the same
// topology and session-ownership contract as the enclosing owner.
func (prepared PreparedCollectionChanges) Apply(ctx context.Context, before func(context.Context, CollectionChangeEffects) error) error {
	if prepared.state == nil || !prepared.state.consumed.CompareAndSwap(false, true) {
		return relationInvalidPlan("prepared collection changes are absent or already attempted")
	}
	if interfaceIsNil(ctx) {
		return relationInvalidPlan("context is nil")
	}
	state := prepared.state
	if err := validateQuerySession(ctx, state.session); err != nil {
		return err
	}
	if before != nil {
		err := before(ctx, prepared.Effects())
		if scopeErr := validateQuerySession(ctx, state.session); scopeErr != nil {
			return errors.Join(err, scopeErr)
		}
		if err != nil {
			return err
		}
	}
	if _, err := state.deletion.execute(ctx, state.session); err != nil {
		return err
	}
	for _, current := range state.changes {
		for _, insert := range current.inserts {
			if err := ctx.Err(); err != nil {
				return err
			}
			var err error
			if current.conflict != nil {
				_, err = current.conflict.InsertOnConflict(ctx, query.NewConflictInsertPlan(insert.Table(), insert.Assignments(), current.change.source.unique))
			} else {
				_, err = state.session.Insert(ctx, insert)
			}
			if err != nil {
				return err
			}
		}
	}
	for _, current := range state.changes {
		if err := current.check(ctx, state.session); err != nil {
			return err
		}
	}
	return validateQuerySession(ctx, state.session)
}

func (current preparedCollectionChange) check(ctx context.Context, session db.RelationSession) error {
	final, err := current.change.source.rows(ctx, session, current.change.owner)
	if err != nil {
		return err
	}
	presentPairs := make(map[manyPair]bool, len(final))
	for _, row := range final {
		if row.sourcePresent && row.targetPresent {
			presentPairs[row.pair] = true
		}
		partner, present := row.partner(current.change.owner)
		operation, keys := current.change.operation, current.change.keys
		if operation == manyClear || present && (operation == manyRemove && slices.Contains(keys, partner) || operation == manySet && !slices.Contains(keys, partner)) {
			return relationBackendInvalidPlan("collection deletion postcondition was not preserved")
		}
	}
	for _, pair := range current.desired {
		if !presentPairs[pair] {
			return relationBackendInvalidPlan("collection insertion did not establish the requested membership")
		}
	}
	return ctx.Err()
}
