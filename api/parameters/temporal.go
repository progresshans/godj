package parameters

import (
	"time"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/temporal"
)

// CanonicalDuration accepts normalized model text, including the full day
// range. Backend-specific storage ranges remain a separate execution boundary.
func CanonicalDuration() Codec[duration.Duration] {
	return Codec[duration.Duration]{
		schema:       openapi.QueryDuration,
		headerSchema: openapi.HeaderDuration,
		decode: func(raw string) (duration.Duration, bool) {
			value, err := duration.Parse(raw)
			return value, err == nil
		},
	}
}

// RFC3339DateTime requires an explicit offset and complete clock. Accepted
// instants are normalized to UTC microseconds without floating point; additional
// fractional digits are truncated. Defaults must already have that identity.
func RFC3339DateTime(maximumBytes int) Codec[time.Time] {
	return Codec[time.Time]{
		schema: func(fallback *time.Time) (openapi.Schema, error) {
			return openapi.QueryDateTime(maximumBytes, fallback)
		},
		headerSchema: func(headerBytes int, fallback *time.Time) (openapi.Schema, error) {
			return openapi.HeaderDateTime(min(maximumBytes, headerBytes), fallback)
		},
		decode: func(raw string) (time.Time, bool) {
			if len(raw) > maximumBytes {
				return time.Time{}, false
			}
			value, err := temporal.ParseRFC3339(raw)
			return value, err == nil
		},
	}
}
