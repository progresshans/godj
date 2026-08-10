package orm

import (
	"fmt"
	"sort"

	"github.com/progresshans/godj/schema/ir"
)

// RelationMetadata is the source-side projection of one project-bound
// relation. It describes scalar foreign-key storage only; it does not expose a
// loader, object cache, query path, or write/delete capability.
type RelationMetadata struct {
	Source      ir.ModelIdentity
	Field       string
	Column      string
	Target      ir.ModelIdentity
	Nullable    bool
	Cardinality ir.RelationCardinality
	Reverse     ir.ReverseRelation
	OnDelete    ir.DeletePolicy
}

// ReverseRelationMetadata is derived from a named forward relation during
// project binding. Owner is the target model whose reverse namespace contains
// Name, while Target is the source model reached by that reverse relation.
type ReverseRelationMetadata struct {
	Owner       ir.ModelIdentity
	Name        string
	Target      ir.ModelIdentity
	SourceField string
	Cardinality ir.RelationCardinality
}

type RelationBindingErrorCode string

const (
	RelationBindingDuplicateApp         RelationBindingErrorCode = "duplicate_app"
	RelationBindingUnresolvedTarget     RelationBindingErrorCode = "unresolved_target"
	RelationBindingReverseNameCollision RelationBindingErrorCode = "reverse_name_collision"
)

// RelationBindingError reports the three project-level failures owned by the
// binder. Invalid schema, model, field, key, and relation shapes remain
// *ir.ValidationError values returned by ir.Normalize.
type RelationBindingError struct {
	Code        RelationBindingErrorCode
	AppLabel    string
	ModelName   string
	FieldName   string
	Target      ir.ModelIdentity
	ReverseName string
}

func (e *RelationBindingError) Error() string {
	if e == nil {
		return "relation binding error"
	}
	return fmt.Sprintf(
		"relation binding %s: source=%s.%s field=%s target=%s.%s reverse=%s",
		e.Code,
		e.AppLabel,
		e.ModelName,
		e.FieldName,
		e.Target.AppLabel,
		e.Target.ModelName,
		e.ReverseName,
	)
}

// ProjectBinding is an immutable project-wide relation snapshot. Its private
// slices are constructed only after all schemas and derived reverse namespaces
// validate successfully. Accessors never expose those slices.
type ProjectBinding struct {
	snapshot *projectBindingSnapshot
}

type projectBindingSnapshot struct {
	models  map[ir.ModelIdentity]ir.Model
	forward []RelationMetadata
	reverse []ReverseRelationMetadata
}

type relationBindingCandidate struct {
	metadata RelationMetadata
}

// BindProject snapshots and normalizes one schema per app, resolves every
// symbolic target, validates the reverse namespaces, and publishes the result
// only after the whole candidate set succeeds. The zero-input project is a
// valid empty binding.
func BindProject(schemas ...ir.Schema) (ProjectBinding, error) {
	snapshots := make([]ir.Schema, len(schemas))
	for index := range schemas {
		snapshots[index] = schemas[index].Clone()
	}
	sort.Slice(snapshots, func(left, right int) bool {
		return snapshots[left].AppLabel < snapshots[right].AppLabel
	})

	for index := 1; index < len(snapshots); index++ {
		if snapshots[index-1].AppLabel == snapshots[index].AppLabel {
			return ProjectBinding{}, &RelationBindingError{
				Code:     RelationBindingDuplicateApp,
				AppLabel: snapshots[index].AppLabel,
			}
		}
	}

	normalized := make([]ir.Schema, len(snapshots))
	models := make(map[ir.ModelIdentity]ir.Model)
	for index := range snapshots {
		schema, err := ir.Normalize(snapshots[index])
		if err != nil {
			return ProjectBinding{}, err
		}
		normalized[index] = schema
		for _, model := range schema.Models {
			identity := ir.ModelIdentity{AppLabel: schema.AppLabel, ModelName: model.Name}
			models[identity] = model.Clone()
		}
	}

	candidates := make([]relationBindingCandidate, 0)
	for _, schema := range normalized {
		for _, model := range schema.Models {
			source := ir.ModelIdentity{AppLabel: schema.AppLabel, ModelName: model.Name}
			for _, field := range model.Fields {
				if field.Relation == nil {
					continue
				}
				candidates = append(candidates, relationBindingCandidate{metadata: RelationMetadata{
					Source:      source,
					Field:       field.Name,
					Column:      field.Column,
					Target:      field.Relation.Target,
					Nullable:    field.Nullable,
					Cardinality: field.Relation.Cardinality,
					Reverse:     field.Relation.Reverse,
					OnDelete:    field.Relation.OnDelete,
				}})
			}
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return compareForward(candidates[left].metadata, candidates[right].metadata) < 0
	})

	forward := make([]RelationMetadata, 0, len(candidates))
	reverse := make([]ReverseRelationMetadata, 0, len(candidates))
	reverseNamespaces := make(map[reverseNamespace]struct{}, len(candidates))
	for _, candidate := range candidates {
		relation := candidate.metadata
		targetModel, exists := models[relation.Target]
		if !exists {
			return ProjectBinding{}, bindingFailure(RelationBindingUnresolvedTarget, relation)
		}

		if !relation.Reverse.Disabled {
			if targetModelHasField(targetModel, relation.Reverse.Name) {
				return ProjectBinding{}, bindingFailure(RelationBindingReverseNameCollision, relation)
			}
			namespace := reverseNamespace{owner: relation.Target, name: relation.Reverse.Name}
			if _, collision := reverseNamespaces[namespace]; collision {
				return ProjectBinding{}, bindingFailure(RelationBindingReverseNameCollision, relation)
			}
			reverseNamespaces[namespace] = struct{}{}
			reverse = append(reverse, ReverseRelationMetadata{
				Owner:       relation.Target,
				Name:        relation.Reverse.Name,
				Target:      relation.Source,
				SourceField: relation.Field,
				Cardinality: ir.RelationOneToMany,
			})
		}
		forward = append(forward, relation)
	}
	sort.Slice(reverse, func(left, right int) bool {
		return compareReverse(reverse[left], reverse[right]) < 0
	})

	return ProjectBinding{snapshot: &projectBindingSnapshot{
		models:  models,
		forward: append([]RelationMetadata(nil), forward...),
		reverse: append([]ReverseRelationMetadata(nil), reverse...),
	}}, nil
}

func (b ProjectBinding) ForwardRelations() []RelationMetadata {
	if b.snapshot == nil {
		return nil
	}
	return append([]RelationMetadata(nil), b.snapshot.forward...)
}

func (b ProjectBinding) ReverseRelations() []ReverseRelationMetadata {
	if b.snapshot == nil {
		return nil
	}
	return append([]ReverseRelationMetadata(nil), b.snapshot.reverse...)
}

func (b ProjectBinding) Relation(source ir.ModelIdentity, field string) (RelationMetadata, bool) {
	if b.snapshot == nil {
		return RelationMetadata{}, false
	}
	position := sort.Search(len(b.snapshot.forward), func(index int) bool {
		candidate := b.snapshot.forward[index]
		if comparison := compareModelIdentity(candidate.Source, source); comparison != 0 {
			return comparison >= 0
		}
		return candidate.Field >= field
	})
	if position == len(b.snapshot.forward) {
		return RelationMetadata{}, false
	}
	candidate := b.snapshot.forward[position]
	if candidate.Source != source || candidate.Field != field {
		return RelationMetadata{}, false
	}
	return candidate, true
}

// Model returns a fresh copy of normalized model metadata from the same
// immutable project snapshot as the relation projections. The zero value is
// an empty, unbound snapshot.
func (b ProjectBinding) Model(identity ir.ModelIdentity) (ir.Model, bool) {
	if b.snapshot == nil {
		return ir.Model{}, false
	}
	model, ok := b.snapshot.models[identity]
	if !ok {
		return ir.Model{}, false
	}
	return model.Clone(), true
}

type reverseNamespace struct {
	owner ir.ModelIdentity
	name  string
}

func targetModelHasField(model ir.Model, name string) bool {
	for _, field := range model.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func bindingFailure(code RelationBindingErrorCode, relation RelationMetadata) *RelationBindingError {
	errorValue := &RelationBindingError{
		Code:      code,
		AppLabel:  relation.Source.AppLabel,
		ModelName: relation.Source.ModelName,
		FieldName: relation.Field,
		Target:    relation.Target,
	}
	if code == RelationBindingReverseNameCollision {
		errorValue.ReverseName = relation.Reverse.Name
	}
	return errorValue
}

func compareForward(left, right RelationMetadata) int {
	if comparison := compareModelIdentity(left.Source, right.Source); comparison != 0 {
		return comparison
	}
	return compareString(left.Field, right.Field)
}

func compareReverse(left, right ReverseRelationMetadata) int {
	if comparison := compareModelIdentity(left.Owner, right.Owner); comparison != 0 {
		return comparison
	}
	return compareString(left.Name, right.Name)
}

func compareModelIdentity(left, right ir.ModelIdentity) int {
	if comparison := compareString(left.AppLabel, right.AppLabel); comparison != 0 {
		return comparison
	}
	return compareString(left.ModelName, right.ModelName)
}

func compareString(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
