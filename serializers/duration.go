package serializers

import "github.com/progresshans/godj/duration"

// Duration is a typed elapsed value encoded as normalized [days ]HH:MM:SS[.ffffff].
// An invalid literal is an invalid Value, never an implicit null.
func Duration(value duration.Duration) Value {
	if !value.Valid() {
		return Value{}
	}
	return Value{kind: ValueDuration, string: value.String(), valid: true}
}
func (value Value) AsDuration() (duration.Duration, bool) {
	if !value.valid || value.kind != ValueDuration {
		return duration.Duration{}, false
	}
	duration, err := duration.Parse(value.string)
	return duration, err == nil
}
func DurationField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldDuration, fieldConfig{required: true}, options)
}
