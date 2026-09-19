package serializers

import "github.com/progresshans/godj/clock"

// Time is a typed clock time encoded as a canonical HH:MM:SS[.ffffff] JSON string.
// An invalid literal is an invalid Value, never an implicit null.
func Time(value clock.Time) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueTime, string: value.String(), valid: true}
}
func (value Value) AsTime() (clock.Time, bool) {
	if !value.valid || value.kind != ValueTime {
		return clock.Time{}, false
	}
	time, err := clock.Parse(value.string)
	return time, err == nil
}
func TimeField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldTime, fieldConfig{required: true}, options)
}
