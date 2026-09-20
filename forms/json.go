package forms

import (
	"strings"

	"github.com/progresshans/godj/internal/jsoninput"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/validation"
)

// JSON snapshots an exact document. Invalid literals remain an invalid JSON
// form value; they never silently become a nullable field's empty value.
func JSON(value jsonvalue.Value) Value {
	canonical, err := value.Canonical()
	if err != nil {
		return Value{kind: ValueJSON}
	}
	return Value{kind: ValueJSON, string: canonical.Text}
}

func (value Value) AsJSON() (jsonvalue.Value, bool) {
	if value.kind != ValueJSON {
		return jsonvalue.Value{}, false
	}
	document, err := jsonvalue.Parse([]byte(value.string))
	return document, err == nil
}

func (values Values) JSON(name string) (jsonvalue.Value, bool) {
	value, ok := values.Get(name)
	if !ok {
		return jsonvalue.Value{}, false
	}
	return value.AsJSON()
}

func JSONField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: Textarea}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldJSON, config)
}

func cleanJSON(raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	document, err := jsonvalue.Parse([]byte(raw))
	if err != nil {
		return Null(), "invalid"
	}
	if document == jsonvalue.Null() {
		return Null(), ""
	}
	return JSON(document), ""
}

func emptyJSON(value Value) bool {
	if value.IsNull() {
		return true
	}
	document, ok := value.AsJSON()
	return ok && (document.Text == "null" || document.Text == `""` || document.Text == "[]" || document.Text == "{}")
}

func validateJSON(value Value) validation.Code {
	document, ok := value.AsJSON()
	if !ok || len(document.Text) == 0 || document.Text[0] != '"' {
		return ""
	}
	decoded, err := document.Decode()
	if err == nil {
		if text, ok := decoded.(string); ok && strings.ContainsRune(text, 0) {
			return "null_characters_not_allowed"
		}
	}
	return ""
}

func changedJSON(raw string, initial Value) bool {
	value, code := cleanJSON(raw)
	if code != "" {
		return true
	}
	return !equalJSON(value, initial)
}

func equalJSON(value, initial Value) bool {
	// Form's blank/null input represents None. Keep an unchanged stored JSON
	// null from becoming a write solely because its initial model tag is richer.
	left, right := jsonvalue.Null(), jsonvalue.Null()
	var ok bool
	if !value.IsNull() {
		left, ok = value.AsJSON()
		if !ok {
			return false
		}
	}
	if !initial.IsNull() {
		right, ok = initial.AsJSON()
		if !ok {
			return false
		}
	}
	return jsoninput.Equal(left, right)
}
