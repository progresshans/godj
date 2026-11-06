package orm

import (
	"context"
	"fmt"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ObservedCollectionChanges owns detached read evidence, not a transaction or
// authority. A composing owner can close its snapshot, prepare expensive scalar
// values such as password hashes, then recheck the observed topology in its
// final write fence. Database/operation identity, current actor, scalar policy
// and revision checks remain the owner's responsibility. An observation is
// reusable and is not an attempt token.
type ObservedCollectionChanges struct{ state *observedCollectionChanges }

type observedCollectionChanges struct {
	source     *projectBindingSnapshot
	selections []observedCollectionSelection
	references []relationDeleteObservation
	effects    CollectionChangeEffects
}

type observedCollectionSelection struct {
	model              ir.ModelIdentity
	source, target     string
	owner              int64
	operation          manyChange
	clear, symmetrical bool
	keys               []int64
	rows               []manyRow
}

func (ObservedCollectionChanges) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.ObservedCollectionChanges{redacted}"))
}

// ObserveCollectionChangesInReadScope collects the complete deletion union and
// prepares typed insert candidates in one borrowed read-only snapshot. Inputs
// run only for missing physical pairs, after all reads and PROTECT checks, just
// as in PrepareCollectionChangesInSession. Inputs must be pure: hashing and
// other expensive or external work belongs after this snapshot closes.
//
// The result retains neither the reader nor the inputs. The caller must publish
// it only after the enclosing snapshot reports successful cleanup; use
// db.ReadSnapshotResult when the caller owns that snapshot.
func ObserveCollectionChangesInReadScope(ctx context.Context, reader db.Queryer, changes ...CollectionChange) (ObservedCollectionChanges, error) {
	var zero ObservedCollectionChanges
	if err := validateCollectionObservationScope(ctx, reader); err != nil {
		return zero, err
	}
	effective, err := executionBackend(ctx, reader)
	if err != nil {
		return zero, err
	}
	if err := validateCollectionObservationScope(ctx, effective); err != nil {
		return zero, err
	}
	plan, err := newCollectionPlan(changes)
	if err != nil {
		return zero, err
	}
	if err := plan.collect(ctx, effective, true); err != nil {
		return zero, err
	}
	if err := plan.buildInsertions(ctx); err != nil {
		return zero, err
	}
	if err := validateQuerySession(ctx, effective); err != nil {
		return zero, err
	}
	state := &observedCollectionChanges{source: plan.source, effects: plan.effects, references: plan.deletion.observations}
	for _, prepared := range plan.changes {
		change := prepared.change
		state.selections = append(state.selections, observedCollectionSelection{
			model: change.source.through, source: change.source.source.Name, target: change.source.target.Name,
			owner: change.owner, operation: change.operation, clear: change.clear, symmetrical: change.source.symmetrical,
			keys: slices.Clone(change.keys), rows: prepared.rows,
		})
	}
	return ObservedCollectionChanges{state: state}, nil
}

func validateCollectionObservationScope(ctx context.Context, reader db.Queryer) error {
	if interfaceIsNil(ctx) || interfaceIsNil(reader) {
		return relationInvalidPlan("collection observation context or reader is absent")
	}
	if validator, ok := reader.(db.SessionValidator); !ok || interfaceIsNil(validator) {
		return relationBackendInvalidPlan("collection observation requires a borrowed read scope")
	}
	switch reader.(type) {
	case db.Mutator, db.RelationMutator, db.ConflictInserter, db.BulkInserter, db.BulkUpdater, db.QueryUpdater,
		db.Atomic, db.CoordinatedAtomic, db.RelationAtomic, db.CoordinatedRelationAtomic, db.Savepointer, db.SnapshotReader:
		return relationBackendInvalidPlan("collection observation scope must not expose mutation or transaction capabilities")
	}
	return validateQuerySession(ctx, reader)
}

func (observed ObservedCollectionChanges) ValidateSource(source ProjectBinding) error {
	if observed.state == nil || !sameProjectSource(observed.state.source, source.snapshot) {
		return relationInvalidPlan("observed collection changes do not match the complete project source")
	}
	return nil
}

func (observed ObservedCollectionChanges) Effects() CollectionChangeEffects {
	if observed.state == nil {
		return CollectionChangeEffects{}
	}
	return CollectionChangeEffects{deletions: observed.state.effects.Deletions(), insertions: observed.state.effects.Insertions()}
}

// PrepareInSession rechecks the same ordered selections, physical links and
// every incoming deletion edge before invoking any supplied typed input. A
// changed observation returns query.CodeCollectionChanged without writing.
//
// Inputs may supply new non-relational scalar values. Every typed insertion's
// model and all FK values (including nullable or secondary endpoints) must
// still agree with the observation, preserving shared-through coalescing. The
// returned plan belongs solely to this live session and keeps the ordinary
// single-attempt, full-row readback and outer-transaction error contract.
func (observed ObservedCollectionChanges) PrepareInSession(ctx context.Context, session db.RelationSession, changes ...CollectionChange) (PreparedCollectionChanges, error) {
	if observed.state == nil {
		return PreparedCollectionChanges{}, relationInvalidPlan("collection observation is absent")
	}
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
	return prepareCollectionChanges(ctx, effective, changes, observed.state)
}

func (observed *observedCollectionChanges) checkSelections(plan *collectionPlan) error {
	if !sameProjectSource(observed.source, plan.source) {
		return relationInvalidPlan("collection observation and changes belong to different project sources")
	}
	if len(observed.selections) != len(plan.changes) {
		return collectionObservationChanged()
	}
	for index, expected := range observed.selections {
		actual := plan.changes[index].change
		if expected.model != actual.source.through || expected.source != actual.source.source.Name || expected.target != actual.source.target.Name ||
			expected.owner != actual.owner || expected.operation != actual.operation || expected.clear != actual.clear || expected.symmetrical != actual.source.symmetrical || !slices.Equal(expected.keys, actual.keys) {
			return collectionObservationChanged()
		}
	}
	return nil
}

func (observed *observedCollectionChanges) checkRows(plan *collectionPlan) error {
	for index, selection := range observed.selections {
		if !slices.Equal(selection.rows, plan.changes[index].rows) {
			return collectionObservationChanged()
		}
	}
	if !slices.EqualFunc(observed.references, plan.deletion.observations, func(a, b relationDeleteObservation) bool {
		return a.target == b.target && a.source == b.source && a.field == b.field && slices.Equal(a.keys, b.keys)
	}) {
		return collectionObservationChanged()
	}
	return nil
}

func (observed *observedCollectionChanges) checkInsertions(plan *collectionPlan) error {
	if len(observed.effects.insertions) != len(plan.effects.insertions) {
		return collectionObservationChanged()
	}
	for index, expected := range observed.effects.insertions {
		actual := plan.effects.insertions[index]
		if expected.Model != actual.Model {
			return collectionObservationChanged()
		}
		model, found := observed.source.models[expected.Model]
		if !found {
			return relationInvalidPlan("observed collection insertion is outside its project source")
		}
		for _, field := range model.Fields {
			if field.Relation != nil && !expected.Values[field.Name].Equal(actual.Values[field.Name]) {
				return collectionObservationChanged()
			}
		}
	}
	return nil
}

func collectionObservationChanged() error {
	return &query.Error{Category: query.CategoryModelState, Code: query.CodeCollectionChanged, Detail: "collection topology changed after its read observation"}
}
