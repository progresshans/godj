// Package forms provides immutable, database-independent form structure and
// validation. It deliberately owns no model persistence or reflection.
package forms

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// ValueKind identifies the closed set of values accepted by form fields.
type ValueKind uint8

const (
	ValueNull ValueKind = iota
	ValueString
	ValueBoolean
)

// Value is an immutable cleaned or initial form value.
type Value struct {
	kind    ValueKind
	string  string
	boolean bool
}

func Null() Value                  { return Value{kind: ValueNull} }
func String(value string) Value    { return Value{kind: ValueString, string: value} }
func Boolean(value bool) Value     { return Value{kind: ValueBoolean, boolean: value} }
func (v Value) Kind() ValueKind    { return v.kind }
func (v Value) IsNull() bool       { return v.kind == ValueNull }
func (v Value) Equal(o Value) bool { return v == o }

func (v Value) AsString() (string, bool) {
	return v.string, v.kind == ValueString
}

func (v Value) AsBoolean() (bool, bool) {
	return v.boolean, v.kind == ValueBoolean
}

// FieldKind is the bounded form field set implemented by this slice.
type FieldKind uint8

const (
	FieldChar FieldKind = iota + 1
	FieldBoolean
)

// FieldValidator performs pure validation of one already-cleaned field value.
// Implementations should be concurrency-safe when a Spec is shared.
type FieldValidator interface {
	ValidateField(Value) validation.Errors
}

// FieldValidatorFunc adapts a function to FieldValidator.
type FieldValidatorFunc func(Value) validation.Errors

func (f FieldValidatorFunc) ValidateField(value Value) validation.Errors { return f(value) }

// CrossValidator validates a detached cleaned-data snapshot. Values for fields
// that failed field cleaning are absent.
type CrossValidator interface {
	ValidateForm(Values) validation.Errors
}

// CrossValidatorFunc adapts a function to CrossValidator.
type CrossValidatorFunc func(Values) validation.Errors

func (f CrossValidatorFunc) ValidateForm(values Values) validation.Errors { return f(values) }

// FieldOption configures a field through closed constructors below. External
// packages cannot implement arbitrary options.
type FieldOption interface {
	apply(*fieldConfig)
}

type fieldOption func(*fieldConfig)

func (option fieldOption) apply(config *fieldConfig) { option(config) }

func WithLabel(label string) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.label = label })
}

func WithRequired(required bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.required = required })
}

func WithNullable() FieldOption {
	return fieldOption(func(config *fieldConfig) { config.nullable = true })
}

func WithMaxLength(limit int) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.maxLength = limit })
}

func WithDefault(value Value) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.defaultValue = value
		config.hasDefault = true
	})
}

func WithValidators(validators ...FieldValidator) FieldOption {
	detached := append([]FieldValidator(nil), validators...)
	return fieldOption(func(config *fieldConfig) {
		config.validators = append(config.validators, detached...)
	})
}

type fieldConfig struct {
	label        string
	required     bool
	nullable     bool
	maxLength    int
	defaultValue Value
	hasDefault   bool
	validators   []FieldValidator
}

// Field is an immutable form field definition.
type Field struct {
	name         string
	label        string
	kind         FieldKind
	required     bool
	nullable     bool
	maxLength    int
	defaultValue Value
	hasDefault   bool
	validators   []FieldValidator
}

// ConfigError reports a startup-time invalid form definition.
type ConfigError struct {
	Path string
	Code string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("forms: %s: %s", e.Path, e.Code)
}

// CharField creates a stripped Unicode string field.
func CharField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldChar, config)
}

// BooleanField creates a checkbox-like boolean field. Missing input cleans to
// false; callers may opt into required=true when false must be rejected.
func BooleanField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldBoolean, config)
}

func makeField(name string, kind FieldKind, config fieldConfig) (Field, error) {
	if !validName(name) {
		return Field{}, &ConfigError{Path: "fields", Code: "invalid_name"}
	}
	if config.label == "" || !utf8.ValidString(config.label) || strings.ContainsRune(config.label, 0) {
		return Field{}, &ConfigError{Path: "fields." + name + ".label", Code: "invalid"}
	}
	for index, validator := range config.validators {
		if validator == nil {
			return Field{}, &ConfigError{Path: fmt.Sprintf("fields.%s.validators[%d]", name, index), Code: "nil"}
		}
	}
	switch kind {
	case FieldChar:
		if config.maxLength < 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "invalid"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
		if config.hasDefault && config.defaultValue.kind == ValueString && config.maxLength > 0 &&
			utf8.RuneCountInString(config.defaultValue.string) > config.maxLength {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "max_length"}
		}
		if config.hasDefault && config.defaultValue.kind == ValueString &&
			(!utf8.ValidString(config.defaultValue.string) || strings.ContainsRune(config.defaultValue.string, 0)) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "invalid_text"}
		}
	case FieldBoolean:
		if config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "unsupported"}
		}
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if config.hasDefault && config.defaultValue.kind != ValueBoolean {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	default:
		return Field{}, &ConfigError{Path: "fields." + name + ".kind", Code: "unsupported"}
	}
	return Field{
		name:         name,
		label:        config.label,
		kind:         kind,
		required:     config.required,
		nullable:     config.nullable,
		maxLength:    config.maxLength,
		defaultValue: config.defaultValue,
		hasDefault:   config.hasDefault,
		validators:   append([]FieldValidator(nil), config.validators...),
	}, nil
}

func validValueForField(value Value, kind FieldKind, nullable bool) bool {
	if value.kind == ValueNull {
		return nullable
	}
	return kind == FieldChar && value.kind == ValueString || kind == FieldBoolean && value.kind == ValueBoolean
}

func (f Field) Name() string    { return f.name }
func (f Field) Label() string   { return f.label }
func (f Field) Kind() FieldKind { return f.kind }
func (f Field) Required() bool  { return f.required }
func (f Field) Nullable() bool  { return f.nullable }
func (f Field) MaxLength() int  { return f.maxLength }

func (f Field) Default() (Value, bool) { return f.defaultValue, f.hasDefault }

func (f Field) clone() Field {
	clone := f
	clone.validators = append([]FieldValidator(nil), f.validators...)
	return clone
}

// Data is an immutable copy of submitted string values. Presence and an empty
// value are distinct; repeated values are retained for deterministic rejection
// by scalar fields.
type Data struct {
	values map[string][]string
}

func NewData(values map[string][]string) Data {
	clone := make(map[string][]string, len(values))
	for name, submitted := range values {
		clone[name] = append([]string(nil), submitted...)
	}
	return Data{values: clone}
}

func (d Data) Get(name string) ([]string, bool) {
	values, ok := d.values[name]
	return append([]string(nil), values...), ok
}

// Entry is an immutable name/value pair returned by Values.All.
type Entry struct {
	name  string
	value Value
}

func (e Entry) Name() string { return e.name }
func (e Entry) Value() Value { return e.value }

// Values is an immutable ordered typed value collection.
type Values struct {
	order  []string
	values map[string]Value
}

func newValues(order []string, values map[string]Value) Values {
	return Values{
		order:  append([]string(nil), order...),
		values: cloneValueMap(values),
	}
}

func (v Values) Get(name string) (Value, bool) {
	value, ok := v.values[name]
	return value, ok
}

func (v Values) String(name string) (string, bool) {
	value, ok := v.Get(name)
	if !ok {
		return "", false
	}
	return value.AsString()
}

func (v Values) Boolean(name string) (bool, bool) {
	value, ok := v.Get(name)
	if !ok {
		return false, false
	}
	return value.AsBoolean()
}

func (v Values) All() []Entry {
	entries := make([]Entry, 0, len(v.order))
	for _, name := range v.order {
		if value, ok := v.values[name]; ok {
			entries = append(entries, Entry{name: name, value: value})
		}
	}
	return entries
}

func cloneValueMap(values map[string]Value) map[string]Value {
	clone := make(map[string]Value, len(values))
	for name, value := range values {
		clone[name] = value
	}
	return clone
}

// Spec is an immutable reusable form definition.
type Spec struct {
	fields []Field
	cross  []CrossValidator
	valid  bool
}

func NewSpec(fields []Field, validators ...CrossValidator) (Spec, error) {
	if len(fields) == 0 {
		return Spec{}, &ConfigError{Path: "fields", Code: "empty"}
	}
	seen := make(map[string]struct{}, len(fields))
	cloned := make([]Field, len(fields))
	for index, field := range fields {
		if !validName(field.name) {
			return Spec{}, &ConfigError{Path: fmt.Sprintf("fields[%d]", index), Code: "invalid"}
		}
		if _, ok := seen[field.name]; ok {
			return Spec{}, &ConfigError{Path: "fields." + field.name, Code: "duplicate"}
		}
		seen[field.name] = struct{}{}
		cloned[index] = field.clone()
	}
	for index, validator := range validators {
		if validator == nil {
			return Spec{}, &ConfigError{Path: fmt.Sprintf("validators[%d]", index), Code: "nil"}
		}
	}
	return Spec{fields: cloned, cross: append([]CrossValidator(nil), validators...), valid: true}, nil
}

func (s Spec) Fields() []Field {
	fields := make([]Field, len(s.fields))
	for index := range s.fields {
		fields[index] = s.fields[index].clone()
	}
	return fields
}

// Form is an immutable result of evaluating a Spec.
type Form struct {
	bound   bool
	valid   bool
	errors  validation.Errors
	cleaned Values
	initial Values
	changed []string
}

func (f Form) Bound() bool               { return f.bound }
func (f Form) Valid() bool               { return f.valid }
func (f Form) Errors() validation.Errors { return validation.NewErrors(f.errors.All()...) }
func (f Form) Cleaned() Values           { return newValues(f.cleaned.order, f.cleaned.values) }
func (f Form) Initial() Values           { return newValues(f.initial.order, f.initial.values) }
func (f Form) Changed() []string         { return append([]string(nil), f.changed...) }

// Unbound constructs a form without running validators. Initial values are
// checked against the Spec and copied before publication.
func (s Spec) Unbound(initial map[string]Value) (Form, error) {
	if !s.valid {
		return Form{}, &ConfigError{Path: "spec", Code: "uninitialized"}
	}
	resolved, err := s.resolveInitial(initial)
	if err != nil {
		return Form{}, err
	}
	return Form{initial: resolved, cleaned: newValues(nil, nil)}, nil
}

// Bind cleans submitted data in field order, then runs cross-field validators
// against only successfully cleaned fields.
func (s Spec) Bind(data Data, initial map[string]Value) (Form, error) {
	if !s.valid {
		return Form{}, &ConfigError{Path: "spec", Code: "uninitialized"}
	}
	resolvedInitial, err := s.resolveInitial(initial)
	if err != nil {
		return Form{}, err
	}
	cleanedMap := make(map[string]Value, len(s.fields))
	cleanedOrder := make([]string, 0, len(s.fields))
	changed := make([]string, 0, len(s.fields))
	errors := validation.NewErrors()
	for _, field := range s.fields {
		value, fieldErrors := cleanField(field, data)
		errors = errors.Append(fieldErrors)
		if !fieldErrors.Empty() {
			initialValue, _ := resolvedInitial.Get(field.name)
			if fieldChanged(field, data, initialValue) {
				changed = append(changed, field.name)
			}
			continue
		}
		cleanedMap[field.name] = value
		cleanedOrder = append(cleanedOrder, field.name)
		initialValue, _ := resolvedInitial.Get(field.name)
		if !value.Equal(initialValue) {
			changed = append(changed, field.name)
		}
	}
	cleaned := newValues(cleanedOrder, cleanedMap)
	for _, validator := range s.cross {
		errors = errors.Append(validator.ValidateForm(cleaned))
	}
	return Form{
		bound:   true,
		valid:   errors.Empty(),
		errors:  errors,
		cleaned: cleaned,
		initial: resolvedInitial,
		changed: changed,
	}, nil
}

func (s Spec) resolveInitial(provided map[string]Value) (Values, error) {
	fieldByName := make(map[string]Field, len(s.fields))
	for _, field := range s.fields {
		fieldByName[field.name] = field
	}
	for name, value := range provided {
		field, ok := fieldByName[name]
		if !ok {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "unknown_field"}
		}
		if !validValueForField(value, field.kind, field.nullable) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "type_mismatch"}
		}
		if value.kind == ValueString && (!utf8.ValidString(value.string) || strings.ContainsRune(value.string, 0)) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "invalid_text"}
		}
		if value.kind == ValueString && field.maxLength > 0 && utf8.RuneCountInString(value.string) > field.maxLength {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "max_length"}
		}
	}
	values := make(map[string]Value, len(s.fields))
	order := make([]string, 0, len(s.fields))
	for _, field := range s.fields {
		value, ok := provided[field.name]
		if !ok {
			switch {
			case field.hasDefault:
				value = field.defaultValue
			case field.kind == FieldBoolean:
				value = Boolean(false)
			case field.nullable:
				value = Null()
			default:
				value = String("")
			}
		}
		values[field.name] = value
		order = append(order, field.name)
	}
	return newValues(order, values), nil
}

func cleanField(field Field, data Data) (Value, validation.Errors) {
	submitted, present := data.Get(field.name)
	if len(submitted) > 1 {
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "multiple"))
	}
	var value Value
	switch field.kind {
	case FieldChar:
		raw := ""
		if present && len(submitted) == 1 {
			raw = strings.TrimSpace(submitted[0])
		}
		if raw == "" {
			switch {
			case field.required:
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "required"))
			case field.nullable:
				value = Null()
			default:
				value = String("")
			}
		} else {
			value = String(raw)
		}
		errors := validation.NewErrors()
		if raw != "" && !utf8.ValidString(raw) {
			errors = errors.Append(validation.NewErrors(validation.New(validation.Field(field.name), "invalid_utf8")))
		}
		if raw != "" && strings.ContainsRune(raw, 0) {
			errors = errors.Append(validation.NewErrors(validation.New(validation.Field(field.name), "null_characters_not_allowed")))
		}
		if raw != "" && field.maxLength > 0 {
			actual := utf8.RuneCountInString(raw)
			if actual > field.maxLength {
				errors = errors.Append(validation.NewErrors(validation.New(
					validation.Field(field.name),
					"max_length",
					validation.NewParam("limit", strconv.Itoa(field.maxLength)),
					validation.NewParam("actual", strconv.Itoa(actual)),
				)))
			}
		}
		for _, validator := range field.validators {
			errors = errors.Append(validator.ValidateField(value))
		}
		return value, errors
	case FieldBoolean:
		raw := ""
		if present && len(submitted) == 1 {
			raw = strings.ToLower(strings.TrimSpace(submitted[0]))
		}
		switch raw {
		case "", "0", "false", "off", "no":
			value = Boolean(false)
		case "1", "true", "on", "yes":
			value = Boolean(true)
		default:
			return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "invalid"))
		}
		if field.required && !value.boolean {
			return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "required"))
		}
	default:
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "unsupported"))
	}
	errors := validation.NewErrors()
	for _, validator := range field.validators {
		errors = errors.Append(validator.ValidateField(value))
	}
	return value, errors
}

func fieldChanged(field Field, data Data, initial Value) bool {
	submitted, present := data.Get(field.name)
	if len(submitted) > 1 {
		return true
	}
	switch field.kind {
	case FieldChar:
		raw := ""
		if present && len(submitted) == 1 {
			raw = strings.TrimSpace(submitted[0])
		}
		value := String(raw)
		if raw == "" && field.nullable {
			value = Null()
		}
		return !value.Equal(initial)
	case FieldBoolean:
		raw := ""
		if present && len(submitted) == 1 {
			raw = strings.ToLower(strings.TrimSpace(submitted[0]))
		}
		switch raw {
		case "", "0", "false", "off", "no":
			return !Boolean(false).Equal(initial)
		case "1", "true", "on", "yes":
			return !Boolean(true).Equal(initial)
		default:
			return true
		}
	default:
		return true
	}
}

func validName(name string) bool {
	if name == "" || strings.HasPrefix(name, "_") || !utf8.ValidString(name) {
		return false
	}
	for index, r := range name {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
