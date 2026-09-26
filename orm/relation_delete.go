package orm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// RelationDeleter is one immutable project-bound low-level delete capability.
// It owns the complete incoming policy of the target and its CASCADE closure.
type RelationDeleter[M any] struct {
	state  relationDeleteState[M]
	marker [0]func(M)
}

type relationDeleteState[M any] struct {
	descriptor  WriteDescriptor[M]
	target      ir.ModelIdentity
	targetModel ir.Model
	targetKey   ir.Field
	graph       map[ir.ModelIdentity]relationDeleteNode
	valid       bool
	marker      [0]func(M)
}

type relationDeleteEdge struct {
	metadata         RelationMetadata
	sourceModel      ir.Model
	sourcePrimaryKey ir.Field
	sourceForeignKey ir.Field
}

// BindRelationDeleter seals one generated write descriptor to the complete
// incoming relation policy in an immutable project binding.
func BindRelationDeleter[M any](
	binding ProjectBinding,
	identity ir.ModelIdentity,
	descriptor WriteDescriptor[M],
	expectedPolicySHA256 string,
) (RelationDeleter[M], error) {
	if interfaceIsNil(descriptor) {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete descriptor is nil")
	}
	if !immutableZeroStateValue(descriptor) {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete descriptor must be a named non-pointer zero-size struct")
	}
	if binding.snapshot == nil {
		return RelationDeleter[M]{}, relationInvalidPlan("project binding is unbound")
	}
	targetModel, ok := binding.snapshot.models[identity]
	if !ok {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete target identity is not present in project binding")
	}
	if !reflect.DeepEqual(descriptor.Metadata().Clone(), targetModel) {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete descriptor metadata does not match project model")
	}
	targetKey, ok := relationDeleteTargetKey(targetModel)
	if !ok {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete target must contain supported storage fields and exactly one AutoField primary key")
	}

	graph, err := relationDeleteGraph(binding.snapshot, identity)
	if err != nil {
		return RelationDeleter[M]{}, err
	}
	if len(graph[identity].incoming) == 0 {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete target has no supported incoming ForeignKey")
	}
	if !validLowerSHA256(expectedPolicySHA256) {
		return RelationDeleter[M]{}, relationInvalidPlan("expected relation policy fingerprint must be lowercase SHA-256")
	}
	actualFingerprint := relationDeletePolicyFingerprint(identity, targetModel, targetKey, graph)
	if actualFingerprint != expectedPolicySHA256 {
		return RelationDeleter[M]{}, relationInvalidPlan("relation policy graph fingerprint does not match project binding")
	}

	state := relationDeleteState[M]{
		descriptor:  descriptor,
		target:      identity,
		targetModel: targetModel.Clone(),
		targetKey:   targetKey.Clone(),
		graph:       graph,
		valid:       true,
	}

	return RelationDeleter[M]{state: state}, nil
}

// Delete collects every CASCADE row and checks all reached PROTECT edges before
// SET_NULL and exact-key deletes in one AtomicRelation transaction.
func (d RelationDeleter[M]) Delete(
	ctx context.Context,
	backend db.RelationAtomic,
	target *M,
) (int64, error) {
	if interfaceIsNil(ctx) {
		return 0, relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if interfaceIsNil(backend) {
		return 0, relationBackendInvalidPlan("relation atomic backend is nil")
	}
	if target == nil {
		return 0, relationInvalidPlan("relation delete target pointer is nil")
	}
	if err := d.state.validate(); err != nil {
		return 0, err
	}

	targetKey, err := d.state.preflight(*target)
	if err != nil {
		return 0, err
	}
	backend, err = executionBackend(ctx, backend)
	if err != nil {
		return 0, err
	}
	var deleted int64

	err = runRelationAtomic(ctx, backend.AtomicRelation, func(session db.RelationSession) error {
		count, err := d.state.execute(ctx, session, targetKey)
		deleted = count
		return err
	})
	if err != nil {
		return 0, err
	}

	// Successful commit is authoritative. Do not downgrade it for a context
	// transition or connection-return condition observed after AtomicRelation.
	d.state.descriptor.ClearPrimaryKey(target)
	return deleted, nil
}

// DeleteInSession applies the sealed host relation policy inside the caller's
// transaction. It never opens, commits, retries or rolls back a transaction and
// never clears the caller's model key: the count is provisional until the outer
// owner confirms commit. Any returned error must leave the outer callback.
func (d RelationDeleter[M]) DeleteInSession(ctx context.Context, session db.RelationSession, target M) (int64, error) {
	if interfaceIsNil(ctx) {
		return 0, relationInvalidPlan("context is nil")
	}
	if interfaceIsNil(session) {
		return 0, relationBackendInvalidPlan("borrowed delete session is nil")
	}
	if _, ok := session.(db.SessionValidator); !ok {
		return 0, relationBackendInvalidPlan("borrowed delete requires session lifetime validation")
	}
	if err := validateQuerySession(ctx, session); err != nil {
		return 0, err
	}
	if err := d.state.validate(); err != nil {
		return 0, err
	}
	key, err := d.state.preflight(target)
	if err != nil {
		return 0, err
	}
	count, err := d.state.execute(ctx, session, key)
	if err != nil {
		return 0, err
	}
	if err := validateQuerySession(ctx, session); err != nil {
		return 0, err
	}
	return count, nil
}

func (state relationDeleteState[M]) validate() error {
	if !state.valid || interfaceIsNil(state.descriptor) || !immutableZeroStateValue(state.descriptor) {
		return relationInvalidPlan("relation deleter is unbound")
	}
	if _, ok := relationDeleteTargetKey(state.targetModel); !ok ||
		!reflect.DeepEqual(state.descriptor.Metadata().Clone(), state.targetModel) ||
		!reflect.DeepEqual(state.targetKey, mustRelationDeleteTargetKey(state.targetModel)) ||
		len(state.graph[state.target].incoming) == 0 ||
		!state.graph[state.target].model.Equal(state.targetModel) || !state.graph[state.target].key.Equal(state.targetKey) {
		return relationInvalidPlan("relation deleter state is invalid")
	}
	return nil
}

func (state relationDeleteState[M]) preflight(target M) (int64, error) {
	keyValue, present := state.descriptor.PrimaryKey(target)
	targetKey, ok := keyValue.Integer()
	if !present || !ok || keyValue.IsNull() {
		return 0, relationInvalidPlan("relation delete target must have a present non-NULL integer primary key")
	}

	snapshot := state.descriptor.CloneWriteModel(target)
	snapshotKey, snapshotPresent := state.descriptor.PrimaryKey(snapshot)
	if !snapshotPresent || !snapshotKey.Equal(keyValue) {
		return 0, relationInvalidPlan("relation delete snapshot primary key does not match the caller")
	}
	clearProbe := state.descriptor.CloneWriteModel(snapshot)
	state.descriptor.ClearPrimaryKey(&clearProbe)
	clearedKey, clearedPresent := state.descriptor.PrimaryKey(clearProbe)
	if clearedPresent || !clearedKey.Equal(query.Integer(0)) {
		return 0, relationInvalidPlan("relation delete descriptor did not clear primary key state canonically")
	}
	for _, field := range state.targetModel.Fields {
		if field.PrimaryKey {
			continue
		}
		before, beforeOK := state.descriptor.WriteFieldValue(snapshot, field.Clone())
		after, afterOK := state.descriptor.WriteFieldValue(clearProbe, field.Clone())
		if !beforeOK || !afterOK || !before.Equal(after) {
			return 0, relationInvalidPlan("relation delete primary key clear changed a non-primary field")
		}
	}
	return targetKey, nil
}

func runRelationAtomic(ctx context.Context, atomic func(context.Context, func(db.RelationSession) error) error, callback func(db.RelationSession) error) error {
	guard := &relationDeleteCallbackGuard{}
	atomicErr := atomic(ctx, func(session db.RelationSession) error {
		return guard.invoke(func() error { return callback(session) })
	})
	snapshot := guard.seal()
	if snapshot.entries == 0 && snapshot.completed == 0 && atomicErr != nil {
		return atomicErr
	}
	if snapshot.entries != 1 || snapshot.completed != 1 {
		return errors.Join(relationBackendInvalidPlan("relation atomic backend violated the single synchronous callback contract"), atomicErr, snapshot.result)
	}
	if snapshot.result != nil && (atomicErr == nil || !errors.Is(atomicErr, snapshot.result)) {
		return errors.Join(relationBackendInvalidPlan("relation atomic backend did not preserve its callback error"), atomicErr, snapshot.result)
	}
	return atomicErr
}

type relationDeleteCallbackGuard struct {
	mu        sync.Mutex
	sealed    bool
	entries   int
	completed int
	result    error
}

type relationDeleteCallbackSnapshot struct {
	entries   int
	completed int
	result    error
}

func (guard *relationDeleteCallbackGuard) invoke(callback func() error) error {
	guard.mu.Lock()
	if guard.sealed {
		guard.mu.Unlock()
		return relationBackendInvalidPlan("relation atomic callback was invoked after its outer call returned")
	}
	guard.entries++
	if guard.entries != 1 {
		guard.mu.Unlock()
		return relationBackendInvalidPlan("relation atomic callback was invoked more than once")
	}
	guard.mu.Unlock()

	result := callback()
	guard.mu.Lock()
	guard.completed++
	guard.result = result
	guard.mu.Unlock()
	return result
}

func (guard *relationDeleteCallbackGuard) seal() relationDeleteCallbackSnapshot {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	guard.sealed = true
	return relationDeleteCallbackSnapshot{
		entries:   guard.entries,
		completed: guard.completed,
		result:    guard.result,
	}
}

func relationDeleteTargetKey(model ir.Model) (ir.Field, bool) {
	primaryKey, ok := relationAutoPrimaryKey(model)
	if !ok {
		return ir.Field{}, false
	}
	for _, field := range model.Fields {
		if field.Kind == ir.FieldForeignKey {
			if field.PrimaryKey || field.Relation == nil || !field.Relation.Cardinality.SingleValued() {
				return ir.Field{}, false
			}
			// The sealed project validates this outgoing declaration. Deleting its
			// owner neither deletes nor updates the referenced target. The descriptor
			// preflight still proves that clearing the owner PK preserves this FK.
			continue
		}
		if field.Relation != nil {
			return ir.Field{}, false
		}
		switch field.Kind {
		case ir.FieldAuto:
			if !field.PrimaryKey || !reflect.DeepEqual(field, primaryKey) {
				return ir.Field{}, false
			}
		case ir.FieldChar, ir.FieldText, ir.FieldBoolean, ir.FieldInteger, ir.FieldDateTime, ir.FieldDate, ir.FieldTime, ir.FieldDuration, ir.FieldFloat, ir.FieldDecimal, ir.FieldUUID, ir.FieldJSON:
			if field.PrimaryKey {
				return ir.Field{}, false
			}
		default:
			return ir.Field{}, false
		}
	}
	return primaryKey, true
}

func mustRelationDeleteTargetKey(model ir.Model) ir.Field {
	primaryKey, _ := relationDeleteTargetKey(model)
	return primaryKey
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
