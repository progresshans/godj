package orm

import (
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ParseDynamicRelations resolves finite forward paths through the sealed IR.
// Scalar suffixes and FK presence use the same route as generated typed groups.
func ParseDynamicRelations[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput) ([]Predicate[M], error) {
	return parseRelationBatch(model, policy, inputs, parseForwardRelationInput[M])
}

// ParseDynamicReverseRelations retains the supported direct reverse grammar.
func ParseDynamicReverseRelations[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput) ([]Predicate[M], error) {
	return parseRelationBatch(model, policy, inputs, parseReverseRelationInput[M])
}
func parseRelationBatch[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput, parse func(BoundModel[M], LookupPolicy, LookupInput) (Predicate[M], error)) ([]Predicate[M], error) {
	if err := validateBoundModel(model); err != nil {
		return nil, err
	}
	result := make([]Predicate[M], 0, len(inputs))
	for _, input := range inputs {
		predicate, err := parse(model, policy, input)
		if err != nil {
			return nil, err
		}
		result = append(result, predicate)
	}
	return result, nil
}

func parseForwardRelationInput[M any](model BoundModel[M], policy LookupPolicy, input LookupInput) (Predicate[M], error) {
	parts := strings.Split(input.Key, "__")
	if len(parts) < 2 || len(parts) > query.MaximumRelationHops+2 {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "forward relation lookup has an invalid segment count")
	}
	for _, part := range parts {
		if part == "" {
			return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "forward relation lookup contains an empty segment")
		}
	}
	identity, current := model.identity, model.model
	route := forwardQueryRoute{}
	for index, part := range parts {
		field, found := findField(current.Fields, part)
		if index == 0 || found && field.Relation != nil {
			if hasReverseRelation(model.snapshot, identity, part) {
				return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "reverse traversal is not supported by the forward parser")
			}
			state, err := resolveForwardRelationState(model.snapshot, identity, current, part)
			if err != nil {
				return Predicate[M]{}, err
			}
			route.steps = append(route.steps, state)
			if len(route.steps) > query.MaximumRelationHops {
				return Predicate[M]{}, relationInvalidPlan("forward query route exceeds 64 declarations")
			}
			if index == len(parts)-1 {
				return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "a forward route must end at a scalar field or isnull")
			}
			identity, current = state.metadata.Target, state.targetModel
			// A real scalar field named isnull takes precedence over lookup syntax.
			_, namedTerminal := findField(current.Fields, "isnull")
			if len(parts)-index == 2 && parts[index+1] == string(query.LookupIsNull) && !namedTerminal {
				sourceKey, ok := findField(state.sourceModel.Fields, state.metadata.Field)
				if !ok {
					return Predicate[M]{}, relationInvalidPlan("forward source key is unavailable")
				}
				if err := allowRelationLookup(policy, sourceKey, query.LookupIsNull); err != nil {
					return Predicate[M]{}, err
				}
				path, err := route.path(fieldReference(sourceKey), query.RelationTerminalSourceKey)
				if err != nil {
					return Predicate[M]{}, err
				}
				return dynamicRelationPredicate[M](path, sourceKey, query.LookupIsNull, input.Value)
			}
			continue
		}
		if !found {
			if index == len(parts)-1 && isRelationLookupSuffix(part) {
				return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "lookup suffixes require a terminal field or supported relation isnull")
			}
			if hasReverseRelation(model.snapshot, identity, part) {
				return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "reverse traversal is not supported by the forward parser")
			}
			return Predicate[M]{}, unknownRelatedField(part)
		}
		if !supportedRelatedTerminal(field, false) {
			return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "related field kind is not supported")
		}
		remaining := len(parts) - index - 1
		if remaining > 1 {
			return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "lookup suffix must follow a terminal scalar field")
		}
		lookupName := string(query.LookupExact)
		if remaining == 1 {
			lookupName = parts[index+1]
		}
		lookup, supported := supportedLookup(field, lookupName)
		if !supported {
			return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.Lookup(lookupName), "lookup is not supported for the related field kind")
		}
		if err := allowRelationLookup(policy, field, lookup); err != nil {
			return Predicate[M]{}, err
		}
		path, err := route.path(fieldReference(field), query.RelationTerminalRelatedField)
		if err != nil {
			return Predicate[M]{}, err
		}
		return dynamicRelationPredicate[M](path, field, lookup, input.Value)
	}
	return Predicate[M]{}, relationInvalidPlan("forward lookup has no terminal")
}

func parseReverseRelationInput[M any](model BoundModel[M], policy LookupPolicy, input LookupInput) (Predicate[M], error) {
	parts := strings.Split(input.Key, "__")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "reverse relation lookup requires a direct target field")
	}
	if isRelationLookupSuffix(parts[1]) {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "lookup suffixes on reverse relations are not supported")
	}
	state, err := resolveReverseRelationState(model.snapshot, model.identity, model.model, parts[0])
	if err != nil {
		return Predicate[M]{}, err
	}
	field, found := findField(state.forward.sourceModel.Fields, parts[1])
	if !found {
		return Predicate[M]{}, unknownRelatedField(parts[1])
	}
	if !supportedRelatedTerminal(field, true) {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "reverse related field kind is not supported")
	}
	if err := allowRelationLookup(policy, field, query.LookupExact); err != nil {
		return Predicate[M]{}, err
	}
	path, err := state.path(fieldReference(field))
	if err != nil {
		return Predicate[M]{}, err
	}
	return dynamicRelationPredicate[M](path, field, query.LookupExact, input.Value)
}

func allowRelationLookup(policy LookupPolicy, field ir.Field, lookup query.Lookup) error {
	if policy != nil && !policy(field.Clone(), lookup) {
		return &query.Error{Category: query.CategoryField, Code: query.CodeDisallowedLookup, Field: field.Name, Lookup: string(lookup), Detail: "lookup was rejected by policy"}
	}
	return nil
}
func dynamicRelationPredicate[M any](path query.RelationPath, terminal ir.Field, lookup query.Lookup, raw any) (Predicate[M], error) {
	var condition query.Condition
	if lookup == query.LookupIn {
		values, err := dynamicMembership(terminal, raw)
		if err != nil {
			return Predicate[M]{}, err
		}
		condition, err = query.NewRelatedInCondition(path, values)
		if err != nil {
			return Predicate[M]{}, err
		}
	} else {
		value, err := dynamicValue(terminal, lookup, raw)
		if err != nil {
			return Predicate[M]{}, err
		}
		condition = query.NewRelatedCondition(path, lookup, value)
	}
	predicate := predicateFromCondition[M](condition, nil)
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

func supportedRelatedTerminal(field ir.Field, reverse bool) bool {
	if field.Relation != nil || reverse && (field.Nullable || field.Kind == ir.FieldBoolean) {
		return false
	}
	switch field.Kind {
	case ir.FieldAuto, ir.FieldInteger, ir.FieldChar, ir.FieldText, ir.FieldDateTime, ir.FieldDate, ir.FieldTime, ir.FieldDuration, ir.FieldFloat, ir.FieldDecimal, ir.FieldBoolean:
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

func unsupportedRelationLookup(field string, lookup query.Lookup, detail string) *query.Error {
	return &query.Error{
		Category: query.CategoryField,
		Code:     query.CodeUnsupportedLookup,
		Field:    field,
		Lookup:   string(lookup),
		Detail:   detail,
	}
}
