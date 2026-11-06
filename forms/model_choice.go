package forms

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/validation"
)

// ModelChoiceField represents one integer model key from an explicitly supplied
// snapshot. Construction and binding never query a database. No choices means
// no permitted nonempty value. The application rechecks membership during its
// write transaction; a displayed choice is not a durable authorization grant.
func ModelChoiceField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: Select, modelChoice: true}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldInteger, config)
}

// ModelChoice distinguishes relation key selection from ordinary integer enums.
func (field Field) ModelChoice() bool { return field.modelChoice }

// WithModelChoices returns an independent form specification with one relation
// choice snapshot replaced. It cannot change structural metadata or validators.
func (s Spec) WithModelChoices(name string, choices ...Choice) (Spec, error) {
	if !s.valid {
		return Spec{}, &ConfigError{Path: "spec", Code: "uninitialized"}
	}
	index, found := s.index[name]
	if !found || !s.fields[index].modelChoice {
		return Spec{}, &ConfigError{Path: "fields." + name, Code: "not_model_choice"}
	}
	config := fieldConfig{modelChoice: true, widget: s.fields[index].widget, choices: choices}
	if err := validateChoices(name, s.fields[index].kind, config); err != nil {
		return Spec{}, err
	}
	fields := s.Fields()
	fields[index].choices = append([]Choice(nil), choices...)
	return NewSpec(fields, s.cross...)
}

func cleanModelChoice(field Field, raw string) (Value, validation.Code) {
	if raw == "" {
		if field.required {
			return Null(), "required"
		}
		return Null(), ""
	}
	value, code := parseModelChoiceKey(raw)
	if code == "invalid_pk_value" {
		code = "invalid_choice"
	}
	if code != "" {
		return Null(), code
	}
	for _, choice := range field.choices {
		if value.Equal(choice.Value) {
			return value, ""
		}
	}
	return Null(), "invalid_choice"
}

func modelChoiceInitial(value Value) string {
	if value.IsNull() {
		return ""
	}
	key, _ := value.AsInteger()
	return strconv.FormatInt(key, 10)
}

// ModelChoiceKeys extracts syntactically valid lookup keys without validating
// membership, requiredness or running field/model validators. A false second
// result means malformed input: skip selection I/O and let normal Bind produce
// the original diagnostic. Even a successful result is not authorization.
// Single choice retains integer aliases; multiple choice requires canonical
// spelling and deduplicates identical raw keys in first-submitted order.
// The caller must use server values instead of raw input for read-only rows.
func (field Field) ModelChoiceKeys(data Data) ([]int64, bool, error) {
	if !field.modelChoice || field.inlineParent {
		return nil, false, &ConfigError{Path: "fields." + field.name, Code: "not_model_choice"}
	}
	raw, _ := data.raw(field.name)
	if field.kind != FieldIntegerList {
		if len(raw) > 1 {
			return nil, false, nil
		}
		if len(raw) == 0 || raw[0] == "" {
			return nil, true, nil
		}
		value, code := parseModelChoiceKey(raw[0])
		if code != "" {
			return nil, false, nil
		}
		key, _ := value.AsInteger()
		return []int64{key}, true, nil
	}
	keys := make([]int64, 0, len(raw))
	seen := make(map[int64]bool, len(raw))
	for _, text := range raw {
		value, code := parseModelChoiceKey(text)
		if code != "" {
			return nil, false, nil
		}
		key, _ := value.AsInteger()
		if strconv.FormatInt(key, 10) != text {
			return nil, false, nil
		}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys, true, nil
}

func parseModelChoiceKey(raw string) (Value, validation.Code) {
	if strings.ContainsRune(raw, 0) {
		return Null(), "null_characters_not_allowed"
	}
	// AutoField lookup preparation excludes IntegerField's zero-fraction syntax.
	if strings.Contains(raw, ".") {
		return Null(), "invalid_pk_value"
	}
	value, code := cleanInteger(raw)
	if code != "" || value.IsNull() {
		return Null(), "invalid_pk_value"
	}
	return value, ""
}
