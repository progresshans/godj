package model

import (
	"fmt"
	"slices"

	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

// TransitionCatalogRelation binds a selected catalog FK/OneToOne field to
// the same typed, current-actor choice policy used by account profiles.
// It owns no database, borrowed scope, evaluated rows or durable authority.
type TransitionCatalogRelation[M any] struct{ relation ProfileRelation[M] }

func (TransitionCatalogRelation[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionCatalogRelation{redacted}"))
}

// NewTransitionCatalogRelation uses the target account binding to select the
// catalog namespace and seal both model descriptors to its complete source.
func NewTransitionCatalogRelation[A, M, T any](binding Binding[A], kind TransitionReferenceKind, descriptor orm.RelationObjectDescriptor[M], field string, target orm.Manager[T], config ProfileRelationConfig[T]) (TransitionCatalogRelation[M], error) {
	var zero TransitionCatalogRelation[M]
	if binding.state == nil || kind != TransitionReferenceGroup && kind != TransitionReferencePermission || config.Filter == nil || !zeroModelDescriptor(descriptor) {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	identity, err := transitionReferenceModel(binding.state.role, kind)
	if err != nil {
		return zero, err
	}
	if _, err := orm.BindModel(binding.state.project, identity, descriptor); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	metadata, err := orm.NewManager[M](descriptor).Metadata()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	declared, found := namedField(metadata, field)
	if !found || declared.Relation == nil || !declared.Relation.Cardinality.SingleValued() || declared.PrimaryKey || declared.NonEditable {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	state, err := newProfileRelationState(binding.state.project, binding.state.roleFingerprint, identity, declared, target, config, true)
	return TransitionCatalogRelation[M]{relation: ProfileRelation[M]{state: state}}, err
}

func prepareTransitionCatalogRelations[M any](project orm.ProjectBinding, role [32]byte, identity ir.ModelIdentity, metadata ir.Model, fields []string, configured []TransitionCatalogRelation[M]) ([]ProfileRelation[M], error) {
	source, err := project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	byName := make(map[string]ProfileRelation[M], len(configured))
	for _, configured := range configured {
		relation := configured.relation
		state := relation.state
		if state == nil || state.source != source || state.role != role || state.model != identity || !slices.Contains(fields, state.field.Name) || byName[state.field.Name].state != nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		byName[state.field.Name] = relation
	}
	var result []ProfileRelation[M]
	for _, field := range metadata.Fields {
		if field.Relation == nil || !slices.Contains(fields, field.Name) {
			continue
		}
		relation, found := byName[field.Name]
		if !found {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		result = append(result, relation)
	}
	return result, nil
}
