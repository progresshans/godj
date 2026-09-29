// Package forms provides immutable, database-independent form structure and
// validation. It deliberately owns no model persistence or reflection.
package forms

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/booleaninput"
	"github.com/progresshans/godj/internal/emailinput"
	"github.com/progresshans/godj/internal/jsoninput"
	"github.com/progresshans/godj/uploads"
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
	ValueJSON
	ValueIntegerList
	ValueFile
)

// Value is an immutable cleaned or initial form value.
type Value struct {
	kind      ValueKind
	textState *privateText
	boolean   bool
	integer   int64
	fileState *privateFile
}

// Text payloads are opaque even to fmt's reflection fallback for unsupported
// verbs. Value equality is semantic; use Equal instead of Go pointer equality.
type privateText struct{ value string }

func textValue(kind ValueKind, text string) Value {
	return Value{kind: kind, textState: &privateText{value: text}}
}
func (v Value) text() string {
	if v.textState == nil {
		return ""
	}
	return v.textState.value
}

func Null() Value               { return Value{kind: ValueNull} }
func String(value string) Value { return textValue(ValueString, value) }
func Boolean(value bool) Value  { return Value{kind: ValueBoolean, boolean: value} }
func Integer(value int64) Value { return Value{kind: ValueInteger, integer: value} }
func (v Value) Kind() ValueKind { return v.kind }
func (v Value) IsNull() bool    { return v.kind == ValueNull }
func (v Value) Equal(o Value) bool {
	if v.kind == ValueFile && o.kind == ValueFile {
		if v.fileState == nil || o.fileState == nil {
			return v.fileState == o.fileState
		}
		left, right := v.fileState, o.fileState
		return left.name == right.name && left.clear == right.clear && (left.upload.Equal(right.upload) || !left.upload.Valid() && !right.upload.Valid())
	}
	if v.kind == ValueJSON && o.kind == ValueJSON {
		left, lok := v.AsJSON()
		right, rok := o.AsJSON()
		return lok && rok && jsoninput.Equal(left, right)
	}
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
	return v.kind == o.kind && v.text() == o.text() && v.boolean == o.boolean && v.integer == o.integer
}

func (v Value) AsString() (string, bool) {
	return v.text(), v.kind == ValueString
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
	FieldJSON
	FieldIntegerList
	FieldEmail
	FieldFile
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
	SelectMultiple
	PasswordInput
	EmailInput
	HiddenInput
	FileInput
	ClearableFileInput
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

// WithTrimWhitespace controls CharField cleaning. Password forms should retain
// whitespace explicitly; the widget itself never changes a field's value rules.
func WithTrimWhitespace(trim bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.trimWhitespace, config.hasTrimWhitespace = trim, true })
}

// WithStringNormalizer supplies a pure string conversion after whitespace
// handling and before required/length/field validation. It is only supported
// for unenumerated Char fields and must be safe for concurrent Spec use.
// It never rewrites raw redisplay input or initial model values.
func WithStringNormalizer(normalize func(string) string) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.normalizeString, config.hasStringNormalizer = normalize, true })
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
	allowEmptyFile      bool
	hasAllowEmptyFile   bool
	normalizeString     func(string) string
	hasStringNormalizer bool
	trimWhitespace      bool
	hasTrimWhitespace   bool
	label               string
	widget              Widget
	hasWidget           bool
	choices             []Choice
	modelChoice         bool
	emptyValue          Value
	hasEmptyValue       bool
	required            bool
	nullable            bool
	maxLength           int
	decimalDigits       int
	decimalPlaces       int
	defaultValue        Value
	hasDefault          bool
	validators          []FieldValidator
}

// Field is an immutable form field definition.
type Field struct {
	allowEmptyFile  bool
	normalizeString func(string) string
	trimWhitespace  bool
	name            string
	label           string
	kind            FieldKind
	widget          Widget
	choices         []Choice
	modelChoice     bool
	inlineParent    bool
	emptyValue      Value
	required        bool
	nullable        bool
	maxLength       int
	decimalDigits   int
	decimalPlaces   int
	defaultValue    Value
	hasDefault      bool
	validators      []FieldValidator
}

// ConfigError reports a startup-time invalid form definition.
type ConfigError struct {
	Path string
	Code string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("forms: %s: %s", e.Path, e.Code)
}

// CharField creates a Unicode string field, stripping whitespace by default.
func CharField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TextInput, trimWhitespace: true}
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
	if config.hasAllowEmptyFile && kind != FieldFile {
		return Field{}, &ConfigError{Path: "fields." + name + ".allow_empty_file", Code: "unsupported"}
	}
	if config.hasStringNormalizer && (!stringFieldKind(kind) || config.normalizeString == nil || config.choices != nil || config.modelChoice) {
		return Field{}, &ConfigError{Path: "fields." + name + ".normalizer", Code: "invalid"}
	}
	if config.hasTrimWhitespace && !stringFieldKind(kind) {
		return Field{}, &ConfigError{Path: "fields." + name + ".trim_whitespace", Code: "unsupported"}
	}
	if !validName(name) {
		return Field{}, &ConfigError{Path: "fields", Code: "invalid_name"}
	}
	if config.label == "" || !utf8.ValidString(config.label) || strings.ContainsRune(config.label, 0) {
		return Field{}, &ConfigError{Path: "fields." + name + ".label", Code: "invalid"}
	}
	if config.widget == PasswordInput && (config.hasDefault || config.choices != nil || config.modelChoice) {
		return Field{}, &ConfigError{Path: "fields." + name + ".password", Code: "default_or_choices"}
	}
	if err := validateChoices(name, kind, config); err != nil {
		return Field{}, err
	}
	if config.choices != nil && !config.hasWidget && kind != FieldIntegerList {
		config.widget = Select
	}
	if kind == FieldBoolean && config.nullable && !config.hasWidget {
		config.widget = NullBooleanSelect
	}
	if !(kind == FieldFile && (config.widget == FileInput || config.widget == ClearableFileInput) || kind == FieldIntegerList && config.modelChoice && config.widget == SelectMultiple || kind == FieldJSON && (config.widget == Textarea || config.widget == TextInput) || stringFieldKind(kind) && (config.widget == TextInput || config.widget == Textarea || config.widget == PasswordInput || config.widget == EmailInput) ||
		kind == FieldBoolean && (config.nullable && config.widget == NullBooleanSelect || !config.nullable && config.widget == Checkbox) || kind == FieldInteger && (config.widget == TextInput || config.widget == HiddenInput) || kind == FieldDateTime && (config.widget == DateTimeInput || config.widget == TextInput) ||
		kind == FieldTime && (config.widget == TimeInput || config.widget == TextInput) ||
		(kind == FieldFloat || kind == FieldDecimal) && (config.widget == NumberInput || config.widget == TextInput) ||
		(kind == FieldDuration || kind == FieldUUID) && config.widget == TextInput ||
		kind == FieldDate && (config.widget == DateInput || config.widget == TextInput) ||
		(config.modelChoice || config.choices != nil) && config.widget == Select) {
		return Field{}, &ConfigError{Path: "fields." + name + ".widget", Code: "unsupported_combination"}
	}
	if config.hasEmptyValue {
		if !stringFieldKind(kind) || !(config.emptyValue.IsNull() && config.nullable ||
			config.emptyValue.kind == ValueString && config.emptyValue.text() == "") {
			return Field{}, &ConfigError{Path: "fields." + name + ".empty_value", Code: "unsupported"}
		}
	} else if stringFieldKind(kind) && !config.nullable {
		config.emptyValue = String("")
	}
	for index, validator := range config.validators {
		if nilInterface(validator) {
			return Field{}, &ConfigError{Path: fmt.Sprintf("fields.%s.validators[%d]", name, index), Code: "nil"}
		}
	}
	switch kind {
	case FieldFile:
		if config.maxLength < 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "invalid"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, true) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
	case FieldIntegerList:
		if !config.modelChoice || config.nullable || config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "invalid_multiple_choice"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, false) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
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
	case FieldJSON:
		if config.maxLength != 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "unsupported"}
		}
		if !config.required && !config.nullable {
			return Field{}, &ConfigError{Path: "fields." + name + ".nullable", Code: "optional_json_requires_null"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
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
	case FieldChar, FieldEmail:
		if config.maxLength < 0 {
			return Field{}, &ConfigError{Path: "fields." + name + ".max_length", Code: "invalid"}
		}
		if config.hasDefault && !validValueForField(config.defaultValue, kind, config.nullable) {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "type_mismatch"}
		}
		if config.hasDefault && config.defaultValue.kind == ValueString && config.maxLength > 0 &&
			utf8.RuneCountInString(config.defaultValue.text()) > config.maxLength {
			return Field{}, &ConfigError{Path: "fields." + name + ".default", Code: "max_length"}
		}
		if config.hasDefault && config.defaultValue.kind == ValueString &&
			(!utf8.ValidString(config.defaultValue.text()) || strings.ContainsRune(config.defaultValue.text(), 0)) {
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
		allowEmptyFile:  config.allowEmptyFile,
		normalizeString: config.normalizeString,
		trimWhitespace:  config.trimWhitespace,
		name:            name,
		label:           config.label,
		kind:            kind,
		widget:          config.widget,
		choices:         append([]Choice(nil), config.choices...),
		modelChoice:     config.modelChoice,
		emptyValue:      config.emptyValue,
		required:        config.required,
		nullable:        config.nullable,
		maxLength:       config.maxLength,
		decimalDigits:   config.decimalDigits, decimalPlaces: config.decimalPlaces,
		defaultValue: config.defaultValue,
		hasDefault:   config.hasDefault,
		validators:   append([]FieldValidator(nil), config.validators...),
	}, nil
}

func validValueForField(value Value, kind FieldKind, nullable bool) bool {
	if kind == FieldFile {
		return value.IsNull() || value.kind == ValueFile && value.fileState != nil && !value.fileState.clear && !value.fileState.upload.Valid()
	}
	if kind == FieldIntegerList {
		_, ok := value.AsIntegers()
		return ok
	}
	if value.kind == ValueNull {
		return nullable
	}
	if kind == FieldJSON {
		_, ok := value.AsJSON()
		return ok
	}
	return stringFieldKind(kind) && value.kind == ValueString || kind == FieldBoolean && value.kind == ValueBoolean ||
		kind == FieldInteger && value.kind == ValueInteger || kind == FieldDateTime && value.kind == ValueDateTime || kind == FieldDate && value.kind == ValueDate || kind == FieldTime && value.kind == ValueTime || kind == FieldDuration && value.kind == ValueDuration || kind == FieldFloat && value.kind == ValueFloat || kind == FieldDecimal && value.kind == ValueDecimal || kind == FieldUUID && value.kind == ValueUUID
}

func (f Field) Name() string              { return f.name }
func (f Field) Label() string             { return f.label }
func (f Field) Kind() FieldKind           { return f.kind }
func (f Field) Widget() Widget            { return f.widget }
func (f Field) EmptyValue() (Value, bool) { return f.emptyValue, stringFieldKind(f.kind) }
func (f Field) Required() bool            { return f.required }
func (f Field) Nullable() bool            { return f.nullable }
func (f Field) MaxLength() int            { return f.maxLength }

func (f Field) TrimWhitespace() bool { return f.trimWhitespace }

func (f Field) Default() (Value, bool) { return f.defaultValue, f.hasDefault }

func (f Field) clone() Field {
	clone := f
	clone.choices = append([]Choice(nil), f.choices...)
	clone.validators = append([]FieldValidator(nil), f.validators...)
	return clone
}

// Data is an immutable copy of submitted string values and file capabilities. Presence and an empty
// value are distinct; repeated values are retained for deterministic rejection
// by scalar fields.
type Data struct{ state *submittedData }
type submittedData struct {
	values map[string][]string
	files  map[string][]uploads.File
}

func (d Data) raw(name string) ([]string, bool) {
	if d.state == nil {
		return nil, false
	}
	values, ok := d.state.values[name]
	return values, ok
}

func NewData(values map[string][]string) Data {
	return NewDataWithFiles(values, nil)
}

func (d Data) Get(name string) ([]string, bool) {
	values, ok := d.raw(name)
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

// NewValues snapshots explicitly supplied typed values in lexical name order.
// A value collection is not a validation or persistence authorization token.
func NewValues(values map[string]Value) Values {
	order := make([]string, 0, len(values))
	for name := range values {
		order = append(order, name)
	}
	slices.Sort(order)
	return Values{order: order, values: cloneValueMap(values)}
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
		case field.kind == FieldFile:
			value = Null()
		case field.kind == FieldIntegerList:
			value = Integers()
		case field.kind == FieldBoolean && !field.nullable:
			value = Boolean(false)
		case field.kind == FieldInteger || field.kind == FieldDateTime || field.kind == FieldDate || field.kind == FieldTime || field.kind == FieldDuration || field.kind == FieldFloat || field.kind == FieldDecimal || field.kind == FieldUUID || field.kind == FieldJSON:
			value = Null()
		case stringFieldKind(field.kind):
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

// formBindingToken has nonzero size so distinct live bindings have distinct
// identities. WithErrors retains it; a fresh Bind cannot impersonate a row.
type formBindingToken struct{ marker byte }

// Form is an immutable result of evaluating a Spec.
type Form struct {
	binding   *formBindingToken
	submitted Data
	bound     bool
	readOnly  bool
	valid     bool
	errors    validation.Errors
	cleaned   Values
	initial   Values
	changed   []string
}

func (f Form) Bound() bool               { return f.bound }
func (f Form) ReadOnly() bool            { return f.readOnly }
func (f Form) Valid() bool               { return f.valid }
func (f Form) Errors() validation.Errors { return f.errors }
func (f Form) Cleaned() Values           { return f.cleaned }
func (f Form) Initial() Values           { return f.initial }
func (f Form) Changed() []string         { return append([]string(nil), f.changed...) }

// Submitted preserves the original immutable submission, including omission
// and repeated values. Reading a secret requires explicitly selecting it;
// normal formatting and password widgets continue to redact it.
func (f Form) Submitted() Data { return f.submitted }

// Unbound constructs a form without running validators. Supplied initial values
// are checked for field type and representation, then copied before publication.
// Input length and precision apply to submissions, not existing display values.
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
		var value Value
		var fieldErrors validation.Errors
		if field.kind == FieldFile {
			initialValue, _ := resolvedInitial.Get(field.name)
			value, fieldErrors = cleanFile(field, data, initialValue)
		} else {
			value, fieldErrors = cleanField(field, data)
		}
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
		var sameInitial bool
		if field.inlineParent {
			sameInitial = true
		} else if field.modelChoice || field.kind == FieldFile {
			sameInitial = !fieldChanged(field, data, initialValue)
		} else if field.kind == FieldJSON {
			sameInitial = equalJSON(value, initialValue)
		} else {
			sameInitial = value.Equal(initialValue)
		}
		if !sameInitial {
			changed = append(changed, field.name)
		}
	}
	cleaned := Values{order: cleanedOrder, values: cleanedMap}
	errors := validation.Join(failures...)
	bound := Form{
		binding:   &formBindingToken{},
		submitted: data,
		bound:     true,
		valid:     errors.Empty(),
		errors:    errors,
		cleaned:   cleaned,
		initial:   resolvedInitial,
		changed:   changed,
	}
	for _, validator := range s.cross {
		if failure := validator.ValidateForm(bound.cleaned); !failure.Empty() {
			bound, err = bound.WithErrors(failure)
			if err != nil {
				return Form{}, err
			}
		}
	}
	return bound, nil
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
		if field.inlineParent && !value.Equal(field.defaultValue) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "parent_mismatch"}
		}
		if !validValueForField(value, field.kind, field.nullable) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "type_mismatch"}
		}
		// Existing values are display/change-comparison input, not a new
		// submission. Preserve representable values even when today's input
		// length or precision is narrower. Bind still validates submitted data.
		if value.kind == ValueString && (!utf8.ValidString(value.text()) || strings.ContainsRune(value.text(), 0)) {
			return Values{}, &ConfigError{Path: "initial." + name, Code: "invalid_text"}
		}
	}
	values := cloneValueMap(s.initial.values)
	for name, value := range provided {
		values[name] = value
	}
	return Values{order: s.initial.order, values: values}, nil
}

func cleanField(field Field, data Data) (Value, validation.Errors) {
	submitted, present := data.raw(field.name)
	if len(submitted) > 1 && field.kind != FieldIntegerList {
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "multiple"))
	}
	if field.inlineParent {
		if len(submitted) == 0 || submitted[0] == "" || !field.defaultValue.IsNull() && submitted[0] == modelChoiceInitial(field.defaultValue) {
			return field.defaultValue, validation.Errors{}
		}
		return Null(), validation.NewErrors(validation.New(validation.Field(field.name), "invalid_choice"))
	}
	var value Value
	var failures []validation.Errors
	if field.kind == FieldIntegerList {
		var code validation.Code
		value, code = cleanModelMultipleChoice(field, submitted)
		if code != "" {
			return Null(), validation.NewErrors(validation.New(validation.Field(field.name), code))
		}
	} else if field.modelChoice || field.choices != nil {
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		var code validation.Code
		if field.modelChoice {
			value, code = cleanModelChoice(field, raw)
		} else {
			value, code = cleanChoice(field, raw)
		}
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
		case FieldJSON:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
			}
			var code validation.Code
			value, code = cleanJSON(raw)
			if code == "" && emptyJSON(value) && field.required {
				code = "required"
			}
			if code == "" {
				code = validateJSON(value)
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
		case FieldChar, FieldEmail:
			raw := ""
			if present && len(submitted) == 1 {
				raw = submitted[0]
				if field.trimWhitespace {
					raw = trimStringInput(field.kind, raw)
				}
			}
			if field.normalizeString != nil {
				raw = field.normalizeString(raw)
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
			if field.kind == FieldEmail {
				failures = append(failures, emailinput.FormErrors(field.name, raw, field.maxLength))
				break
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
	if field.kind == FieldFile {
		return fileChanged(field, data)
	}
	if field.inlineParent {
		return false
	}
	submitted, present := data.raw(field.name)
	if field.kind == FieldIntegerList {
		return modelMultipleChoiceChanged(submitted, initial)
	}
	if len(submitted) > 1 {
		return true
	}
	if field.modelChoice {
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		return raw != modelChoiceInitial(initial)
	}
	if field.choices != nil {
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		if raw == "" {
			if field.kind == FieldChar {
				return !field.emptyValue.Equal(initial)
			}
			return !initial.IsNull()
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
	case FieldJSON:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
		}
		return changedJSON(raw, initial)
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
	case FieldChar, FieldEmail:
		raw := ""
		if present && len(submitted) == 1 {
			raw = submitted[0]
			if field.trimWhitespace {
				raw = trimStringInput(field.kind, raw)
			}
		}
		if field.normalizeString != nil {
			raw = field.normalizeString(raw)
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
