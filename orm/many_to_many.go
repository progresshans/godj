package orm

import (
	"context"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ManyToManyInput is implemented by generated Create inputs. Endpoint fields
// are supplied by the bound relation, while the input owns payload/defaults.
// Its result is validated as a complete typed create before any write.
type ManyToManyInput[L any] interface {
	BuildManyToManyCreate(source, target ir.Field, sourceKey, targetKey int64) Mutation[L]
}

type ManyToManyDescriptor[L any] interface {
	WriteDescriptor[L]
	RelationObjectDescriptor[L]
	ManyToManyCreateInput() ManyToManyInput[L]
}

// ManyToMany is an immutable factory. Each From call owns a separate cache.
type ManyToMany[O, T, L any] struct {
	owner          PrimaryKeyObjectDescriptor[O]
	target         PrimaryKeyObjectDescriptor[T]
	through        ManyToManyDescriptor[L]
	state          *manyToManyState
	plan           query.Plan
	path           query.RelationPath
	prefetchSource BoundModel[L]
	prefetchTarget BoundModel[T]
	prefetchOwner  BoundModel[O]
	prefetchName   string
}

type manyToManyState struct {
	through             ir.ModelIdentity
	model               ir.Model
	key, source, target ir.Field
	unique              []query.FieldRef
	symmetrical         bool
	graph               map[ir.ModelIdentity]relationDeleteNode
}

// BindManyToMany seals the declaration, both endpoints, physical storage and
// the complete incoming delete policy in one project snapshot.
func BindManyToMany[O, T, L any](owner BoundModel[O], name string, target BoundModel[T], through BoundModel[L], expectedDeletePolicySHA256 string) (ManyToMany[O, T, L], error) {
	return bindManyToMany(owner, name, target, through, expectedDeletePolicySHA256, false)
}

func BindReverseManyToMany[O, T, L any](owner BoundModel[O], name string, target BoundModel[T], through BoundModel[L], expectedDeletePolicySHA256 string) (ManyToMany[O, T, L], error) {
	return bindManyToMany(owner, name, target, through, expectedDeletePolicySHA256, true)
}

func bindManyToMany[O, T, L any](owner BoundModel[O], name string, target BoundModel[T], through BoundModel[L], fingerprint string, reverse bool) (ManyToMany[O, T, L], error) {
	var zero ManyToMany[O, T, L]
	if err := validateObjectBoundModel(owner); err != nil {
		return zero, err
	}
	if err := validateObjectBoundModel(target); err != nil {
		return zero, err
	}
	if err := validateObjectBoundModel(through); err != nil {
		return zero, err
	}
	if owner.snapshot != target.snapshot || owner.snapshot != through.snapshot {
		return zero, relationInvalidPlan("collection models belong to different project snapshots")
	}
	ownerDescriptor, ownerOK := owner.objectDescriptor.(PrimaryKeyObjectDescriptor[O])
	targetDescriptor, targetOK := target.objectDescriptor.(PrimaryKeyObjectDescriptor[T])
	throughDescriptor, throughOK := through.objectDescriptor.(ManyToManyDescriptor[L])
	if !ownerOK || !targetOK || !throughOK {
		return zero, relationInvalidPlan("collection descriptors lack presence-aware endpoint or through-create capabilities")
	}
	var binding *ir.ManyToManyBinding
	for _, candidate := range owner.snapshot.manyToMany {
		matches := !reverse && candidate.Source == owner.identity && candidate.Target == target.identity && candidate.Field == name
		matches = matches || reverse && candidate.Target == owner.identity && candidate.Source == target.identity && !candidate.Reverse.Disabled && candidate.Reverse.Name == name
		if matches {
			copy := candidate
			binding = &copy
			break
		}
	}
	if binding == nil || binding.Through.Model != through.identity {
		return zero, relationInvalidPlan("collection declaration does not match the bound endpoint and through models")
	}
	key, keyOK := relationDeleteTargetKey(through.model)
	_, ownerKeyOK := relationAutoPrimaryKey(owner.model)
	targetKey, targetKeyOK := relationAutoPrimaryKey(target.model)
	if !keyOK || !ownerKeyOK || !targetKeyOK {
		return zero, relationInvalidPlan("collection models require exactly one AutoField primary key")
	}
	sourceField, sourceOK := findField(through.model.Fields, binding.Through.SourceField)
	targetField, fieldOK := findField(through.model.Fields, binding.Through.TargetField)
	if !sourceOK || !fieldOK {
		return zero, relationInvalidPlan("collection through fields are missing")
	}
	if reverse {
		sourceField, targetField = targetField, sourceField
	}
	graph, err := relationDeleteGraph(owner.snapshot, through.identity)
	if err != nil {
		return zero, err
	}
	if !validLowerSHA256(fingerprint) || fingerprint != relationDeletePolicyFingerprint(through.identity, through.model, key, graph) {
		return zero, relationInvalidPlan("collection delete policy fingerprint does not match project binding")
	}
	state := &manyToManyState{through: through.identity, model: through.model.Clone(), key: key, source: sourceField, target: targetField, symmetrical: binding.Symmetry == ir.ManyToManySymmetrical, graph: graph}
	for _, constraint := range through.model.UniqueConstraints {
		if len(constraint.Fields) == 2 && (constraint.Fields[0] == sourceField.Name && constraint.Fields[1] == targetField.Name || constraint.Fields[1] == sourceField.Name && constraint.Fields[0] == targetField.Name) {
			state.unique = []query.FieldRef{fieldReference(sourceField), fieldReference(targetField)}
			break
		}
	}
	// This physical join is private to the collection. It does not expose or
	// invent a public reverse accessor on either selected through ForeignKey.
	path, err := query.NewReverseRelationPath(through.identity, through.model.DBTable, targetField.Name, targetField.Column, target.identity, target.model.DBTable, targetKey.Column, physicalReverseAccessor(targetField), targetField.Nullable, fieldReference(sourceField), ir.RelationOneToMany)
	if err != nil {
		return zero, err
	}
	plan := target.objectPlan
	return ManyToMany[O, T, L]{owner: ownerDescriptor, target: targetDescriptor, through: throughDescriptor, state: state, plan: plan, path: path, prefetchSource: through, prefetchTarget: target, prefetchOwner: owner, prefetchName: name}, nil
}

// ManyCollection owns a lazy target QuerySet. A mutation invalidates this
// handle before and after the attempt, including failures and unknown outcomes.
// Previously returned QuerySets and independently materialized owners retain
// their own snapshots. A dereference-copy of the handle is invalid.
type ManyCollection[T, L any] struct {
	mu       sync.Mutex
	querySet QuerySet[T]
	basePlan query.Plan
	target   PrimaryKeyObjectDescriptor[T]
	through  ManyToManyDescriptor[L]
	state    *manyToManyState
	backend  db.Queryer
	ownerKey int64
	_self    *ManyCollection[T, L]
}

func (r ManyToMany[O, T, L]) From(backend db.Queryer, owner O) (*ManyCollection[T, L], error) {
	if _, borrowed := backend.(db.SessionValidator); borrowed {
		return nil, relationInvalidPlan("use InSession to bind a borrowed collection session")
	}
	return r.from(backend, owner)
}

// InSession joins the supplied transaction and never begins, commits, rolls
// back or retries one. Reads accept an ordinary session; mutations require a
// RelationSession. Successful mutations remain provisional. Return any
// error from the enclosing transaction callback; this is not a savepoint.
// The independent handle and its query caches expire with the session.
func (r ManyToMany[O, T, L]) InSession(session db.Session, owner O) (*ManyCollection[T, L], error) {
	if interfaceIsNil(session) {
		return nil, relationBackendInvalidPlan("collection session is nil")
	}
	if _, ok := session.(db.SessionValidator); !ok {
		return nil, relationBackendInvalidPlan("borrowed collection requires session lifetime validation")
	}
	if err := validateQuerySession(context.Background(), session); err != nil {
		return nil, err
	}
	return r.from(session, owner)
}

func (r ManyToMany[O, T, L]) from(backend db.Queryer, owner O) (*ManyCollection[T, L], error) {
	if r.state == nil || interfaceIsNil(backend) {
		return nil, relationInvalidPlan("collection is unbound or backend is nil")
	}
	key, err := manyObjectKey(r.owner, owner)
	if err != nil {
		return nil, err
	}
	expression, err := query.NewExpression(query.NewRelatedCondition(r.path, query.LookupExact, query.Integer(key)))
	if err != nil {
		return nil, err
	}
	plan, err := r.plan.WithWhere(expression)
	if err != nil {
		return nil, err
	}
	plan, err = plan.ReuseNextCollectionFilter()
	if err != nil {
		return nil, err
	}
	collection := &ManyCollection[T, L]{querySet: newQuerySet(backend, r.target, plan), basePlan: plan, target: r.target, through: r.through, state: r.state, backend: backend, ownerKey: key}
	collection._self = collection
	return collection, nil
}

func manyObjectKey[M any](descriptor PrimaryKeyObjectDescriptor[M], value M) (int64, error) {
	key, present := descriptor.PrimaryKey(value)
	integer, valid := key.Integer()
	if !present || !valid || key.IsNull() {
		return 0, &query.Error{Category: query.CategoryQuery, Code: query.CodeMissingPrimaryKey, Detail: "collection endpoint has no present integer primary key"}
	}
	copyKey, copyPresent := descriptor.PrimaryKey(descriptor.CloneModel(value))
	if !copyPresent || !copyKey.Equal(key) {
		return 0, relationInvalidPlan("collection endpoint clone changed primary key state")
	}
	return integer, nil
}

func (c *ManyCollection[T, L]) validate() error {
	if c == nil || c._self != c || c.state == nil {
		return relationInvalidPlan("collection is nil, zero, or copied")
	}
	return nil
}

func (c *ManyCollection[T, L]) Query() (QuerySet[T], error) {
	if err := c.validate(); err != nil {
		return QuerySet[T]{}, err
	}
	if err := validateQuerySession(context.Background(), c.backend); err != nil {
		return QuerySet[T]{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.querySet, nil
}

func (c *ManyCollection[T, L]) All(ctx context.Context) ([]T, error) {
	set, err := c.Query()
	if err != nil {
		return nil, err
	}
	if interfaceIsNil(ctx) {
		return nil, relationInvalidPlan("context is nil")
	}
	return set.All(ctx)
}

func (c *ManyCollection[T, L]) Fresh() (*ManyCollection[T, L], error) {
	set, err := c.Query()
	if err != nil {
		return nil, err
	}
	set = newQuerySet[T](c.backend, c.target, c.basePlan)
	result := &ManyCollection[T, L]{querySet: set, basePlan: c.basePlan, target: c.target, through: c.through, state: c.state, backend: c.backend, ownerKey: c.ownerKey}
	result._self = result
	return result, nil
}

func (c *ManyCollection[T, L]) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.querySet = newQuerySet[T](c.backend, c.target, c.basePlan)
}

// Invalidate discards only this handle's evaluation cache. It performs no I/O;
// held QuerySets and independent materializations keep their own snapshots.
func (c *ManyCollection[T, L]) Invalidate() error {
	if err := c.validate(); err != nil {
		return err
	}
	c.invalidate()
	return nil
}

// ManyCollectionCache belongs to one generated owner materialization. Binding
// is serialized and does no I/O; failed binding leaves the cell empty.
type ManyCollectionCache[T, L any] struct {
	mu    sync.Mutex
	value *ManyCollection[T, L]
}

func (c *ManyCollectionCache[T, L]) Get(bind func() (*ManyCollection[T, L], error)) (*ManyCollection[T, L], error) {
	if c == nil || bind == nil {
		return nil, relationInvalidPlan("collection cache or binder is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil {
		return c.value, nil
	}
	value, err := bind()
	if err != nil {
		return nil, err
	}
	if err := value.validate(); err != nil {
		return nil, err
	}
	c.value = value
	return value, nil
}
