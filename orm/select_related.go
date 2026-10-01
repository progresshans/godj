package orm

import (
	"reflect"
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ProjectionPresence is the sealed row-shape result reported by an additive
// generated projection scanner. The zero value is deliberately invalid.
type ProjectionPresence uint8

const (
	ProjectionInvalid ProjectionPresence = iota
	ProjectionAbsent  ProjectionPresence = 1
	ProjectionPresent ProjectionPresence = 2
)

// ProjectionDescriptor is the additive joined-row scanning capability. It is
// implemented by generated companions on the same named descriptor used by
// ordinary root-only QuerySets.
type ProjectionDescriptor[M any] interface {
	ModelDescriptor[M]
	NewProjectionScan() ProjectionScan[M]
}

// ProjectionScan owns fresh per-row destinations and decodes their complete
// state after the runtime has called Row.Scan exactly once.
type ProjectionScan[M any] interface {
	Destinations() []any
	Decode() (model M, primaryKey query.Value, presence ProjectionPresence)
}

// RelatedSelectPath is one immutable, project-resolved single-valued eager
// path. Its zero value is invalid. Source and target follow traversal direction.
type RelatedSelectPath[S any] struct{ state relatedSelectPathState[S] }
type relatedSelectPathState[S any] struct {
	source           BoundModel[S]
	sourceDescriptor ProjectionDescriptor[S]
	targetIdentity   ir.ModelIdentity
	targetModel      ir.Model
	sourceKey        ir.Field // the physical FK, regardless of traversal direction
	projection       query.RelationProjection
	path             string
	valid            bool
}

// ResolveRelatedSelectPath resolves one case-sensitive forward or reverse
// OneToOne accessor. Collections and multi-hop names fail before any I/O.
func ResolveRelatedSelectPath[S any](source BoundModel[S], path string) (RelatedSelectPath[S], error) {
	if err := validateObjectBoundModel(source); err != nil {
		return RelatedSelectPath[S]{}, err
	}
	if path == "" || strings.TrimSpace(path) == "" || strings.Contains(path, "__") {
		return RelatedSelectPath[S]{}, invalidRelatedPath(path)
	}
	descriptor, err := projectionDescriptorFor(source)
	if err != nil {
		return RelatedSelectPath[S]{}, err
	}
	state := relatedSelectPathState[S]{source: source, sourceDescriptor: descriptor, path: path}
	var route query.RelationPath
	var targetKey ir.Field
	if metadata, ok := (ProjectBinding{snapshot: source.snapshot}).Relation(source.identity, path); ok && metadata.Cardinality.SingleValued() {
		relation, err := resolveForwardRelationState(source.snapshot, source.identity, source.model, path)
		if err != nil {
			return RelatedSelectPath[S]{}, err
		}
		state.sourceKey = mustFindField(relation.sourceModel.Fields, path).Clone()
		state.targetIdentity = relation.metadata.Target
		state.targetModel = relation.targetModel.Clone()
		targetKey = relation.targetPrimaryKey
		route, err = query.NewForwardRelationPath(source.identity, source.model.DBTable, path, state.sourceKey.Column, state.targetIdentity, state.targetModel.DBTable, targetKey.Column, state.sourceKey.Nullable, fieldReference(targetKey), metadata.Cardinality)
		if err != nil {
			return RelatedSelectPath[S]{}, err
		}
	} else {
		reverse, ok := findReverseRelation(source.snapshot, source.identity, path)
		if !ok || reverse.Cardinality != ir.RelationOneToOne {
			return RelatedSelectPath[S]{}, invalidRelatedPath(path)
		}
		relation, err := resolveReverseRelationState(source.snapshot, source.identity, source.model, path)
		if err != nil {
			return RelatedSelectPath[S]{}, err
		}
		state.sourceKey = mustFindField(relation.forward.sourceModel.Fields, relation.reverse.SourceField).Clone()
		state.targetIdentity = relation.reverse.Target
		state.targetModel = relation.forward.sourceModel.Clone()
		targetKey, ok = relationAutoPrimaryKey(state.targetModel)
		if !ok {
			return RelatedSelectPath[S]{}, relationInvalidPlan("selected reverse target has no AutoField primary key")
		}
		route, err = query.NewReverseRelationPath(state.targetIdentity, state.targetModel.DBTable, state.sourceKey.Name, state.sourceKey.Column, source.identity, source.model.DBTable, relation.forward.targetPrimaryKey.Column, path, state.sourceKey.Nullable, fieldReference(targetKey), ir.RelationOneToOne)
		if err != nil {
			return RelatedSelectPath[S]{}, err
		}
	}
	state.projection, err = query.NewRelationProjection(route.Hops(), fieldReference(targetKey), modelFieldReferences(state.targetModel))
	if err != nil {
		return RelatedSelectPath[S]{}, err
	}
	state.valid = true
	return RelatedSelectPath[S]{state: state}, nil
}

// RelatedSelect retains concrete source/target types for either direction;
// every selected tree uses the same row scanner and cache publication path.
type RelatedSelect[S, T any] struct {
	state            relatedSelectState[S, T]
	children         []RelatedSelection[T]
	configurationErr error
}
type relatedSelectState[S, T any] struct {
	path             relatedSelectPathState[S]
	source           BoundModel[S]
	target           BoundModel[T]
	sourceStorage    RelationStorage[S]
	targetStorage    RelationStorage[T]
	targetKey        ir.Field
	sourceDescriptor ProjectionDescriptor[S]
	targetDescriptor ProjectionDescriptor[T]
	valid            bool
}

func BindRequiredForwardSelect[S, T any](path RelatedSelectPath[S], relation RequiredForwardObject[S, T]) (RelatedSelect[S, T], error) {
	return bindForwardSelect(path.state, relation.state, false)
}
func BindNullableForwardSelect[S, T any](path RelatedSelectPath[S], relation NullableForwardObject[S, T]) (RelatedSelect[S, T], error) {
	return bindForwardSelect(path.state, relation.state, true)
}
func bindForwardSelect[S, T any](path relatedSelectPathState[S], relation forwardObjectState[S, T], nullable bool) (RelatedSelect[S, T], error) {
	if !relation.valid || relation.nullable != nullable || path.projection.TerminalHop().Direction() != query.RelationForward {
		return RelatedSelect[S, T]{}, relationInvalidPlan("forward selection and object handle disagree")
	}
	return bindRelatedSelect(path, relation.source, relation.target, relation.storage, nil)
}

// BindReverseOneToOneSelect uses the physical child FK to validate membership.
// The traversal is always optional, including required child ForeignKeys.
func BindReverseOneToOneSelect[S, T any](path RelatedSelectPath[S], relation ReverseOneToOneObject[S, T]) (RelatedSelect[S, T], error) {
	if err := relation.state.validate(); err != nil {
		return RelatedSelect[S, T]{}, err
	}
	if path.state.projection.TerminalHop().Direction() != query.RelationReverse || path.state.path != relation.state.sourceForeignKey.Relation.Reverse.Name {
		return RelatedSelect[S, T]{}, relationInvalidPlan("reverse selection and object handle disagree")
	}
	storage, ok := relation.state.source.objectDescriptor.BindRelationStorage(relation.state.sourceForeignKey.Clone())
	if !ok {
		return RelatedSelect[S, T]{}, relationInvalidPlan("reverse selection child FK storage is unavailable")
	}
	return bindRelatedSelect(path.state, relation.state.owner, relation.state.source, nil, storage)
}

func bindRelatedSelect[S, T any](path relatedSelectPathState[S], source BoundModel[S], target BoundModel[T], sourceStorage RelationStorage[S], targetStorage RelationStorage[T]) (RelatedSelect[S, T], error) {
	if err := validateRelatedSelectPath(path); err != nil {
		return RelatedSelect[S, T]{}, err
	}
	sourceDescriptor, err := projectionDescriptorFor(source)
	if err != nil {
		return RelatedSelect[S, T]{}, err
	}
	targetDescriptor, err := projectionDescriptorFor(target)
	if err != nil {
		return RelatedSelect[S, T]{}, err
	}
	targetKey, ok := relationAutoPrimaryKey(target.model)
	if !ok {
		return RelatedSelect[S, T]{}, relationInvalidPlan("selected target has no AutoField primary key")
	}
	state := relatedSelectState[S, T]{path: path, source: source, target: target, sourceStorage: sourceStorage, targetStorage: targetStorage, targetKey: targetKey, sourceDescriptor: sourceDescriptor, targetDescriptor: targetDescriptor, valid: true}
	if err := validateRelatedSelectState(state); err != nil {
		return RelatedSelect[S, T]{}, err
	}
	return RelatedSelect[S, T]{state: state}, nil
}

func projectionDescriptorFor[M any](model BoundModel[M]) (ProjectionDescriptor[M], error) {
	if err := validateObjectBoundModel(model); err != nil {
		return nil, err
	}
	descriptor, ok := model.objectDescriptor.(ProjectionDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return nil, relationInvalidPlan("bound model does not provide a projection descriptor")
	}
	if !immutableZeroStateValue(descriptor) || !reflect.DeepEqual(descriptor.Metadata(), model.model) {
		return nil, relationInvalidPlan("projection descriptor is mutable or disagrees with the project model")
	}
	return descriptor, nil
}

func validateRelatedSelectPath[S any](state relatedSelectPathState[S]) error {
	if !state.valid {
		return relationInvalidPlan("related select path is unbound")
	}
	expected, err := ResolveRelatedSelectPath(state.source, state.path)
	if err != nil {
		return err
	}
	if !expected.state.projection.Equal(state.projection) || expected.state.targetIdentity != state.targetIdentity || !reflect.DeepEqual(expected.state.targetModel, state.targetModel) || !reflect.DeepEqual(expected.state.sourceKey, state.sourceKey) || reflect.TypeOf(expected.state.sourceDescriptor) != reflect.TypeOf(state.sourceDescriptor) {
		return relationInvalidPlan("related select path changed after resolution")
	}
	return nil
}
func mustFindField(fields []ir.Field, name string) ir.Field {
	field, _ := findField(fields, name)
	return field
}
func validateRelatedSelectState[S, T any](state relatedSelectState[S, T]) error {
	if !state.valid {
		return relationInvalidPlan("related select is unbound")
	}
	if err := validateRelatedSelectPath(state.path); err != nil {
		return err
	}
	sourceDescriptor, err := projectionDescriptorFor(state.source)
	if err != nil {
		return err
	}
	targetDescriptor, err := projectionDescriptorFor(state.target)
	if err != nil {
		return err
	}
	if state.source.snapshot != state.path.source.snapshot || state.source.identity != state.path.source.identity || state.source.snapshot != state.target.snapshot || state.target.identity != state.path.targetIdentity || !reflect.DeepEqual(state.source.model, state.path.source.model) || !reflect.DeepEqual(state.target.model, state.path.targetModel) || reflect.TypeOf(sourceDescriptor) != reflect.TypeOf(state.sourceDescriptor) || reflect.TypeOf(sourceDescriptor) != reflect.TypeOf(state.path.sourceDescriptor) || reflect.TypeOf(targetDescriptor) != reflect.TypeOf(state.targetDescriptor) {
		return relationInvalidPlan("related select models or descriptors changed after binding")
	}
	key, ok := relationAutoPrimaryKey(state.target.model)
	if !ok || !reflect.DeepEqual(key, state.targetKey) {
		return relationInvalidPlan("selected target primary key changed")
	}
	if state.path.projection.TerminalHop().Direction() == query.RelationForward {
		if !interfaceIsNil(state.targetStorage) || interfaceIsNil(state.sourceStorage) || !immutableZeroStateValue(state.sourceStorage) || !reflect.DeepEqual(state.sourceStorage.Field(), state.path.sourceKey) {
			return relationInvalidPlan("selected forward storage is unavailable, mutable, or changed")
		}
	} else {
		if !interfaceIsNil(state.sourceStorage) || interfaceIsNil(state.targetStorage) || !immutableZeroStateValue(state.targetStorage) || !reflect.DeepEqual(state.targetStorage.Field(), state.path.sourceKey) {
			return relationInvalidPlan("selected reverse storage is unavailable, mutable, or changed")
		}
		if _, ok := state.source.objectDescriptor.(PrimaryKeyObjectDescriptor[S]); !ok {
			return relationInvalidPlan("selected reverse owner has no primary key descriptor")
		}
	}
	return nil
}

func invalidRelatedPath(path string) *query.Error {
	return &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeInvalidRelatedPath,
		Field:    path,
		Detail:   "path is not one direct single-valued relation",
	}
}

func relatedObjectProjectionError(field ir.Field, detail string) *query.Error {
	return &query.Error{
		Category: query.CategoryIntegrity,
		Code:     query.CodeRelatedObjectProjection,
		Field:    field.Name,
		Detail:   detail,
	}
}
