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
	config := fieldConfig{modelChoice: true, widget: Select, choices: choices}
	if err := validateChoices(name, FieldInteger, config); err != nil {
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
	if strings.ContainsRune(raw, 0) {
		return Null(), "null_characters_not_allowed"
	}
	// AutoField converts the lookup value as an integer, rather than using
	// IntegerField's additional zero-fraction form syntax.
	if strings.Contains(raw, ".") {
		return Null(), "invalid_choice"
	}
	value, code := cleanInteger(raw)
	if code != "" || value.IsNull() {
		return Null(), "invalid_choice"
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
