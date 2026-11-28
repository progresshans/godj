package orm

import (
	"reflect"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type reverseRelationState struct {
	snapshot *projectBindingSnapshot
	owner    ir.ModelIdentity
	reverse  ReverseRelationMetadata
	forward  forwardRelationState
	valid    bool
}

// BindReverse resolves a named reverse namespace and reconstructs its
// canonical physical ForeignKey declaration from the same project snapshot.
func BindReverse[Owner, Source any](
	owner BoundModel[Owner],
	reverseName string,
	source BoundModel[Source],
) (QueryRelation[Owner, Source], error) {
	_, err := bindReverseRelationState(owner, reverseName, source)
	if err != nil {
		return QueryRelation[Owner, Source]{}, err
	}
	return BindQueryRelation(owner, reverseName, source)
}

func bindReverseRelationState[Owner, Source any](
	owner BoundModel[Owner],
	reverseName string,
	source BoundModel[Source],
) (reverseRelationState, error) {
	if err := validateBoundModel(owner); err != nil {
		return reverseRelationState{}, err
	}
	if err := validateBoundModel(source); err != nil {
		return reverseRelationState{}, err
	}
	if owner.snapshot != source.snapshot {
		return reverseRelationState{}, relationInvalidPlan("owner and source models belong to different project snapshots")
	}

	state, err := resolveReverseRelationState(owner.snapshot, owner.identity, owner.model, reverseName)
	if err != nil {
		return reverseRelationState{}, err
	}
	if state.reverse.Target != source.identity || !reflect.DeepEqual(state.forward.sourceModel, source.model) {
		return reverseRelationState{}, relationInvalidPlan("reverse relation source does not match bound source model")
	}
	return state, nil
}

func resolveReverseRelationState(
	snapshot *projectBindingSnapshot,
	owner ir.ModelIdentity,
	ownerModel ir.Model,
	reverseName string,
) (reverseRelationState, error) {
	if snapshot == nil {
		return reverseRelationState{}, relationInvalidPlan("project binding is unbound")
	}
	reverse, ok := findReverseRelation(snapshot, owner, reverseName)
	if !ok {
		return reverseRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnknownRelation,
			Field:    reverseName,
			Detail:   "reverse relation is not present on the owner model",
		}
	}
	if (reverse.Cardinality != ir.RelationOneToMany && reverse.Cardinality != ir.RelationOneToOne) || reverse.Owner != owner {
		return reverseRelationState{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnsupportedLookup,
			Field:    reverseName,
			Detail:   "reverse relation requires a named one-to-many or one-to-one edge",
		}
	}
	sourceModel, ok := snapshot.models[reverse.Target]
	if !ok {
		return reverseRelationState{}, relationInvalidPlan("reverse relation source model is missing")
	}
	forward, err := resolveForwardRelationState(snapshot, reverse.Target, sourceModel, reverse.SourceField)
	if err != nil {
		return reverseRelationState{}, relationInvalidPlan("reverse relation declaration cannot be reconstructed")
	}
	wantForward := ir.RelationManyToOne
	if reverse.Cardinality == ir.RelationOneToOne {
		wantForward = ir.RelationOneToOne
	}
	if forward.metadata.Target != owner ||
		forward.metadata.Cardinality != wantForward ||
		forward.metadata.Reverse.Disabled ||
		forward.metadata.Reverse.Name != reverseName ||
		!reflect.DeepEqual(forward.targetModel, ownerModel) {
		return reverseRelationState{}, relationInvalidPlan("reverse namespace disagrees with its forward declaration")
	}
	return reverseRelationState{
		snapshot: snapshot,
		owner:    owner,
		reverse:  reverse,
		forward:  forward,
		valid:    true,
	}, nil
}

func findReverseRelation(
	snapshot *projectBindingSnapshot,
	owner ir.ModelIdentity,
	name string,
) (ReverseRelationMetadata, bool) {
	if snapshot == nil {
		return ReverseRelationMetadata{}, false
	}
	for _, relation := range snapshot.reverse {
		if relation.Owner == owner && relation.Name == name {
			return relation, true
		}
	}
	return ReverseRelationMetadata{}, false
}

func validateReverseRelationState(state reverseRelationState) error {
	if !state.valid || state.snapshot == nil {
		return relationInvalidPlan("reverse relation is unbound")
	}
	ownerModel, ok := state.snapshot.models[state.owner]
	if !ok || !reflect.DeepEqual(ownerModel, state.forward.targetModel) {
		return relationInvalidPlan("reverse relation owner does not match its project snapshot")
	}
	reverse, ok := findReverseRelation(state.snapshot, state.owner, state.reverse.Name)
	if !ok || reverse != state.reverse {
		return relationInvalidPlan("reverse relation namespace does not match its project snapshot")
	}
	if err := validateForwardState(state.forward); err != nil {
		return err
	}
	return nil
}

func (state reverseRelationState) path(terminal query.FieldRef) (query.RelationPath, error) {
	return query.NewReverseRelationPath(
		state.forward.sourceIdentity,
		state.forward.sourceModel.DBTable,
		state.forward.metadata.Field,
		state.forward.metadata.Column,
		state.owner,
		state.forward.targetModel.DBTable,
		state.forward.targetPrimaryKey.Column,
		state.reverse.Name,
		state.forward.metadata.Nullable,
		terminal, state.reverse.Cardinality,
	)
}
