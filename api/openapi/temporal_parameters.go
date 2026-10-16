package openapi

import (
	"time"

	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/temporal"
	"github.com/progresshans/godj/serializers"
)

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
