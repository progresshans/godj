package query

import (
	"slices"
	"unicode/utf8"
)

const (
	MaxJSONKeys     = 1024
	MaxJSONKeyBytes = 4096
)

// JSONKeyList owns literal UTF-8 keys. Order and duplicates remain part of the
// AST even when a backend's membership operation treats them as a set.
// Zero is invalid; NewJSONKeyList() constructs a valid empty list.
type JSONKeyList struct{ data *jsonKeyListData }
type jsonKeyListData struct{ values []string }

func NewJSONKeyList(keys ...string) (JSONKeyList, error) {
	if len(keys) > MaxJSONKeys {
		return JSONKeyList{}, invalidPlanError("JSON key list exceeds 1024 keys")
	}
	bytes := 0
	for _, key := range keys {
		if len(key) > MaxJSONKeyBytes-bytes || !utf8.ValidString(key) {
			return JSONKeyList{}, invalidPlanError("JSON keys require valid UTF-8 and at most 4096 bytes in total")
		}
		bytes += len(key)
	}
	return JSONKeyList{data: &jsonKeyListData{values: slices.Clone(keys)}}, nil
}

func (keys JSONKeyList) Valid() bool { return keys.data != nil }

// Values returns an owned non-nil slice for a valid list, including an empty
// list. This preserves an empty SQL array/JSON array rather than SQL/JSON null.
func (keys JSONKeyList) Values() []string {
	if keys.data == nil {
		return nil
	}
	return append([]string{}, keys.data.values...)
}
func (keys JSONKeyList) Equal(other JSONKeyList) bool {
	if keys.data == other.data {
		return true
	}
	return keys.data != nil && other.data != nil && slices.Equal(keys.data.values, other.data.values)
}

func NewJSONKeysCondition(field FieldRef, lookup Lookup, keys ...string) (Condition, error) {
	owned, err := NewJSONKeyList(keys...)
	if err != nil {
		return Condition{}, err
	}
	condition := Condition{field: field, lookup: lookup, rhs: &conditionRHS{kind: conditionRHSJSONKeys, keys: owned}}
	if err := validateExpressionCondition(condition); err != nil {
		return Condition{}, err
	}
	return condition, nil
}

func NewRelatedJSONKeysCondition(path RelationPath, lookup Lookup, keys ...string) (Condition, error) {
	if !forwardMembershipPath(path) {
		return Condition{}, invalidPlanError("JSON key presence requires a forward target-field path")
	}
	condition, err := NewJSONKeysCondition(path.Terminal(), lookup, keys...)
	if err != nil {
		return Condition{}, err
	}
	condition.relationPath = &path
	return condition, nil
}

func (c Condition) JSONKeys() (JSONKeyList, bool) {
	if c.rhs == nil || c.rhs.kind != conditionRHSJSONKeys {
		return JSONKeyList{}, false
	}
	return c.rhs.keys, true
}

func jsonKeysLookup(lookup Lookup) bool {
	return lookup == LookupHasKey || lookup == LookupHasKeys || lookup == LookupHasAnyKeys
}
