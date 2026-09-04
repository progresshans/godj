package serializers

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/progresshans/godj/validation"
)

// FieldKind is the bounded serializer field set implemented by this slice.
type FieldKind uint8

const (
	FieldString FieldKind = iota + 1
	FieldBoolean
	FieldInteger
)

const (
	CodeRequired  validation.Code = "required"
	CodeReadOnly  validation.Code = "read_only"
	CodeNull      validation.Code = "null"
	CodeType      validation.Code = "type"
	CodeBlank     validation.Code = "blank"
	CodeMaxLength validation.Code = "max_length"
	CodeUnknown   validation.Code = "unknown"
)

// FieldOption is closed to this package so serializer structure cannot hide
// arbitrary callbacks or application I/O.
type FieldOption interface {
	apply(*fieldConfig)
}

type fieldOption func(*fieldConfig)

func (option fieldOption) apply(config *fieldConfig) { option(config) }

func WithRequired(required bool) FieldOption {
	return fieldOption(func(config *fieldConfig) { config.required = required })
}

func WithNullable() FieldOption {
	return fieldOption(func(config *fieldConfig) { config.nullable = true })
}

// WithReadOnly removes the field from writable requirements. Supplying it in
// input remains a deterministic validation error rather than being ignored.
func WithReadOnly() FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.readOnly = true
		config.required = false
	})
}

func WithMaxLength(limit int) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.maxLength = limit
		config.maxLengthSet = true
	})
}

func WithDefault(value Value) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.defaultValue = value.clone()
		config.hasDefault = true
		config.required = false
	})
}

func WithAllowEmpty() FieldOption {
	return fieldOption(func(config *fieldConfig) { config.allowEmpty = true })
}

func WithTrimWhitespace(trim bool) FieldOption {
	return fieldOption(func(config *fieldConfig) {
		config.trimWhitespace = trim
		config.trimWhitespaceSet = true
	})
}

type fieldConfig struct {
	required          bool
	nullable          bool
	readOnly          bool
	maxLength         int
	maxLengthSet      bool
	defaultValue      Value
	hasDefault        bool
	allowEmpty        bool
	trimWhitespace    bool
	trimWhitespaceSet bool
}

// Field is an immutable serializer field definition.
type Field struct {
	name           string
	kind           FieldKind
	required       bool
	nullable       bool
	readOnly       bool
	maxLength      int
	defaultValue   Value
	hasDefault     bool
	allowEmpty     bool
	trimWhitespace bool
	valid          bool
}

func StringField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldString, fieldConfig{required: true, trimWhitespace: true}, options)
}

func BooleanField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldBoolean, fieldConfig{required: true}, options)
}

func IntegerField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldInteger, fieldConfig{required: true}, options)
}

func makeField(name string, kind FieldKind, config fieldConfig, options []FieldOption) (Field, error) {
	if !validFieldName(name) {
		return Field{}, invalidConfig("fields", "field name must be an ASCII identifier")
	}
	for index, option := range options {
		typed, ok := option.(fieldOption)
		if !ok || typed == nil {
			return Field{}, invalidConfig(fmt.Sprintf("fields.%s.options[%d]", name, index), "field option is nil or invalid")
		}
		typed.apply(&config)
	}
	if config.readOnly && (config.required || config.hasDefault) {
		return Field{}, invalidConfig("fields."+name, "read-only field cannot be required or have a default")
	}
	if config.hasDefault {
		if !config.defaultValue.validValue() || !valueMatchesField(config.defaultValue, kind, config.nullable) {
			return Field{}, invalidConfig("fields."+name+".default", "default value does not match the field")
		}
	}
	switch kind {
	case FieldString:
		if config.maxLength < 0 {
			return Field{}, invalidConfig("fields."+name+".max_length", "maximum length cannot be negative")
		}
		if config.hasDefault && config.defaultValue.kind == ValueString {
			cleaned := config.defaultValue.string
			if config.trimWhitespace {
				cleaned = strings.TrimSpace(cleaned)
			}
			if cleaned == "" && !config.allowEmpty {
				return Field{}, invalidConfig("fields."+name+".default", "default value cannot be blank")
			}
			if config.maxLength > 0 && utf8.RuneCountInString(cleaned) > config.maxLength {
				return Field{}, invalidConfig("fields."+name+".default", "default value exceeds maximum length")
			}
			config.defaultValue = String(cleaned)
		}
	case FieldBoolean, FieldInteger:
		if config.maxLengthSet || config.allowEmpty || config.trimWhitespaceSet {
			return Field{}, invalidConfig("fields."+name, "string-only option applied to a non-string field")
		}
	default:
		return Field{}, invalidConfig("fields."+name, "field kind is unsupported")
	}
	return Field{
		name:           name,
		kind:           kind,
		required:       config.required,
		nullable:       config.nullable,
		readOnly:       config.readOnly,
		maxLength:      config.maxLength,
		defaultValue:   config.defaultValue.clone(),
		hasDefault:     config.hasDefault,
		allowEmpty:     config.allowEmpty,
		trimWhitespace: config.trimWhitespace,
		valid:          true,
	}, nil
}

func valueMatchesField(value Value, kind FieldKind, nullable bool) bool {
	if value.kind == ValueNull {
		return nullable
	}
	return kind == FieldString && value.kind == ValueString ||
		kind == FieldBoolean && value.kind == ValueBoolean ||
		kind == FieldInteger && value.kind == ValueInteger
}

func validFieldName(name string) bool {
	if name == "" {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			index > 0 && character >= '0' && character <= '9' || index > 0 && character == '_' {
			continue
		}
		return false
	}
	return true
}

func (f Field) Name() string         { return f.name }
func (f Field) Kind() FieldKind      { return f.kind }
func (f Field) Required() bool       { return f.required }
func (f Field) Nullable() bool       { return f.nullable }
func (f Field) ReadOnly() bool       { return f.readOnly }
func (f Field) MaxLength() int       { return f.maxLength }
func (f Field) AllowEmpty() bool     { return f.allowEmpty }
func (f Field) TrimWhitespace() bool { return f.trimWhitespace }

func (f Field) Default() (Value, bool) {
	return f.defaultValue.clone(), f.hasDefault
}

// Mode controls absence semantics. Full mode applies defaults and required
// checks; Partial mode validates only fields supplied by the caller.
type Mode uint8

const (
	ModeFull Mode = iota + 1
	ModePartial
)

// Spec is an immutable ordered serializer definition.
type Spec struct {
	fields []Field
	index  map[string]int
	valid  bool
}

func NewSpec(fields []Field) (Spec, error) {
	if len(fields) == 0 {
		return Spec{}, invalidConfig("fields", "serializer must declare at least one field")
	}
	result := Spec{
		fields: make([]Field, len(fields)),
		index:  make(map[string]int, len(fields)),
		valid:  true,
	}
	for index := range fields {
		field := fields[index]
		if !field.valid || !validFieldName(field.name) {
			return Spec{}, invalidConfig(fmt.Sprintf("fields[%d]", index), "field is zero or invalid")
		}
		if _, duplicate := result.index[field.name]; duplicate {
			return Spec{}, invalidConfig("fields."+field.name, "field name is duplicated")
		}
		result.index[field.name] = index
		field.defaultValue = field.defaultValue.clone()
		result.fields[index] = field
	}
	return result, nil
}

func (s Spec) Fields() []Field {
	if !s.valid {
		return nil
	}
	fields := make([]Field, len(s.fields))
	for index := range s.fields {
		fields[index] = s.fields[index]
		fields[index].defaultValue = s.fields[index].defaultValue.clone()
	}
	return fields
}

// Entry is one ordered cleaned serializer value.
type Entry struct {
	name  string
	value Value
}

func (e Entry) Name() string { return e.name }
func (e Entry) Value() Value { return e.value.clone() }

// Values is an immutable ordered cleaned-value collection.
type Values struct {
	order  []string
	values map[string]Value
}

func newValues(order []string, values map[string]Value) Values {
	result := Values{
		order:  append([]string(nil), order...),
		values: make(map[string]Value, len(values)),
	}
	for name, value := range values {
		result.values[name] = value.clone()
	}
	return result
}

func (v Values) Get(name string) (Value, bool) {
	value, ok := v.values[name]
	return value.clone(), ok
}

func (v Values) All() []Entry {
	entries := make([]Entry, 0, len(v.order))
	for _, name := range v.order {
		if value, ok := v.values[name]; ok {
			entries = append(entries, Entry{name: name, value: value.clone()})
		}
	}
	return entries
}

// Result is the immutable output of one full or partial validation.
type Result struct {
	valid  bool
	errors validation.Errors
	values Values
}

func (r Result) Valid() bool               { return r.valid }
func (r Result) Errors() validation.Errors { return validation.NewErrors(r.errors.All()...) }
func (r Result) Values() Values            { return newValues(r.values.order, r.values.values) }

// Bind validates an ordered object in field declaration order, followed by
// strict unknown-field errors in lexical field-name order.
func (s Spec) Bind(object Object, mode Mode) (Result, error) {
	if !s.valid {
		return Result{}, invalidConfig("spec", "serializer spec is zero or invalid")
	}
	if !object.validObject() {
		return Result{}, invalidValue("object", "input object is zero or invalid")
	}
	if mode != ModeFull && mode != ModePartial {
		return Result{}, invalidConfig("mode", "serializer mode is unsupported")
	}
	cleanedOrder := make([]string, 0, len(s.fields))
	cleanedValues := make(map[string]Value, len(s.fields))
	errorsOut := validation.NewErrors()
	for _, field := range s.fields {
		value, present := object.Get(field.name)
		if field.readOnly {
			if present {
				errorsOut = errorsOut.Append(oneViolation(field.name, CodeReadOnly))
			}
			continue
		}
		if !present {
			if mode == ModePartial {
				continue
			}
			if field.hasDefault {
				cleanedOrder = append(cleanedOrder, field.name)
				cleanedValues[field.name] = field.defaultValue.clone()
				continue
			}
			if field.required {
				errorsOut = errorsOut.Append(oneViolation(field.name, CodeRequired))
			}
			continue
		}
		cleaned, fieldErrors := cleanValue(field, value)
		errorsOut = errorsOut.Append(fieldErrors)
		if fieldErrors.Empty() {
			cleanedOrder = append(cleanedOrder, field.name)
			cleanedValues[field.name] = cleaned
		}
	}
	unknown := make([]string, 0)
	for _, member := range object.members {
		if _, known := s.index[member.name]; !known {
			unknown = append(unknown, member.name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		errorsOut = errorsOut.Append(oneViolation(name, CodeUnknown))
	}
	return Result{
		valid:  errorsOut.Empty(),
		errors: errorsOut,
		values: newValues(cleanedOrder, cleanedValues),
	}, nil
}

func cleanValue(field Field, value Value) (Value, validation.Errors) {
	if value.kind == ValueNull {
		if field.nullable {
			return Null(), validation.NewErrors()
		}
		return Value{}, oneViolation(field.name, CodeNull)
	}
	if !valueMatchesField(value, field.kind, field.nullable) {
		return Value{}, oneViolation(field.name, CodeType)
	}
	if field.kind != FieldString {
		return value.clone(), validation.NewErrors()
	}
	cleaned := value.string
	if field.trimWhitespace {
		cleaned = strings.TrimSpace(cleaned)
	}
	if cleaned == "" && !field.allowEmpty {
		return Value{}, oneViolation(field.name, CodeBlank)
	}
	if field.maxLength > 0 && utf8.RuneCountInString(cleaned) > field.maxLength {
		return Value{}, validation.NewErrors(validation.New(
			validation.Field(field.name),
			CodeMaxLength,
			validation.NewParam("max_length", fmt.Sprintf("%d", field.maxLength)),
		))
	}
	return String(cleaned), validation.NewErrors()
}

func oneViolation(name string, code validation.Code) validation.Errors {
	return validation.NewErrors(validation.New(validation.Field(name), code))
}

func invalidConfig(field, detail string) error {
	return &Error{Code: CodeInvalidConfig, Field: field, Detail: detail}
}
