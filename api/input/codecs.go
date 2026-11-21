package input

import (
	"strings"
	"time"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/uuid"
)

// Codec transfers an already cleaned serializer value to its Go type. It does
// not parse or normalize input and cannot declare independent schema rules.
// Its zero value is invalid. All state is private and immutable.
type Codec[T any] struct {
	accepts  func(serializers.FieldKind) bool
	read     func(serializers.Value) (T, bool)
	nullable bool
}

func scalar[T any](kind serializers.FieldKind, read func(serializers.Value) (T, bool)) Codec[T] {
	return Codec[T]{accepts: func(candidate serializers.FieldKind) bool { return candidate == kind }, read: read}
}

// String accepts String, Email, URL and Slug fields. Their distinct input
// validation and normalization remain owned by the serializer field.
func String() Codec[string] {
	return Codec[string]{
		accepts: func(kind serializers.FieldKind) bool {
			return kind == serializers.FieldString || kind == serializers.FieldEmail || kind == serializers.FieldURL || kind == serializers.FieldSlug
		},
		read: func(value serializers.Value) (string, bool) {
			text, ok := value.AsString()
			return strings.Clone(text), ok
		},
	}
}

func Boolean() Codec[bool]  { return scalar(serializers.FieldBoolean, serializers.Value.AsBoolean) }
func Integer() Codec[int64] { return scalar(serializers.FieldInteger, serializers.Value.AsInteger) }
func Float() Codec[float64] { return scalar(serializers.FieldFloat, serializers.Value.AsFloat) }
func DateTime() Codec[time.Time] {
	return scalar(serializers.FieldDateTime, serializers.Value.AsDateTime)
}
func Date() Codec[calendar.Date] { return scalar(serializers.FieldDate, serializers.Value.AsDate) }
func Time() Codec[clock.Time]    { return scalar(serializers.FieldTime, serializers.Value.AsTime) }
func Duration() Codec[duration.Duration] {
	return scalar(serializers.FieldDuration, serializers.Value.AsDuration)
}
func Decimal() Codec[decimal.Decimal] {
	return scalar(serializers.FieldDecimal, serializers.Value.AsDecimal)
}
func UUID() Codec[uuid.UUID] { return scalar(serializers.FieldUUID, serializers.Value.AsUUID) }
func Binary() Codec[binaryvalue.Value] {
	return scalar(serializers.FieldBinary, serializers.Value.AsBinary)
}
func JSON() Codec[jsonvalue.Value] { return scalar(serializers.FieldJSON, serializers.Value.AsJSON) }

// Integers returns a new slice for each effective value, including defaults.
// Order, duplicates and empty arrays retain the serializer's meaning.
func Integers() Codec[[]int64] {
	return scalar(serializers.FieldIntegerList, serializers.Value.AsIntegers)
}

// Nullable requires a nullable serializer field and uses nil only for its
// model-null value. A JSON field's literal-null default remains a non-nil JSON
// value, as in Spec.Bind. Non-null pointers are owned by the returned DTO.
// Applying Nullable to a zero or already nullable codec produces an invalid codec.
func Nullable[T any](codec Codec[T]) Codec[*T] {
	if codec.read == nil || codec.accepts == nil || codec.nullable {
		return Codec[*T]{}
	}
	return Codec[*T]{accepts: codec.accepts, nullable: true, read: func(value serializers.Value) (*T, bool) {
		if value.IsNull() {
			return nil, true
		}
		decoded, ok := codec.read(value)
		if !ok {
			return nil, false
		}
		return &decoded, true
	}}
}
