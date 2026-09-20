// Package forms provides immutable, database-independent form structure and
// validation. It deliberately owns no model persistence or reflection.
package forms

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/booleaninput"
	"github.com/progresshans/godj/validation"
)

// ValueKind identifies the closed set of values accepted by form fields.
type ValueKind uint8

const (
	ValueNull ValueKind = iota
	ValueString
	ValueBoolean
	ValueInteger
	ValueDateTime
	ValueDate
	ValueDuration
	ValueFloat
	ValueDecimal
	ValueTime
	ValueUUID
)

// Value is an immutable cleaned or initial form value.
type Value struct {
	kind    ValueKind
	string  string
	boolean bool
	integer int64
}

func Null() Value               { return Value{kind: ValueNull} }
func String(value string) Value { return Value{kind: ValueString, string: value} }
func Boolean(value bool) Value  { return Value{kind: ValueBoolean, boolean: value} }
func Integer(value int64) Value { return Value{kind: ValueInteger, integer: value} }
func (v Value) Kind() ValueKind { return v.kind }
func (v Value) IsNull() bool    { return v.kind == ValueNull }
func (v Value) Equal(o Value) bool {
	if v.kind == ValueDecimal && o.kind == ValueDecimal {
		left, lok := v.AsDecimal()
		right, rok := o.AsDecimal()
		return lok && rok && left.Equal(right)
	}
	if v.kind == ValueFloat && o.kind == ValueFloat {
		left, _ := v.AsFloat()
		right, _ := o.AsFloat()
		return left == right
	}
	return v == o
}

func (v Value) AsString() (string, bool) {
	return v.string, v.kind == ValueString
}

func (v Value) AsBoolean() (bool, bool) {
	return v.boolean, v.kind == ValueBoolean
}

func (v Value) AsInteger() (int64, bool) {
	return v.integer, v.kind == ValueInteger
}

// FieldKind is the bounded form field set implemented by this slice.
type FieldKind uint8

const (
	FieldChar FieldKind = iota + 1
	FieldBoolean
	FieldInteger
	FieldDateTime
	FieldDate
	FieldDuration
	FieldFloat
	FieldDecimal
	FieldTime
	FieldUUID
)

// Widget selects presentation independently of the field's cleaned value type.
// Only combinations with an implemented submission representation are accepted.
type Widget uint8

const (
	TextInput Widget = iota + 1
	Textarea
	Checkbox
	DateTimeInput
	DateInput
	Select
	NullBooleanSelect
	TimeInput
	NumberInput
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

func WithWidget(widget Widget) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.widget, config.hasWidget = widget, true })
}

// WithEmptyValue selects Null or the empty string for optional string input.
// A null empty value requires a nullable field; it is not an input default.
func WithEmptyValue(value Value) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.emptyValue, config.hasEmptyValue = value, true
	})
}

func WithValidators(validators ...FieldValidator) FieldOption {
	detached := append([]FieldValidator(nil), validators...)
	return fieldOption(func(config *fieldConfig) {
		config.validators = append(config.validators, detached...)
	})
}

type fieldConfig struct {
	label         string
	widget        Widget
	hasWidget     bool
	choices       []Choice
	emptyValue    Value
	hasEmptyValue bool
	required      bool
	nullable      bool
	maxLength     int
	decimalDigits int
	decimalPlaces int
	defaultValue  Value
	hasDefault    bool
	validators    []FieldValidator
}

// Field is an immutable form field definition.
type Field struct {
	name          string
	label         string
	kind          FieldKind
	widget        Widget
	choices       []Choice
	emptyValue    Value
	required      bool
	nullable      bool
	maxLength     int
	decimalDigits int
	decimalPlaces int
	defaultValue  Value
	hasDefault    bool
	validators    []FieldValidator
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
	config := fieldConfig{label: name, required: true, widget: TextInput}
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
// WithNullable selects a three-state input whose missing/unknown value is Null.
// The checkbox required rule does not apply to this nullable widget, matching
// Django's NullBooleanField; custom field validators may reject Null.
func BooleanField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, widget: Checkbox}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldBoolean, config)
}

// IntegerField cleans signed decimal input without converting through floating
// point. Optional empty input requires WithNullable and cleans to Null.
func IntegerField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TextInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldInteger, config)
}

func makeField(name string, kind FieldKind, config fieldConfig) (Field, error) {
	if !validName(name) {
		return Field{}, &ConfigError{Path: "fields", Code: "invalid_name"}
	}
	if config.label == "" || !utf8.ValidString(config.label) || strings.ContainsRune(config.label, 0) {
		return Field{}, &ConfigError{Path: "fields." + name + ".label", Code: "invalid"}
	}
	if err := validateChoices(name, kind, config); err != nil {
		return Field{}, err
	}
	if config.choices != nil && !config.hasWidget {
		config.widget = Select
	}
	if kind == FieldBoolean && config.nullable && !config.hasWidget {
		config.widget = NullBooleanSelect
	}
	if !(kind == FieldChar && (config.widget == TextInput || config.widget == Textarea) ||
		kind == FieldBoolean && (config.nullable && config.widget == NullBooleanSelect || !config.nullable && config.widget == Checkbox) || kind == FieldInteger && config.widget == TextInput || kind == FieldDateTime && (config.widget == DateTimeInput || config.widget == TextInput) ||
		kind == FieldTime && (config.widget == TimeInput || config.widget == TextInput) ||
		(kind == FieldFloat || kind == FieldDecimal) && (config.widget == NumberInput || config.widget == TextInput) ||
		(kind == FieldDuration || kind == FieldUUID) && config.widget == TextInput ||
		kind == FieldDate && (config.widget == DateInput || config.widget == TextInput) ||
		config.choices != nil && config.widget == Select) {
		return Field{}, &ConfigError{Path: "fields." + name + ".widget", Code: "unsupported_combination"}
	}
	if config.hasEmptyValue {
		if kind != FieldChar || !(config.emptyValue.IsNull() && config.nullable ||
			config.emptyValue.kind == ValueString && config.emptyValue.string == "") {
			return Field{}, &ConfigError{Path: "fields." + name + ".empty_value", Code: "unsupported"}
		}
	} else if kind == FieldChar && !config.nullable {
		config.emptyValue = String("")
	}
	for index, validator := range config.validators {
		if nilInterface(validator) {
			return Field{}, &ConfigError{Path: fmt.Sprintf("fields.%s.validators[%d]", name, index), Code: "nil"}
		}
	}
	switch kind {
	case FieldDecimal:
		if config.decimalDigits < 1 || config.decimalDigits > decimal.MaxDigits || config.decimalPlaces < 0 || config.decimalPlaces > config.decimalDigits {
			return Field{}, &ConfigError{Path: "fields." + name + ".precision", Code: "invalid"}
		}
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_decimal_requires_null"}
		}
		if config.hasDefault {
			if !validValueForField(config.defaultValue, kind, config.nullable) {
				return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
			}
			if !config.defaultValue.IsNull() {
				number, ok := config.defaultValue.AsDecimal()
				if !ok || !number.Fits(config.decimalDigits, config.decimalPlaces) {
					return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "precision"}
				}
			}
		}
	case FieldUUID:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_uuid_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldFloat:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_float_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldDuration:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_duration_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldTime:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_time_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldDate:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_date_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldDateTime:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_datetime_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldInteger:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_integer_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
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
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	default:
		return Field{}, &ConfigError{Path: "fields." + name + ".kind", Code: "unsupported"}
	}
	return Field{
		name:          name,
		label:         config.label,
		kind:          kind,
		widget:        config.widget,
		choices:       append([]Choice(nil), config.choices...),
		emptyValue:    config.emptyValue,
		required:      config.required,
		nullable:      config.nullable,
		maxLength:     config.maxLength,
		decimalDigits: config.decimalDigits, decimalPlaces: config.decimalPlaces,
		defaultValue: config.defaultValue,
		hasDefault:   config.hasDefault,
		validators:   append([]FieldValidator(nil), config.validators...),
	}, nil
}

func validValueForField(value Value, kind FieldKind, nullable bool) bool {
	if value.kind == ValueNull {
		return nullable
	}
	return kind == FieldChar && value.kind == ValueString || kind == FieldBoolean && value.kind == ValueBoolean ||
		kind == FieldInteger && value.kind == ValueInteger || kind == FieldDateTime && value.kind == ValueDateTime || kind == FieldDate && value.kind == ValueDate || kind == FieldTime && value.kind == ValueTime || kind == FieldDuration && value.kind == ValueDuration || kind == FieldFloat && value.kind == ValueFloat || kind == FieldDecimal && value.kind == ValueDecimal || kind == FieldUUID && value.kind == ValueUUID
}

func (f Field) Name() string              { return f.name }
func (f Field) Label() string             { return f.label }
func (f Field) Kind() FieldKind           { return f.kind }
func (f Field) Widget() Widget            { return f.widget }
func (f Field) EmptyValue() (Value, bool) { return f.emptyValue, f.kind == FieldChar }
func (f Field) Required() bool            { return f.required }
func (f Field) Nullable() bool            { return f.nullable }
func (f Field) MaxLength() int            { return f.maxLength }

func (f Field) Default() (Value, bool) { return f.defaultValue, f.hasDefault }

func (f Field) clone() Field {
	clone := f
	clone.choices = append([]Choice(nil), f.choices...)
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

func (v Values) Integer(name string) (int64, bool) {
	value, ok := v.Get(name)
	if !ok {
		return 0, false
	}
	return value.AsInteger()
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
	fields  []Field
	index   map[string]int
	initial Values
	cross   []CrossValidator
	valid   bool
}

func NewSpec(fields []Field, validators ...CrossValidator) (Spec, error) {
	if len(fields) == 0 {
		return Spec{}, &ConfigError{Path: "fields", Code: "empty"}
	}
	byName := make(map[string]int, len(fields))
	cloned := make([]Field, len(fields))
	initial := Values{order: make([]string, len(fields)), values: make(map[string]Value, len(fields))}
	for index, field := range fields {
		if !validName(field.name) {
			return Spec{}, &ConfigError{Path: fmt.Sprintf("fields[%d]", index), Code: "invalid"}
		}
		if _, ok := byName[field.name]; ok {
			return Spec{}, &ConfigError{Path: "fields." + field.name, Code: "duplicate"}
		}
		byName[field.name] = index
		cloned[index] = field.clone()
		initial.order[index] = field.name
		value := String("")
		switch {
		case field.hasDefault:
			value = field.defaultValue
		case field.kind == FieldBoolean && !field.nullable:
			value = Boolean(false)
		case field.kind == FieldInteger || field.kind == FieldDateTime || field.kind == FieldDate || field.kind == FieldTime || field.kind == FieldDuration || field.kind == FieldFloat || field.kind == FieldDecimal || field.kind == FieldUUID:
			value = Null()
		case field.kind == FieldChar:
			value = field.emptyValue
		case field.nullable:
			value = Null()
		}
		initial.values[field.name] = value
	}
	for index, validator := range validators {
		if nilInterface(validator) {
			return Spec{}, &ConfigError{Path: fmt.Sprintf("validators[%d]", index), Code: "nil"}
		}
	}
	return Spec{fields: cloned, index: byName, initial: initial, cross: append([]CrossValidator(nil), validators...), valid: true}, nil
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
func (f Form) Errors() validation.Errors { return f.errors }
func (f Form) Cleaned() Values           { return f.cleaned }
func (f Form) Initial() Values           { return f.initial }
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
	return Form{initial: resolved}, nil
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
	var failures []validation.Errors
	for _, field := range s.fields {
		value, fieldErrors := cleanField(field, data)
		if !fieldErrors.Empty() {
			failures = append(failures, fieldErrors)
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
	cleaned := Values{order: cleanedOrder, values: cleanedMap}
	for _, validator := range s.cross {
		if failure := validator.ValidateForm(cleaned); !failure.Empty() {
			failures = append(failures, failure)
		}
	}
	errors := validation.Join(failures...)
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
	if len(provided) == 0 {
		return s.initial, nil
	}
	for name, value := range provided {
		index, ok := s.index[name]
		if !ok {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "unknown_field"}
		}
		field := s.fields[index]
		if !validValueForField(value, field.kind, field.nullable) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "type_mismatch"}
		}
		if field.kind == FieldDecimal && !value.IsNull() {
			number, ok := value.AsDecimal()
			if !ok || !number.Fits(field.decimalDigits, field.decimalPlaces) {
				return Values{}, &ConfigError{Path: "initial." + name, Code: "precision"}
			}
		}
		if value.kind == ValueString && (!utf8.ValidString(value.string) || strings.ContainsRune(value.string, 0)) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "invalid_text"}
		}
		if value.kind == ValueString && field.maxLength > 0 && utf8.RuneCountInString(value.string) > field.maxLength {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "max_length"}
		}
	}
	values := cloneValueMap(s.initial.values)
	for name, value := range provided {
		values[name] = value
	}
	return Values{order: s.initial.order, values: values}, nil
}

func cleanField(field Field, data Data) (Value, validation.Errors) {
	submitted, present := data.values[field.name]
	if len(submitted) > 1 {
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "multiple"))
	}
	var value Value
	var failures []validation.Errors
	if field.choices != nil {
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		var code validation.Code
		value, code = cleanChoice(field, raw)
		if code != "" {
			return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
		}
	} else {
		switch field.kind {
		case FieldDecimal:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanDecimal(field, raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldUUID:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanUUID(raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldFloat:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanFloat(raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldDuration:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanDuration(raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldTime:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanTime(raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldDate:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanDate(raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldDateTime:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanDateTime(raw)
			if code == "" && value.IsNull() && field.required {
				code = "required"
			}
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
		case FieldInteger:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanInteger(raw)
			if code != "" {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
			}
			if value.IsNull() && field.required {
				return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "required"))
			}
		case FieldChar:
			raw := ""
			if present && len(submitted) == 1 {
				raw = strings.TrimSpace(submitted[0])
			}
			if raw == "" {
				switch {
				case field.required:
					return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "required"))
				default:
					value = field.emptyValue
				}
			} else {
				value = String(raw)
			}
			if raw != "" && !utf8.ValidString(raw) {
				failures = append(failures, validation.NewErrors(validation.New(validation.Field(field.name), "invalid_utf8")))
			}
			if raw != "" && strings.ContainsRune(raw, 0) {
				failures = append(failures, validation.NewErrors(validation.New(validation.Field(field.name), "null_characters_not_allowed")))
			}
			if raw != "" && field.maxLength > 0 {
				actual := utf8.RuneCountInString(raw)
				if actual > field.maxLength {
					failures = append(failures, validation.NewErrors(validation.New(
						validation.Field(field.name),
						"max_length",
						validation.NewParam("limit", strconv.Itoa(field.maxLength)),
						validation.NewParam("actual", strconv.Itoa(actual)),
					)))
				}
			}
		case FieldBoolean:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			if field.nullable {
				value = nullableBooleanValue(raw)
				break
			}
			raw = strings.ToLower(strings.TrimSpace(raw))
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
	}
	for _, validator := range field.validators {
		if failure := validator.ValidateField(value); !failure.Empty() {
			failures = append(failures, failure)
		}
	}
	return value, validation.Join(failures...)
}

func fieldChanged(field Field, data Data, initial Value) bool {
	submitted, present := data.values[field.name]
	if len(submitted) > 1 {
		return true
	}
	if field.choices != nil {
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanChoice(field, raw)
		return code != "" || !value.Equal(initial)
	}
	switch field.kind {
	case FieldDecimal:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		return changedDecimal(raw, initial)
	case FieldUUID:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanUUID(raw)
		return code != "" || !value.Equal(initial)
	case FieldFloat:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanFloat(raw)
		return code != "" || !value.Equal(initial)
	case FieldDuration:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanDuration(raw)
		return code != "" || !value.Equal(initial)
	case FieldTime:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanTime(raw)
		return code != "" || !value.Equal(initial)
	case FieldDate:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanDate(raw)
		return code != "" || !value.Equal(initial)
	case FieldDateTime:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanDateTime(raw)
		return code != "" || !value.Equal(initial)
	case FieldInteger:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		value, code := cleanInteger(raw)
		return code != "" || !value.Equal(initial)
	case FieldChar:
		raw := ""
		if present && len(submitted) == 1 {
			raw = strings.TrimSpace(submitted[0])
		}
		value := String(raw)
		if raw == "" {
			value = field.emptyValue
		}
		return !value.Equal(initial)
	case FieldBoolean:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		if field.nullable {
			return !nullableBooleanValue(raw).Equal(initial)
		}
		raw = strings.ToLower(strings.TrimSpace(raw))
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

func nullableBooleanValue(raw string) Value {
	if value, known := booleaninput.NullableSelect(raw); known {
		return Boolean(value)
	}
	return Null()
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

// nilInterface rejects typed nil implementations before an immutable Spec is
// published. Reflection is limited to startup validation; form evaluation
// does not pay this cost.
func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
