package orm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const relationDeletePolicyFingerprintVersion = "godj-relation-delete-policy-v1"

// RelationDeleter is one immutable project-bound low-level delete capability.
// It owns the complete incoming ForeignKey policy for one target model.
type RelationDeleter[M any] struct {
	state  relationDeleteState[M]
	marker [0]func(M)
}

type relationDeleteState[M any] struct {
	descriptor  WriteDescriptor[M]
	target      ir.ModelIdentity
	targetModel ir.Model
	targetKey   ir.Field
	protect     []relationDeleteEdge
	setNull     []relationDeleteEdge
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
	expectedIncomingPolicySHA256 string,
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
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete target must contain only scalar fields and exactly one AutoField primary key")
	}

	edges, err := relationDeleteIncomingEdges(binding.snapshot, identity)
	if err != nil {
		return RelationDeleter[M]{}, err
	}
	if len(edges) == 0 {
		return RelationDeleter[M]{}, relationInvalidPlan("relation delete target has no supported incoming ForeignKey")
	}
	if !validLowerSHA256(expectedIncomingPolicySHA256) {
		return RelationDeleter[M]{}, relationInvalidPlan("expected incoming relation policy fingerprint must be lowercase SHA-256")
	}
	actualFingerprint := relationDeletePolicyFingerprint(identity, targetModel, targetKey, edges)
	if actualFingerprint != expectedIncomingPolicySHA256 {
		return RelationDeleter[M]{}, relationInvalidPlan("incoming relation policy fingerprint does not match project binding")
	}

	state := relationDeleteState[M]{
		descriptor:  descriptor,
		target:      identity,
		targetModel: targetModel.Clone(),
		targetKey:   targetKey.Clone(),
		valid:       true,
	}
	for _, edge := range edges {
		cloned := cloneRelationDeleteEdge(edge)
		switch edge.metadata.OnDelete {
		case ir.DeleteProtect:
			state.protect = append(state.protect, cloned)
		case ir.DeleteSetNull:
			state.setNull = append(state.setNull, cloned)
		}
	}
	return RelationDeleter[M]{state: state}, nil
}

// Delete scans every PROTECT edge before mutation, then executes canonical
// SET_NULL updates followed by one exact-key target delete in AtomicRelation.
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
	protectPlans, setNullPlans, deletePlan := d.state.plans(targetKey)
	guard := &relationDeleteCallbackGuard{}
	callback := func(session db.RelationSession) error {
		return guard.invoke(func() error {
			return d.state.execute(ctx, session, targetKey, protectPlans, setNullPlans, deletePlan)
		})
	}

	atomicErr := backend.AtomicRelation(ctx, callback)
	guardSnapshot := guard.seal()
	if guardSnapshot.entries == 0 && guardSnapshot.completed == 0 && atomicErr != nil {
		return 0, atomicErr
	}
	if guardSnapshot.entries != 1 || guardSnapshot.completed != 1 {
		return 0, errors.Join(
			relationBackendInvalidPlan("relation atomic backend violated the single synchronous callback contract"),
			atomicErr,
			guardSnapshot.result,
		)
	}
	if guardSnapshot.result != nil {
		if atomicErr == nil || !errors.Is(atomicErr, guardSnapshot.result) {
			return 0, errors.Join(
				relationBackendInvalidPlan("relation atomic backend did not preserve its callback error"),
				atomicErr,
				guardSnapshot.result,
			)
		}
		return 0, atomicErr
	}
	if atomicErr != nil {
		return 0, atomicErr
	}

	// Successful commit is authoritative. Do not downgrade it for a context
	// transition or connection-return condition observed after AtomicRelation.
	d.state.descriptor.ClearPrimaryKey(target)
	return 1, nil
}

func (state relationDeleteState[M]) validate() error {
	if !state.valid || interfaceIsNil(state.descriptor) || !immutableZeroStateValue(state.descriptor) {
		return relationInvalidPlan("relation deleter is unbound")
	}
	if _, ok := relationDeleteTargetKey(state.targetModel); !ok ||
		!reflect.DeepEqual(state.descriptor.Metadata().Clone(), state.targetModel) ||
		!reflect.DeepEqual(state.targetKey, mustRelationDeleteTargetKey(state.targetModel)) ||
		len(state.protect)+len(state.setNull) == 0 {
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

func (state relationDeleteState[M]) plans(targetKey int64) ([]query.Plan, []query.RelationSetNullPlan, query.DeletePlan) {
	keyValue := query.Integer(targetKey)
	protect := make([]query.Plan, len(state.protect))
	for index, edge := range state.protect {
		foreignKey := fieldReference(edge.sourceForeignKey)
		protect[index] = query.NewPlan(
			edge.sourceModel.DBTable,
			[]query.FieldRef{fieldReference(edge.sourcePrimaryKey), foreignKey},
		).WithConditions(query.NewCondition(foreignKey, query.LookupExact, keyValue))
	}
	setNull := make([]query.RelationSetNullPlan, len(state.setNull))
	for index, edge := range state.setNull {
		setNull[index] = query.NewRelationSetNullPlan(
			edge.sourceModel.DBTable,
			fieldReference(edge.sourceForeignKey),
			keyValue,
		)
	}
	return protect, setNull, query.NewDeletePlan(
		state.targetModel.DBTable,
		fieldReference(state.targetKey),
		keyValue,
	)
}

func (state relationDeleteState[M]) execute(
	ctx context.Context,
	session db.RelationSession,
	targetKey int64,
	protectPlans []query.Plan,
	setNullPlans []query.RelationSetNullPlan,
	deletePlan query.DeletePlan,
) error {
	if interfaceIsNil(session) {
		return relationBackendInvalidPlan("relation atomic backend supplied a nil session")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	protected := make(map[relationDeleteProtectedSource]struct{})
	for index, plan := range protectPlans {
		if err := collectProtectedRelationRows(ctx, session, state.protect[index], plan, targetKey, protected); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(protected) != 0 {
		err, constructorErr := query.NewProtectedForeignKeyError(int64(len(protected)))
		if constructorErr != nil {
			return constructorErr
		}
		return err
	}

	for _, plan := range setNullPlans {
		if err := ctx.Err(); err != nil {
			return err
		}
		rowsAffected, err := session.RelationSetNull(ctx, plan)
		if err != nil {
			return err
		}
		if rowsAffected < 0 {
			return relationBackendInvalidPlan("relation SET_NULL returned a negative affected-row count")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rowsAffected, err := session.Delete(ctx, deletePlan)
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return unexpectedRows("relation delete target", rowsAffected)
	}
	return ctx.Err()
}

type relationDeleteProtectedSource struct {
	source     ir.ModelIdentity
	primaryKey int64
}

func collectProtectedRelationRows(
	ctx context.Context,
	session db.RelationSession,
	edge relationDeleteEdge,
	plan query.Plan,
	targetKey int64,
	protected map[relationDeleteProtectedSource]struct{},
) error {
	rows, err := session.Query(ctx, plan)
	if err != nil {
		if !interfaceIsNil(rows) {
			if closeErr := rows.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close protected relation rows returned with backend error: %w", closeErr))
			}
		}
		return joinContextErr(err, ctx)
	}
	if interfaceIsNil(rows) {
		return joinContextErr(relationBackendInvalidPlan("relation session returned nil protected rows without an error"), ctx)
	}

	for rows.Next() {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
			break
		}
		var sourcePrimaryKey any
		var sourceForeignKey any
		if scanErr := rows.Scan(&sourcePrimaryKey, &sourceForeignKey); scanErr != nil {
			err = fmt.Errorf("scan protected relation row: %w", scanErr)
			break
		}
		primaryKey, primaryOK := sourcePrimaryKey.(int64)
		foreignKey, foreignOK := sourceForeignKey.(int64)
		if !primaryOK || !foreignOK || foreignKey != targetKey {
			err = relationBackendInvalidPlan("protected relation row did not contain the exact integer primary and target keys")
			break
		}
		protected[relationDeleteProtectedSource{source: edge.metadata.Source, primaryKey: primaryKey}] = struct{}{}
	}
	return finishRowsLifecycle(ctx, err, rows)
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
		if field.Relation != nil || field.Kind == ir.FieldForeignKey {
			return ir.Field{}, false
		}
		switch field.Kind {
		case ir.FieldAuto:
			if !field.PrimaryKey || !reflect.DeepEqual(field, primaryKey) {
				return ir.Field{}, false
			}
		case ir.FieldChar, ir.FieldBoolean:
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

func relationDeleteIncomingEdges(snapshot *projectBindingSnapshot, target ir.ModelIdentity) ([]relationDeleteEdge, error) {
	edges := make([]relationDeleteEdge, 0)
	for _, metadata := range snapshot.forward {
		if metadata.Target != target {
			continue
		}
		if metadata.Cardinality != ir.RelationManyToOne ||
			(metadata.OnDelete != ir.DeleteProtect && metadata.OnDelete != ir.DeleteSetNull) ||
			(metadata.OnDelete == ir.DeleteSetNull && !metadata.Nullable) {
			return nil, relationInvalidPlan("incoming relation uses an unsupported cardinality, policy, or nullability")
		}
		sourceModel, ok := snapshot.models[metadata.Source]
		if !ok {
			return nil, relationInvalidPlan("incoming relation source model is missing from project binding")
		}
		sourcePrimaryKey, ok := relationAutoPrimaryKey(sourceModel)
		if !ok {
			return nil, relationInvalidPlan("incoming relation source must have exactly one AutoField primary key")
		}
		sourceForeignKey, ok := findField(sourceModel.Fields, metadata.Field)
		if !ok || sourceForeignKey.Kind != ir.FieldForeignKey || sourceForeignKey.PrimaryKey ||
			sourceForeignKey.Column != metadata.Column || sourceForeignKey.Nullable != metadata.Nullable ||
			sourceForeignKey.Relation == nil || sourceForeignKey.Relation.Target != metadata.Target ||
			sourceForeignKey.Relation.Cardinality != metadata.Cardinality ||
			sourceForeignKey.Relation.Reverse != metadata.Reverse ||
			sourceForeignKey.Relation.OnDelete != metadata.OnDelete {
			return nil, relationInvalidPlan("incoming relation metadata disagrees with its source ForeignKey")
		}
		edges = append(edges, relationDeleteEdge{
			metadata:         metadata,
			sourceModel:      sourceModel.Clone(),
			sourcePrimaryKey: sourcePrimaryKey.Clone(),
			sourceForeignKey: sourceForeignKey.Clone(),
		})
	}
	sort.Slice(edges, func(left, right int) bool {
		return compareForward(edges[left].metadata, edges[right].metadata) < 0
	})
	return edges, nil
}

func cloneRelationDeleteEdge(edge relationDeleteEdge) relationDeleteEdge {
	return relationDeleteEdge{
		metadata:         edge.metadata,
		sourceModel:      edge.sourceModel.Clone(),
		sourcePrimaryKey: edge.sourcePrimaryKey.Clone(),
		sourceForeignKey: edge.sourceForeignKey.Clone(),
	}
}

func relationDeletePolicyFingerprint(
	target ir.ModelIdentity,
	targetModel ir.Model,
	targetPrimaryKey ir.Field,
	edges []relationDeleteEdge,
) string {
	hash := sha256.New()
	writeRelationDeleteFingerprintValue(hash, relationDeletePolicyFingerprintVersion)
	writeRelationDeleteFingerprintValue(hash, target.AppLabel)
	writeRelationDeleteFingerprintValue(hash, target.ModelName)
	writeRelationDeleteFingerprintValue(hash, targetModel.DBTable)
	writeRelationDeleteFingerprintValue(hash, targetPrimaryKey.Name)
	writeRelationDeleteFingerprintValue(hash, targetPrimaryKey.Column)
	writeRelationDeleteFingerprintValue(hash, strconv.FormatUint(uint64(len(edges)), 10))
	for _, edge := range edges {
		nullable := "0"
		if edge.metadata.Nullable {
			nullable = "1"
		}
		for _, value := range []string{
			edge.metadata.Source.AppLabel,
			edge.metadata.Source.ModelName,
			edge.sourceModel.DBTable,
			edge.sourcePrimaryKey.Name,
			edge.sourcePrimaryKey.Column,
			edge.sourceForeignKey.Name,
			edge.sourceForeignKey.Column,
			nullable,
			string(edge.metadata.Cardinality),
			string(edge.metadata.OnDelete),
		} {
			writeRelationDeleteFingerprintValue(hash, value)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type relationDeleteFingerprintWriter interface {
	Write([]byte) (int, error)
}

func writeRelationDeleteFingerprintValue(writer relationDeleteFingerprintWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}

func validLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
