package forms

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/internal/dateinput"
	"github.com/progresshans/godj/validation"
)

// Date snapshots a valid calendar date. Invalid literals are not NULL.
func Date(value calendar.Date) Value {
	if !value.Valid() {
		return Value{kind: ValueKind(255)}
	}
	return Value{kind: ValueDate, string: value.String()}
}
func (value Value) AsDate() (calendar.Date, bool) {
	if value.kind != ValueDate {
		return calendar.Date{}, false
	}
	date, err := calendar.Parse(value.string)
	return date, err == nil
}
func (values Values) Date(name string) (calendar.Date, bool) {
	value, ok := values.Get(name)
	if !ok {
		return calendar.Date{}, false
	}
	return value.AsDate()
}

// DateField uses the pinned English Django Form date grammar. It never
// accepts a clock or derives a time zone from the process environment.
func DateField(name string, options ...FieldOption) (Field, error) {
	config := fieldConfig{label: name, required: true, widget: DateInput}
	for _, option := range options {
		if option == nil {
			return Field{}, &ConfigError{Path: "fields." + name, Code: "nil_option"}
		}
		option.apply(&config)
	}
	return makeField(name, FieldDate, config)
}
func cleanDate(raw string) (Value, validation.Code) {
	if raw == "" {
		return Null(), ""
	}
	value, err := dateinput.FormEnglish(raw)
	if err != nil {
		return Null(), "invalid"
	}
	return Date(value), ""
}
