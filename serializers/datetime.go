package serializers

import (
	"github.com/progresshans/godj/internal/temporal"
	"time"
)

// DateTime is a typed instant encoded as a canonical RFC 3339 JSON string.
// Invalid instants produce an invalid Value, never a null or an epoch fallback.
func DateTime(value time.Time) Value {
	canonical, err := temporal.Canonical(value)
	if err != nil {
		return Value{}
	}
	return Value{kind: ValueDateTime, integer: canonical.UnixMicro(), valid: true}
}
func (value Value) AsDateTime() (time.Time, bool) {
	if !value.valid || value.kind != ValueDateTime {
		return time.Time{}, false
	}
	return time.UnixMicro(value.integer).UTC(), true
}
func DateTimeField(name string, options ...FieldOption) (Field, error) {
	return makeField(name, FieldDateTime, fieldConfig{required: true}, options)
}
