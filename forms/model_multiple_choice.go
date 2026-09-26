package forms

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/progresshans/godj/validation"
)

// Integers owns an ordered integer list. Its immutable encoding keeps Value
// comparable without sharing a caller's slice or losing int64 precision.
func Integers(values ...int64) Value {
	if values == nil {
		values = []int64{}
	}
	encoded, _ := json.Marshal(values)
	return Value{kind: ValueIntegerList, string: string(encoded)}
}

func (v Value) AsIntegers() ([]int64, bool) {
	if v.kind != ValueIntegerList {
		return nil, false
	}
	var values []int64
	if err := json.Unmarshal([]byte(v.string), &values); err != nil || values == nil {
		return nil, false
	}
	return values, true
}

func (v Values) Integers(name string) ([]int64, bool) {
	value, ok := v.Get(name)
	if !ok {
		return nil, false
	}
	return value.AsIntegers()
}

// ModelMultipleChoiceField selects an entire set from an explicit ordered
// choice snapshot. Binding performs no I/O; the application must recheck the
// complete submitted set and its authorization in the write transaction.
// Empty optional input is an empty list, never NULL. Successful values are
// unique keys in choice order; Changed compares the raw submitted membership.
func ModelMultipleChoiceField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: SelectMultiple, modelChoice: true}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldIntegerList, config)
}

func cleanModelMultipleChoice(field Field, raw []string) (Value, validation.Code) {
	if len(raw) == 0 {
		if field.required {
			return Null(), "required"
		}
		return Integers(), ""
	}
	selected := make(map[string]bool, len(raw))
	for _, text := range raw {
		if strings.ContainsRune(text, 0) {
			return Null(), "null_characters_not_allowed"
		}
		// Match AutoField's integer preparation before checking the original key
		// spelling. Multiple selection rejects aliases that single choice accepts.
		value, code := cleanInteger(text)
		if code != "" || value.IsNull() || strings.Contains(text, ".") {
			return Null(), "invalid_pk_value"
		}
		selected[text] = true
	}
	values := make([]int64, 0, len(selected))
	for _, choice := range field.choices {
		if selected[choice.InputValue()] {
			key, _ := choice.Value.AsInteger()
			values = append(values, key)
			delete(selected, choice.InputValue())
		}
	}
	if len(selected) != 0 {
		return Null(), "invalid_choice"
	}
	return Integers(values...), ""
}

func modelMultipleChoiceChanged(raw []string, initial Value) bool {
	keys, ok := initial.AsIntegers()
	if !ok || len(keys) != len(raw) {
		return true
	}
	before := make(map[string]bool, len(keys))
	after := make(map[string]bool, len(raw))
	for _, key := range keys {
		before[strconv.FormatInt(key, 10)] = true
	}
	for _, value := range raw {
		after[value] = true
	}
	if len(before) != len(after) {
		return true
	}
	for value := range before {
		if !after[value] {
			return true
		}
	}
	return false
}
