package forms

import (
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/validation"
	"time"
)

// DateTime records a canonical UTC microsecond value. An unsupported instant
// is invalid and cannot be used as a default or initial value; it is not NULL.
func DateTime(value time.Time) Value {
	canonical, err := temporal.Canonical(value)
	if err != nil {
		return Value{kind: ValueKind(255)}
	}
	return Value{kind: ValueDateTime, integer: canonical.UnixMicro()}
}
func (value Value) AsDateTime() (time.Time, bool) {
	if value.kind != ValueDateTime {
		return time.Time{}, false
	}
	return time.UnixMicro(value.integer).UTC(), true
}
func (values Values) DateTime(name string) (time.Time, bool) {
	value, ok := values.Get(name)
	if !ok {
		return time.Time{}, false
	}
	return value.AsDateTime()
}

// DateTimeField accepts ISO/calendar input. A missing offset explicitly means
// UTC; localized formats and named-zone/DST interpretation are not inferred.
func DateTimeField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: DateTimeInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldDateTime, config)
}
func cleanDateTime(raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	parsed, err := temporal.ParseFormUTC(raw)
	if err != nil {
		return Null(), "invalid"
	}
	return DateTime(parsed), ""
}
