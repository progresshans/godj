package orm

import (
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ParseDynamicRelations resolves finite mixed paths through the sealed IR.
// Scalar suffixes and FK presence use the same route as generated typed groups.
func ParseDynamicRelations[M any](model BoundModel[M], policy LookupPolicy, inputs []LookupInput) ([]Predicate[M], error) {
	if err := validateBoundModel(model); err != nil {
		return nil, err
	}
	result := make([]Predicate[M], 0, len(inputs))
	for _, input := range inputs {
		predicate, err := parseRelationInput(model, policy, input)
		if err != nil {
			return nil, err
		}
		result = append(result, predicate)
	}
	return result, nil
}

func parseRelationInput[M any](model BoundModel[M], policy LookupPolicy, input LookupInput) (Predicate[M], error) {
	parts := strings.Split(input.Key, "__")
	if len(parts) < 2 || len(parts) > query.MaximumRelationHops+2 {
		return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "relation lookup has an invalid segment count")
	}
	for _, part := range parts {
		if part == "" {
			return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "relation lookup contains an empty segment")
		}
	}
	identity, current := model.identity, model.model
	route := relationQueryRoute{}
	for index, part := range parts {
		field, found := findField(current.Fields, part)
		if index == 0 || found && field.Relation != nil || hasQueryRelation(model.snapshot, identity, part) {
			state, err := resolveQueryRelationStep(model.snapshot, identity, current, part)
			if err != nil {
				return Predicate[M]{}, err
			}
			route.steps = append(route.steps, state)
			if len(route.steps) > query.MaximumRelationHops {
				return Predicate[M]{}, relationInvalidPlan("query route exceeds 64 declarations")
			}
			if index == len(parts)-1 {
				return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "a relation route must end at a scalar field or isnull")
			}
			identity, current = state.targetIdentity, state.targetModel
			// A real scalar field named isnull takes precedence over lookup syntax.
			_, namedTerminal := findField(current.Fields, "isnull")
			namedTerminal = namedTerminal || hasQueryRelation(model.snapshot, identity, "isnull")
			if len(parts)-index == 2 && parts[index+1] == string(query.LookupIsNull) && !namedTerminal {
				sourceKey := state.presence
				if err := allowRelationLookup(policy, sourceKey, query.LookupIsNull); err != nil {
					return Predicate[M]{}, err
				}
				path, err := route.path(fieldReference(sourceKey), state.presenceScope)
				if err != nil {
					return Predicate[M]{}, err
				}
				return dynamicRelationPredicate[M](path, sourceKey, query.LookupIsNull, input.Value, input.JSONPath)
			}
			continue
		}
		if !found {
			if index == len(parts)-1 && isRelationLookupSuffix(part) {
				return Predicate[M]{}, unsupportedRelationLookup(input.Key, query.LookupExact, "lookup suffixes require a terminal field or supported relation isnull")
			}
			return Predicate[M]{}, unknownRelatedField(part)
		}
		if !supportedRelatedTerminal(field) {
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
		return dynamicRelationPredicate[M](path, field, lookup, input.Value, input.JSONPath)
	}
	return Predicate[M]{}, relationInvalidPlan("relation lookup has no terminal")
}

func allowRelationLookup(policy LookupPolicy, field ir.Field, lookup query.Lookup) error {
	if policy != nil && !policy(field.Clone(), lookup) {
		return &query.Error{Category: query.CategoryField, Code: query.CodeDisallowedLookup, Field: field.Name, Lookup: string(lookup), Detail: "lookup was rejected by policy"}
	}
	return nil
}
func dynamicRelationPredicate[M any](path query.RelationPath, terminal ir.Field, lookup query.Lookup, raw any, segments []query.JSONPathSegment) (Predicate[M], error) {
	var condition query.Condition
	if isJSONKeysLookup(lookup) {
		keys, err := dynamicJSONKeys(terminal, lookup, raw)
		if err != nil {
			return Predicate[M]{}, err
		}
		condition, err = query.NewRelatedJSONKeysCondition(path, lookup, keys...)
		if err != nil {
			return Predicate[M]{}, err
		}
	} else if lookup == query.LookupIn {
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
	condition, err := dynamicJSONPath(condition, segments)
	predicate := predicateFromCondition[M](condition, err)
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
	if field.Relation != nil {
		return false
	}
	switch field.Kind {
	case ir.FieldAuto, ir.FieldInteger, ir.FieldChar, ir.FieldText, ir.FieldDateTime, ir.FieldDate, ir.FieldTime, ir.FieldDuration, ir.FieldFloat, ir.FieldDecimal, ir.FieldUUID, ir.FieldJSON, ir.FieldBoolean:
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
