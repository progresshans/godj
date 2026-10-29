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
	build     func(manyPair) (collectionInsert, error)
	read      func(context.Context, db.RelationSession, int64) (map[string]query.Value, error)
}

func (CollectionChange) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.CollectionChange{redacted}"))
}

// WithCollectionThroughInput replaces only the typed create input of a
// collection selection. The bound model must be the actual through model in
// the same complete project source. Endpoint keys, operation, deletion graph
// and all final collection checks stay owned by the original selection.
// Application owners use this to supply prepared identity candidates without
// opening a second transaction or reducing them to untyped field maps.
func WithCollectionThroughInput[L any](change CollectionChange, model BoundModel[L], input ManyToManyInput[L]) (CollectionChange, error) {
	if err := validateObjectBoundModel(model); err != nil {
		return CollectionChange{}, err
	}
	current := change.state
	if current == nil || current.source == nil || current.source.through != model.identity || !sameProjectSource(current.source.snapshot, model.snapshot) {
		return CollectionChange{}, relationInvalidPlan("collection through input does not match the bound model and complete project source")
	}
	through, ok := model.objectDescriptor.(ManyToManyDescriptor[L])
	if !ok || interfaceIsNil(through) {
		return CollectionChange{}, relationInvalidPlan("collection through model has no typed creation descriptor")
	}
	return newCollectionChange(current.source, through, current.owner, current.operation, current.keys, input, current.clear)
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
	state.build = func(pair manyPair) (collectionInsert, error) {
		mutation := input.BuildManyToManyCreate(source.source.Clone(), source.target.Clone(), pair.source, pair.target)
		if err := validateMutation(mutation, MutationCreate, manager.prepared, through, nil); err != nil {
			return collectionInsert{}, err
		}
		if _, present := through.PrimaryKey(mutation.value); present {
			return collectionInsert{}, relationInvalidPlan("collection create input supplied a primary key")
		}
		for _, endpoint := range []struct {
			name string
			key  int64
		}{{source.source.Name, pair.source}, {source.target.Name, pair.target}} {
			for _, assignment := range mutation.assignments {
				if assignment.Field().Name() == endpoint.name && !assignment.Value().Equal(query.Integer(endpoint.key)) {
					return collectionInsert{}, relationInvalidPlan("collection create input changed a bound endpoint")
				}
			}
		}
		values, err := manager.ModelValues(mutation.value)
		if err != nil {
			return collectionInsert{}, err
		}
		return collectionInsert{plan: query.NewInsertPlanReturningKey(source.model.DBTable, mutation.Assignments(), fieldReference(source.key)), values: values}, nil
	}
	state.read = func(ctx context.Context, scope db.RelationSession, key int64) (map[string]query.Value, error) {
		expression, err := query.NewExpression(query.NewCondition(fieldReference(source.key), query.LookupExact, query.Integer(key)))
		if err != nil {
			return nil, err
		}
		row, err := manager.Using(scope).Filter(Predicate[L]{expression: expression}).Get(ctx)
		if err != nil {
			return nil, err
		}
		return manager.ModelValues(row)
	}

	return CollectionChange{state: state}, nil
}

// CollectionInsertEffect is a proposed typed through insertion. Its key is not
// known until execution. Values contain the complete typed scalar candidate,
// including defaults and an absent (NULL) key. They are not insertion proof.
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
	collectionPlan
	session  db.RelationSession
	consumed atomic.Bool
}

// collectionPlan is read and prepared without recovering write capability. Only
// PreparedCollectionChanges attaches it to a live native relation transaction.
type collectionPlan struct {
	source   *projectBindingSnapshot
	deletion relationDeletePlan
	changes  []preparedCollectionChange
	effects  CollectionChangeEffects
	graph    map[ir.ModelIdentity]relationDeleteNode
}
type preparedCollectionChange struct {
	change   *collectionChange
	rows     []manyRow
	desired  []manyPair
	inserts  []collectionInsert
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
	return prepareCollectionChanges(ctx, effective, changes, nil)
}

func prepareCollectionChanges(ctx context.Context, session db.RelationSession, changes []CollectionChange, observed *observedCollectionChanges) (PreparedCollectionChanges, error) {
	if interfaceIsNil(ctx) || interfaceIsNil(session) {
		return PreparedCollectionChanges{}, relationInvalidPlan("collection preparation context or session is absent")
	}
	if err := validateQuerySession(ctx, session); err != nil {
		return PreparedCollectionChanges{}, err
	}
	plan, err := newCollectionPlan(changes)
	if err != nil {
		return PreparedCollectionChanges{}, err
	}
	if observed != nil {
		if err := observed.checkSelections(plan); err != nil {
			return PreparedCollectionChanges{}, err
		}
	}
	for index := range plan.changes {
		next := &plan.changes[index]
		current := next.change
		if len(current.source.unique) != 0 && (current.operation == manyAdd || current.operation == manySet) {
			var ok bool
			next.conflict, ok = session.(db.ConflictInserter)
			if !ok || interfaceIsNil(next.conflict) {
				return PreparedCollectionChanges{}, relationBackendInvalidPlan("collection unique pair requires native conflict insertion in its transaction session")
			}
		}
	}
	if err := plan.collect(ctx, session, observed != nil); err != nil {
		return PreparedCollectionChanges{}, err
	}
	if observed != nil {
		// No typed input is invoked for a stale selection or deletion graph.
		if err := observed.checkRows(plan); err != nil {
			return PreparedCollectionChanges{}, err
		}
	}
	if err := plan.buildInsertions(ctx); err != nil {
		return PreparedCollectionChanges{}, err
	}
	if observed != nil {
		if err := observed.checkInsertions(plan); err != nil {
			return PreparedCollectionChanges{}, err
		}
	}
	if err := validateQuerySession(ctx, session); err != nil {
		return PreparedCollectionChanges{}, err
	}
	return PreparedCollectionChanges{state: &preparedCollectionChanges{collectionPlan: *plan, session: session}}, nil
}

func newCollectionPlan(changes []CollectionChange) (*collectionPlan, error) {
	if len(changes) == 0 {
		return nil, relationInvalidPlan("collection changes are absent")
	}
	state := &collectionPlan{graph: make(map[ir.ModelIdentity]relationDeleteNode)}
	type identity struct {
		model          ir.ModelIdentity
		source, target string
		owner          int64
	}
	seen := make(map[identity]bool)
	// Reject incompatible sources and duplicate selections before any I/O.
	for _, change := range changes {
		current := change.state
		if current == nil || current.source == nil || current.source.snapshot == nil || current.build == nil || current.read == nil {
			return nil, relationInvalidPlan("collection change is unbound")
		}
		source := current.source
		if state.source == nil {
			state.source = source.snapshot
		} else if !sameProjectSource(state.source, source.snapshot) {
			return nil, relationInvalidPlan("collection changes belong to different project sources")
		}
		key := identity{source.through, source.source.Name, source.target.Name, current.owner}
		if seen[key] {
			return nil, relationInvalidPlan("collection batch repeats the same owner and storage selection")
		}
		seen[key] = true
		for model, node := range source.graph {
			state.graph[model] = node
		}
		state.changes = append(state.changes, preparedCollectionChange{change: current})
	}
	return state, nil
}

func (state *collectionPlan) collect(ctx context.Context, reader db.Queryer, observe bool) error {
	var roots []relationDeleteRow
	for index := range state.changes {
		current := &state.changes[index]
		var err error
		current.rows, err = current.change.source.rows(ctx, reader, current.change.owner)
		if err != nil {
			return err
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
	state.deletion, err = collectRelationDeleteRoots(ctx, reader, state.graph, roots, observe)
	if err != nil {
		return err
	}
	state.effects.deletions = state.deletion.effects()
	return validateQuerySession(ctx, reader)
}

func (state *collectionPlan) buildInsertions(ctx context.Context) error {
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
				return err
			}
			insert, err := current.change.build(pair)
			if err != nil {
				return err
			}
			current.inserts = append(current.inserts, insert)
			state.effects.insertions = append(state.effects.insertions, CollectionInsertEffect{Model: source.through, Values: maps.Clone(insert.values)})
		}
	}
	return ctx.Err()
}

// Apply executes the collected deletion union once, then every insertion and
// every collection postcondition. Before receives detached effects after all
// reads, PROTECT checks and typed insert preparation. It must obey the same
// topology and session-ownership contract as the enclosing owner.
func (prepared PreparedCollectionChanges) Apply(ctx context.Context, before func(context.Context, CollectionChangeEffects) error) (AppliedCollectionChanges, error) {
	var zero AppliedCollectionChanges
	if prepared.state == nil || !prepared.state.consumed.CompareAndSwap(false, true) {
		return zero, relationInvalidPlan("prepared collection changes are absent or already attempted")
	}
	if interfaceIsNil(ctx) {
		return zero, relationInvalidPlan("context is nil")
	}
	state := prepared.state
	if err := validateQuerySession(ctx, state.session); err != nil {
		return zero, err
	}
	if before != nil {
		err := before(ctx, prepared.Effects())
		if scopeErr := validateQuerySession(ctx, state.session); scopeErr != nil {
			return zero, errors.Join(err, scopeErr)
		}
		if err != nil {
			return zero, err
		}
	}
	if _, err := state.deletion.execute(ctx, state.session); err != nil {
		return zero, err
	}
	var inserted []CollectionInsertedRow
	var readers []func(context.Context, db.RelationSession, int64) (map[string]query.Value, error)
	seen := make(map[relationDeleteRow]bool)
	for _, current := range state.changes {
		for _, insert := range current.inserts {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			var key int64
			if current.conflict != nil {
				result, err := current.conflict.InsertOnConflict(ctx, query.NewConflictInsertPlan(insert.plan, current.change.source.unique))
				if err != nil {
					return zero, err
				}
				if !result.Inserted {
					if result.Key != 0 {
						return zero, relationBackendInvalidPlan("skipped collection insertion returned a key")
					}
					continue
				}
				key = result.Key
			} else {
				var err error
				key, err = state.session.Insert(ctx, insert.plan)
				if err != nil {
					return zero, err
				}
			}
			identity := relationDeleteRow{current.change.source.through, key}
			if seen[identity] {
				return zero, relationBackendInvalidPlan("collection insertions returned the same physical key")
			}
			seen[identity] = true
			values := maps.Clone(insert.values)
			values[current.change.source.key.Name] = query.Integer(key)
			inserted = append(inserted, CollectionInsertedRow{Model: current.change.source.through, Key: key, Values: values})
			readers = append(readers, current.change.read)
		}
	}
	for _, current := range state.changes {
		if err := current.check(ctx, state.session); err != nil {
			return zero, err
		}
	}
	// Read every actual key after the whole write set: a later insertion/trigger
	// may have changed an earlier row. Logical membership alone cannot prove its
	// nullable endpoints, typed payload or returned identity remained intact.
	applied := AppliedCollectionChanges{state: &appliedCollectionChanges{source: state.source, inserted: inserted, session: state.session, readers: readers}}
	if err := applied.Verify(ctx); err != nil {
		return zero, err
	}
	return applied, nil
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
