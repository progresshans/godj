package orm

import (
	"fmt"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func (f jsonField[M]) HasKey(key string) Predicate[M] {
	return f.jsonKeys(query.LookupHasKey, []string{key})
}
func (f jsonField[M]) HasKeys(keys ...string) Predicate[M] {
	return f.jsonKeys(query.LookupHasKeys, keys)
}
func (f jsonField[M]) HasAnyKeys(keys ...string) Predicate[M] {
	return f.jsonKeys(query.LookupHasAnyKeys, keys)
}
func (f jsonField[M]) jsonKeys(lookup query.Lookup, keys []string) Predicate[M] {
	if f.err != nil {
		return Predicate[M]{err: f.err}
	}
	condition, err := query.NewJSONKeysCondition(f.reference, lookup, keys...)
	return predicateFromCondition[M](condition, err)
}

func (f RelatedJSONField[M]) HasKey(key string) Predicate[M] {
	return f.jsonKeys(query.LookupHasKey, []string{key})
}
func (f RelatedJSONField[M]) HasKeys(keys ...string) Predicate[M] {
	return f.jsonKeys(query.LookupHasKeys, keys)
}
func (f RelatedJSONField[M]) HasAnyKeys(keys ...string) Predicate[M] {
	return f.jsonKeys(query.LookupHasAnyKeys, keys)
}
func (f RelatedJSONField[M]) jsonKeys(lookup query.Lookup, keys []string) Predicate[M] {
	if f.configurationErr != nil {
		return Predicate[M]{err: f.configurationErr}
	}
	if err := relatedLookupError(f.path, f.valid, lookup); err != nil {
		return Predicate[M]{err: err}
	}
	condition, err := query.NewRelatedJSONKeysCondition(f.path, lookup, keys...)
	return predicateFromCondition[M](condition, err)
}

func (f JSONPathField[M]) HasKey(key string) Predicate[M] {
	return f.jsonKeys(query.LookupHasKey, []string{key})
}
func (f JSONPathField[M]) HasKeys(keys ...string) Predicate[M] {
	return f.jsonKeys(query.LookupHasKeys, keys)
}
func (f JSONPathField[M]) HasAnyKeys(keys ...string) Predicate[M] {
	return f.jsonKeys(query.LookupHasAnyKeys, keys)
}
func (f JSONPathField[M]) jsonKeys(lookup query.Lookup, keys []string) Predicate[M] {
	if f.err != nil {
		return Predicate[M]{err: f.err}
	}
	var condition query.Condition
	var err error
	if f.relation != nil {
		if err = relatedLookupError(*f.relation, true, lookup); err != nil {
			return Predicate[M]{err: err}
		}
		condition, err = query.NewRelatedJSONKeysCondition(*f.relation, lookup, keys...)
	} else {
		condition, err = query.NewJSONKeysCondition(f.field, lookup, keys...)
	}
	if err == nil {
		condition, err = condition.WithJSONPath(f.path)
	}
	return predicateFromCondition[M](condition, err)
}

func isJSONKeysLookup(lookup query.Lookup) bool {
	return lookup == query.LookupHasKey || lookup == query.LookupHasKeys || lookup == query.LookupHasAnyKeys
}
func dynamicJSONKeys(field ir.Field, lookup query.Lookup, raw any) ([]string, error) {
	invalid := func() ([]string, error) {
		return nil, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field.Name, Lookup: string(lookup), Detail: fmt.Sprintf("expected a string key or string key list for JSON key presence, got %T", raw)}
	}
	if lookup == query.LookupHasKey {
		key, ok := raw.(string)
		if !ok {
			return invalid()
		}
		return []string{key}, nil
	}
	switch value := raw.(type) {
	case []string:
		return value, nil // The AST constructor takes the owned copy.
	case []any:
		if len(value) > query.MaxJSONKeys {
			return invalid()
		}
		keys := make([]string, len(value))
		for i, item := range value {
			key, ok := item.(string)
			if !ok {
				return invalid()
			}
			keys[i] = key
		}
		return keys, nil
	default:
		return invalid()
	}
}
