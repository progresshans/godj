package forms

import (
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/internal/timeinput"
	"github.com/progresshans/godj/validation"
)

// Time snapshots a valid clock time. Invalid literals are not NULL.
func Time(value clock.Time) Value {
	if !value.Valid() {
		return Value{kind: ValueKind(255)}
	}
	return Value{kind: ValueTime, string: value.String()}
}
func (value Value) AsTime() (clock.Time, bool) {
	if value.kind != ValueTime {
		return clock.Time{}, false
	}
	time, err := clock.Parse(value.string)
	return time, err == nil
}
func (values Values) Time(name string) (clock.Time, bool) {
	value, ok := values.Get(name)
	if !ok {
		return clock.Time{}, false
	}
	return value.AsTime()
}

// TimeField uses the pinned English Form clock grammar without an offset.
func TimeField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: TimeInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldTime, config)
}
func cleanTime(raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	value, err := timeinput.FormEnglish(raw)
	if err != nil {
		return Null(), "invalid"
	}
	return Time(value), ""
}
