package serializers

import "github.com/progresshans/godj/calendar"

// Date is a typed calendar date encoded as a canonical YYYY-MM-DD JSON string.
// An invalid literal is an invalid Value, never an implicit null.
func Date(value calendar.Date) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueDate, string: value.String(), valid: true}
}
func (value Value) AsDate() (calendar.Date, bool) {
	if !value.valid || value.kind != ValueDate {
		return calendar.Date{}, false
	}
	date, err := calendar.Parse(value.string)
	return date, err == nil
}
func DateField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldDate, fieldConfig{required: true}, options)
}
