package forms

import (
	"errors"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationinput"
	"github.com/progresshans/godj/validation"
)

// Duration snapshots a normalized elapsed value. Invalid literals are not NULL.
func Duration(value duration.Duration) Value {
	if !value.Valid() {
		return Value{kind: ValueKind(255)}
	}
	return Value{kind: ValueDuration, string: value.String()}
}
func (value Value) AsDuration() (duration.Duration, bool) {
	if value.kind != ValueDuration {
		return duration.Duration{}, false
	}
	duration, err := duration.Parse(value.string)
	return duration, err == nil
}
func (values Values) Duration(name string) (duration.Duration, bool) {
	value, ok := values.Get(name)
	if !ok {
		return duration.Duration{}, false
	}
	return value.AsDuration()
}

// DurationField uses the pinned Django day/clock and ISO duration grammar.
func DurationField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TextInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldDuration, config)
}
func cleanDuration(raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	value, err := durationinput.Parse(raw)
	if err != nil {
		if errors.Is(err, duration.ErrRange) {
			return Null(), "overflow"
		}
		return Null(), "invalid"
	}
	return Duration(value), ""
}
