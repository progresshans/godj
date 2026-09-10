package orm

import (
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ParseDynamicRelations parses bounded forward, two-segment implicit-exact
// lookups through the same path constructor used by typed related fields.
func ParseDynamicRelations[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput) ([]Predicate[M], error) {
	return parseDynamicRelations(model, policy, inputs, false)
}

// ParseDynamicReverseRelations accepts the named reverse counterpart. Direction
// resolution and unsupported paths remain distinct from forward relations.
func ParseDynamicReverseRelations[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput) ([]Predicate[M], error) {
	return parseDynamicRelations(model, policy, inputs, true)
}

func parseDynamicRelations[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput, reverse bool) ([]Predicate[M], error) {
	if err := validateBoundModel(model); err != nil {
		return nil, err
	}
	result := make([]Predicate[M], 0, len(inputs))
	for _, input := range inputs {
		predicate, err := parseDynamicRelationInput(model, policy, input, strings.Split(input.Key, "__"), reverse)
		if err != nil {
			return nil, err
		}
		result = append(result, predicate)
	}
	return result, nil
}

// The caller validates the bound model once for its whole input batch. Failed
// inputs never publish a partial batch; policy sees a detached terminal field.
func parseDynamicRelationInput[M any](model BoundModel[M], policy LookupPolicy, input LookupInput, segments []string, reverse bool) (Predicate[M], error) {
	direction := ""
	if reverse {
		direction = "reverse "
	}
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, direction+"relation lookup must contain exactly two non-empty implicit-exact segments")
	}
	relationName, terminalName := segments[0], segments[1]
	if isRelationLookupSuffix(terminalName) {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, "lookup suffixes on "+direction+"relations are not supported")
	}
	var related ir.Model
	var pathFor func(query.FieldRef) (query.RelationPath, error)
	if reverse {
		state, err := resolveReverseRelationState(model.snapshot, model.identity, model.model, relationName)
		if err != nil {
			return Predicate[M]{}, err
		}
		related, pathFor = state.forward.sourceModel, state.path
	} else {
		if hasReverseRelation(model.snapshot, model.identity, relationName) {
			return Predicate[M]{}, unsupportedRelationLookup(input.Key, "reverse relation predicates are not supported")
		}
		state, err := resolveForwardRelation(model.snapshot, model.identity, model.model, relationName)
		if err != nil {
			return Predicate[M]{}, err
		}
		related, pathFor = state.targetModel, state.path
	}
	terminal, ok := findField(related.Fields, terminalName)
	if !ok {
		return Predicate[M]{}, unknownRelatedField(terminalName)
	}
	if !supportedRelatedTerminal(terminal) {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, direction+"related field kind is not supported")
	}
	if policy != nil && !policy(terminal.Clone(), query.LookupExact) {
		return Predicate[M]{}, &query.Error{Category: query.CategoryField, Code: query.CodeDisallowedLookup,
			Field: terminal.Name, Lookup: string(query.LookupExact), Detail: "lookup was rejected by policy"}
	}
	value, err := dynamicValue(terminal, query.LookupExact, input.Value)
	if err != nil {
		return Predicate[M]{}, err
	}
	path, err := pathFor(fieldReference(terminal))
	if err != nil {
		return Predicate[M]{}, err
	}
	predicate := predicateFromCondition[M](query.NewRelatedCondition(path, query.LookupExact, value), nil)
	if predicate.err != nil {
		return Predicate[M]{}, predicate.err
	}
	return predicate, nil
}

// ParseDynamicRelationObjects is the ordered additive object-surface parser.
// It preserves every GDJ-0025 required implicit-exact lookup and adds only a
// nullable forward relation's two-segment isnull form. Any failure discards
// the entire candidate slice.
func ParseDynamicRelationObjects[M any](
	model BoundModel[M],
	policy LookupPolicy,
	inputs []LookupInput,
) ([]Predicate[M], error) {
	if err := validateBoundModel(model); err != nil {
		return nil, err
	}

	result := make([]Predicate[M], 0, len(inputs))
	for _, input := range inputs {
		segments := strings.Split(input.Key, "__")
		if len(segments) == 2 && segments[0] != "" && segments[1] == string(query.LookupIsNull) {
			metadata, exists := ProjectBinding{snapshot: model.snapshot}.Relation(model.identity, segments[0])
			if exists && metadata.Nullable {
				predicate, err := parseDynamicNullableRelationIsNull(model, policy, input, metadata)
				if err != nil {
					return nil, err
				}
				result = append(result, predicate)
				continue
			}
		}

		predicate, err := parseDynamicRelationInput(model, policy, input, segments, false)
		if err != nil {
			return nil, err
		}
		result = append(result, predicate)
	}
	return result, nil
}

func parseDynamicNullableRelationIsNull[M any](
	model BoundModel[M],
	policy LookupPolicy,
	input LookupInput,
	metadata RelationMetadata,
) (Predicate[M], error) {
	state, err := resolveForwardRelationState(model.snapshot, model.identity, model.model, metadata.Field)
	if err != nil {
		return Predicate[M]{}, err
	}
	if !state.metadata.Nullable {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, "required forward relation isnull is not supported")
	}
	sourceField, ok := findField(state.sourceModel.Fields, state.metadata.Field)
	if !ok || sourceField.Kind != ir.FieldForeignKey || !sourceField.Nullable || sourceField.Relation == nil ||
		sourceField.Relation.Target != state.metadata.Target {
		return Predicate[M]{}, relationInvalidPlan("nullable relation source field is not canonical")
	}
	if policy != nil && !policy(sourceField.Clone(), query.LookupIsNull) {
		return Predicate[M]{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeDisallowedLookup,
			Field:    sourceField.Name,
			Lookup:   string(query.LookupIsNull),
			Detail:   "lookup was rejected by policy",
		}
	}
	value, err := dynamicValue(sourceField, query.LookupIsNull, input.Value)
	if err != nil {
		return Predicate[M]{}, err
	}
	path, err := query.NewNullableForwardRelationIsNullPath(
		state.sourceIdentity,
		state.sourceModel.DBTable,
		fieldReference(sourceField),
		state.metadata.Target,
		state.targetModel.DBTable,
		state.targetPrimaryKey.Column,
	)
	if err != nil {
		return Predicate[M]{}, err
	}
	predicate := predicateFromCondition[M](query.NewRelatedCondition(path, query.LookupIsNull, value), nil)
	if predicate.err != nil {
		return Predicate[M]{}, predicate.err
	}
	return predicate, nil
}

func isRelationLookupSuffix(name string) bool {
	switch query.Lookup(name) {
	case query.LookupExact, query.LookupIsNull, query.LookupIContains:
		return true
	default:
		return false
	}
}

func supportedRelatedTerminal(field ir.Field) bool {
	if field.Relation != nil || field.Nullable {
		return false
	}
	switch field.Kind {
	case ir.FieldAuto, ir.FieldChar:
		return true
	default:
		return false
	}
}

func hasReverseRelation(snapshot *projectBindingSnapshot, owner ir.ModelIdentity, name string) bool {
	if snapshot == nil {
		return false
	}
	for _, relation := range snapshot.reverse {
		if relation.Owner == owner && relation.Name == name {
			return true
		}
	}
	return false
}

func unsupportedRelationLookup(field, detail string) *query.Error {
	return &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeUnsupportedLookup,
		Field:    field,
		Lookup:   string(query.LookupExact),
		Detail:   detail,
	}
}
