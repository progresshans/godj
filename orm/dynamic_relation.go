package orm

import (
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ParseDynamicRelations parses the bounded two-segment implicit-exact relation
// lookups owned by GDJ-0025. It builds the same RelationPath constructor used
// by typed related fields and publishes no partial predicate slice on error.
func ParseDynamicRelations[M any](
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
		if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
			return nil, unsupportedRelationLookup(input.Key, "relation lookup must contain exactly two non-empty implicit-exact segments")
		}

		relationName, terminalName := segments[0], segments[1]
		if isRelationLookupSuffix(terminalName) {
			return nil, unsupportedRelationLookup(input.Key, "lookup suffixes on relations are not supported")
		}
		if hasReverseRelation(model.snapshot, model.identity, relationName) {
			return nil, unsupportedRelationLookup(input.Key, "reverse relation predicates are not supported")
		}
		state, err := resolveForwardRelation(model.snapshot, model.identity, model.model, relationName)
		if err != nil {
			return nil, err
		}
		terminal, ok := findField(state.targetModel.Fields, terminalName)
		if !ok {
			return nil, unknownRelatedField(terminalName)
		}
		if !supportedRelatedTerminal(terminal) {
			return nil, unsupportedRelationLookup(input.Key, "related field kind is not supported")
		}
		if policy != nil && !policy(terminal.Clone(), query.LookupExact) {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeDisallowedLookup,
				Field:    terminal.Name,
				Lookup:   string(query.LookupExact),
				Detail:   "lookup was rejected by policy",
			}
		}
		value, err := dynamicValue(terminal, query.LookupExact, input.Value)
		if err != nil {
			return nil, err
		}
		path, err := state.path(fieldReference(terminal))
		if err != nil {
			return nil, err
		}
		result = append(result, Predicate[M]{
			condition: query.NewRelatedCondition(path, query.LookupExact, value),
		})
	}
	return result, nil
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
