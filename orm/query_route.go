package orm

import (
	"reflect"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// A logical edge owns its canonical physical traversal. ManyToMany contributes
// two hops; their intermediary key is retained for correlated query semantics.
// Object and mutation handles keep their own single-declaration lifetimes.
type queryRelationStep struct {
	snapshot                       *projectBindingSnapshot
	sourceIdentity, targetIdentity ir.ModelIdentity
	sourceModel, targetModel       ir.Model
	accessor                       string
	hops                           []query.RelationHop
	keys                           []query.FieldRef
	presence                       ir.Field
	presenceScope                  query.RelationTerminalScope
}

type relationQueryRoute struct {
	steps []queryRelationStep
	err   error
}

func hasQueryRelation(snapshot *projectBindingSnapshot, identity ir.ModelIdentity, name string) bool {
	if snapshot == nil {
		return false
	}
	if hasReverseRelation(snapshot, identity, name) {
		return true
	}
	for _, binding := range snapshot.manyToMany {
		if binding.Source == identity && binding.Field == name || binding.Target == identity && !binding.Reverse.Disabled && binding.Reverse.Name == name {
			return true
		}
	}
	return false
}

func (route relationQueryRoute) last() queryRelationStep { return route.steps[len(route.steps)-1] }

// BindQueryRelation resolves forward, reverse and ManyToMany namespaces from
// one sealed project. Root and terminal Go types remain part of the capability.
func BindQueryRelation[S, T any](source BoundModel[S], accessor string, target BoundModel[T]) (QueryRelation[S, T], error) {
	if err := validateBoundModel(source); err != nil {
		return QueryRelation[S, T]{}, err
	}
	if err := validateBoundModel(target); err != nil {
		return QueryRelation[S, T]{}, err
	}
	if source.snapshot != target.snapshot {
		return QueryRelation[S, T]{}, relationInvalidPlan("query relation models belong to different project snapshots")
	}
	step, err := resolveQueryRelationStep(source.snapshot, source.identity, source.model, accessor)
	if err != nil {
		return QueryRelation[S, T]{}, err
	}
	if step.targetIdentity != target.identity || !reflect.DeepEqual(step.targetModel, target.model) {
		return QueryRelation[S, T]{}, relationInvalidPlan("query relation target disagrees with its bound model")
	}
	return QueryRelation[S, T]{route: relationQueryRoute{steps: []queryRelationStep{step}}}, nil
}

func resolveQueryRelationStep(snapshot *projectBindingSnapshot, identity ir.ModelIdentity, source ir.Model, accessor string) (queryRelationStep, error) {
	var zero queryRelationStep
	if snapshot == nil {
		return zero, relationInvalidPlan("query relation project is unbound")
	}
	if canonical, ok := snapshot.models[identity]; !ok || !reflect.DeepEqual(canonical, source) {
		return zero, relationInvalidPlan("query source disagrees with its project snapshot")
	}
	rootKey, ok := relationAutoPrimaryKey(source)
	if !ok {
		return zero, relationInvalidPlan("query relation source requires one AutoField primary key")
	}
	step := queryRelationStep{snapshot: snapshot, sourceIdentity: identity, sourceModel: source.Clone(), accessor: accessor, presenceScope: query.RelationTerminalRelatedField}
	if field, found := findField(source.Fields, accessor); found && field.Relation != nil {
		forward, err := resolveForwardRelationState(snapshot, identity, source, accessor)
		if err != nil {
			return zero, err
		}
		path, err := forward.path(fieldReference(forward.targetPrimaryKey))
		if err != nil {
			return zero, err
		}
		step.targetIdentity, step.targetModel = forward.metadata.Target, forward.targetModel.Clone()
		step.hops, step.keys = path.Hops(), []query.FieldRef{fieldReference(rootKey), fieldReference(forward.targetPrimaryKey)}
		step.presence, step.presenceScope = field.Clone(), query.RelationTerminalSourceKey
		return step, nil
	}
	for _, binding := range snapshot.manyToMany {
		reverse := binding.Target == identity && !binding.Reverse.Disabled && binding.Reverse.Name == accessor
		if !(binding.Source == identity && binding.Field == accessor) && !reverse {
			continue
		}
		targetIdentity := binding.Target
		sourceField, targetField := binding.Through.SourceField, binding.Through.TargetField
		if reverse {
			targetIdentity = binding.Source
			sourceField, targetField = targetField, sourceField
		}
		through, exists := snapshot.models[binding.Through.Model]
		if !exists {
			return zero, relationInvalidPlan("collection intermediary is missing")
		}
		left, err := resolveForwardRelationState(snapshot, binding.Through.Model, through, sourceField)
		if err != nil {
			return zero, err
		}
		right, err := resolveForwardRelationState(snapshot, binding.Through.Model, through, targetField)
		if err != nil {
			return zero, err
		}
		if left.metadata.Target != identity || right.metadata.Target != targetIdentity {
			return zero, relationInvalidPlan("collection physical endpoints disagree with its declaration")
		}
		throughKey, present := relationAutoPrimaryKey(through)
		if !present {
			return zero, relationInvalidPlan("collection intermediary requires an AutoField primary key")
		}
		physicalField, ok := findField(through.Fields, left.metadata.Field)
		if !ok {
			return zero, relationInvalidPlan("collection source field is missing")
		}
		first, err := query.NewReverseRelationPath(binding.Through.Model, through.DBTable, left.metadata.Field, left.metadata.Column, identity, source.DBTable, rootKey.Column, physicalReverseAccessor(physicalField), left.metadata.Nullable, fieldReference(throughKey), ir.RelationOneToMany)
		if err != nil {
			return zero, err
		}
		last, err := right.path(fieldReference(right.targetPrimaryKey))
		if err != nil {
			return zero, err
		}
		step.targetIdentity, step.targetModel = targetIdentity, right.targetModel.Clone()
		step.hops = append(first.Hops(), last.Hops()...)
		step.keys = []query.FieldRef{fieldReference(rootKey), fieldReference(throughKey), fieldReference(right.targetPrimaryKey)}
		step.presence = right.targetPrimaryKey.Clone()
		return step, nil
	}
	reverse, err := resolveReverseRelationState(snapshot, identity, source, accessor)
	if err != nil {
		return zero, err
	}
	targetKey, ok := relationAutoPrimaryKey(reverse.forward.sourceModel)
	if !ok {
		return zero, relationInvalidPlan("reverse query target requires an AutoField primary key")
	}
	path, err := reverse.path(fieldReference(targetKey))
	if err != nil {
		return zero, err
	}
	step.targetIdentity, step.targetModel = reverse.reverse.Target, reverse.forward.sourceModel.Clone()
	step.hops, step.keys = path.Hops(), []query.FieldRef{fieldReference(rootKey), fieldReference(targetKey)}
	step.presence = targetKey.Clone()
	return step, nil
}

// The physical join identity is shared with an explicit through model's named
// reverse route. A disabled reverse namespace gets a private stable field name;
// this does not publish an accessor or invent a second public declaration.
func physicalReverseAccessor(field ir.Field) string {
	if field.Relation != nil && !field.Relation.Reverse.Disabled {
		return field.Relation.Reverse.Name
	}
	return field.Name
}

func (route relationQueryRoute) validate() error {
	if route.err != nil {
		return route.err
	}
	if len(route.steps) == 0 || len(route.steps) > query.MaximumRelationHops {
		return relationInvalidPlan("query route requires between 1 and 64 declarations")
	}
	hops := 0
	for index, step := range route.steps {
		canonical, err := resolveQueryRelationStep(step.snapshot, step.sourceIdentity, step.sourceModel, step.accessor)
		if err != nil {
			return relationInvalidPlan("query route declaration cannot be reconstructed from its project snapshot")
		}
		if !reflect.DeepEqual(canonical, step) {
			return relationInvalidPlan("query route declaration disagrees with its project snapshot")
		}
		hops += len(step.hops)
		if hops > query.MaximumRelationHops {
			return relationInvalidPlan("query route exceeds 64 physical declarations")
		}
		if index > 0 {
			previous := route.steps[index-1]
			if previous.snapshot != step.snapshot || previous.targetIdentity != step.sourceIdentity || !reflect.DeepEqual(previous.targetModel, step.sourceModel) {
				return relationInvalidPlan("query route has disconnected project models")
			}
		}
	}
	return nil
}

func (route relationQueryRoute) path(terminal query.FieldRef, scope query.RelationTerminalScope) (query.RelationPath, error) {
	if err := route.validate(); err != nil {
		return query.RelationPath{}, err
	}
	var hops []query.RelationHop
	var keys []query.FieldRef
	for index, step := range route.steps {
		hops = append(hops, step.hops...)
		if index == 0 {
			keys = append(keys, step.keys[0])
		}
		keys = append(keys, step.keys[1:]...)
	}
	return query.NewRelationChain(hops, keys, terminal, scope)
}

func ChainRelations[S, T, U any](prefix QueryRelation[S, T], suffix QueryRelation[T, U]) QueryRelation[S, U] {
	fail := func(err error) QueryRelation[S, U] { return QueryRelation[S, U]{route: relationQueryRoute{err: err}} }
	if err := prefix.route.validate(); err != nil {
		return fail(err)
	}
	if err := suffix.route.validate(); err != nil {
		return fail(err)
	}
	steps := make([]queryRelationStep, 0, len(prefix.route.steps)+len(suffix.route.steps))
	steps = append(steps, prefix.route.steps...)
	steps = append(steps, suffix.route.steps...)
	result := QueryRelation[S, U]{route: relationQueryRoute{steps: steps}}
	if err := result.route.validate(); err != nil {
		return fail(err)
	}
	return result
}

func (relation QueryRelation[S, T]) WithConfigurationError(err error) QueryRelation[S, T] {
	if relation.route.err == nil {
		relation.route.err = err
	}
	return relation
}

func (relation QueryRelation[S, T]) IsNull(value bool) Predicate[S] {
	if err := relation.route.validate(); err != nil {
		return Predicate[S]{err: err}
	}
	last := relation.route.last()
	path, err := relation.route.path(fieldReference(last.presence), last.presenceScope)
	if err != nil {
		return Predicate[S]{err: err}
	}
	return predicateFromCondition[S](query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(value)), nil)
}
