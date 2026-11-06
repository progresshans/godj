package openapi

import (
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/serializers"
)

func QueryDate(fallback *calendar.Date) (Schema, error) {
	return dateTextSchema(10, fallback, "query")
}

func HeaderDate(maximumBytes int, fallback *calendar.Date) (Schema, error) {
	if maximumBytes < 10 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.date", "date header budget must be between 10 bytes and 1 MiB")
	}
	return dateTextSchema(10, fallback, "header")
}

func dateTextSchema(maximumBytes int, fallback *calendar.Date, source string) (Schema, error) {
	annotations := []serializers.Member{
		serializers.MemberOf("pattern", serializers.String(`^(?!0000)[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$(?![\s\S])`)),
		serializers.MemberOf("minLength", serializers.Integer(10)),
		serializers.MemberOf("maxLength", serializers.Integer(10)),
		serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximumBytes))),
	}
	if fallback != nil {
		if !fallback.Valid() {
			return Schema{}, schemaConfigError(source+".default", "date default must be a valid Gregorian day in years 1 through 9999")
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.String(fallback.String())))
	}
	return schemaAnnotate(Date(), annotations...)
}

func QueryTime(fallback *clock.Time) (Schema, error) {
	return timeTextSchema(15, fallback, "query")
}

func HeaderTime(maximumBytes int, fallback *clock.Time) (Schema, error) {
	if maximumBytes < 8 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.time", "time header budget must be between 8 bytes and 1 MiB")
	}
	return timeTextSchema(min(15, maximumBytes), fallback, "header")
}

func timeTextSchema(maximumBytes int, fallback *clock.Time, source string) (Schema, error) {
	policy, _ := serializers.NewObject(
		serializers.MemberOf("timezone", serializers.String("none")),
		serializers.MemberOf("precision", serializers.String("microsecond")),
		serializers.MemberOf("inputOffset", serializers.String("reject")),
		serializers.MemberOf("subMicrosecond", serializers.String("reject")),
	)
	annotations := []serializers.Member{
		serializers.MemberOf("pattern", serializers.String(`^([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.(?!000000)[0-9]{6})?$(?![\s\S])`)),
		serializers.MemberOf("minLength", serializers.Integer(8)),
		serializers.MemberOf("maxLength", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-time", policy.Value()),
	}
	if fallback != nil {
		if !fallback.Valid() || len(fallback.String()) > maximumBytes {
			return Schema{}, schemaConfigError(source+".default", "time default must be a valid clock within the byte budget")
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.String(fallback.String())))
	}
	return schemaAnnotate(String(), annotations...)
}

func QueryDuration(fallback *duration.Duration) (Schema, error) {
	return durationTextSchema(26, fallback, "query")
}

func HeaderDuration(maximumBytes int, fallback *duration.Duration) (Schema, error) {
	if maximumBytes < 8 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError("header.duration", "duration header budget must be between 8 bytes and 1 MiB")
	}
	return durationTextSchema(min(26, maximumBytes), fallback, "header")
}

func durationTextSchema(maximumBytes int, fallback *duration.Duration, source string) (Schema, error) {
	// A zero fractional component is omitted by Duration.String. The end
	// assertion also excludes ECMAScript's match before a final newline.
	annotations := []serializers.Member{
		serializers.MemberOf("pattern", serializers.String(`^(-?[1-9][0-9]{0,8} )?([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.(?!000000)[0-9]{6})?$(?![\s\S])`)),
		serializers.MemberOf("minLength", serializers.Integer(8)),
		serializers.MemberOf("maxLength", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-duration", serializers.String("normalized-days-and-microseconds")),
	}
	if fallback != nil {
		if !fallback.Valid() || len(fallback.String()) > maximumBytes {
			return Schema{}, schemaConfigError(source+".default", "duration default is outside the input domain")
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.String(fallback.String())))
	}
	return schemaAnnotate(String(), annotations...)
}

// QueryDateTime requires a complete clock and explicit RFC 3339 offset. The
// shared instant domain owns calendar validity and UTC microsecond truncation.
func QueryDateTime(maximumBytes int, fallback *time.Time) (Schema, error) {
	return dateTimeTextSchema(maximumBytes, fallback, "query")
}

func HeaderDateTime(maximumBytes int, fallback *time.Time) (Schema, error) {
	return dateTimeTextSchema(maximumBytes, fallback, "header")
}

func dateTimeTextSchema(maximumBytes int, fallback *time.Time, source string) (Schema, error) {
	if maximumBytes < 20 || maximumBytes > maximumHeaderValueBytes {
		return Schema{}, schemaConfigError(source+".datetime", "datetime input budget must be between 20 bytes and 1 MiB")
	}
	annotations := []serializers.Member{
		serializers.MemberOf("pattern", serializers.String(`^(?!0000)[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])[Tt]([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]+)?([Zz]|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$(?![\s\S])`)),
		serializers.MemberOf("minLength", serializers.Integer(20)),
		serializers.MemberOf("maxLength", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-max-bytes", serializers.Integer(int64(maximumBytes))),
		serializers.MemberOf("x-godj-input-offset", serializers.String("required")),
	}
	if fallback != nil {
		canonical, err := temporal.Canonical(*fallback)
		// Default returns the supplied Go value directly. Reject a default that
		// would differ from a parsed value in location, monotonic or precision
		// identity instead of publishing a normalized but different schema value.
		if err != nil || canonical != *fallback || len(canonical.Format(time.RFC3339Nano)) > maximumBytes {
			return Schema{}, schemaConfigError(source+".default", "datetime default must be a canonical UTC microsecond value within the byte budget")
		}
		annotations = append(annotations, serializers.MemberOf("default", serializers.String(canonical.Format(time.RFC3339Nano))))
	}
	return schemaAnnotate(DateTime(), annotations...)
}
