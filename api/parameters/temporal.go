package parameters

import (
	"time"

	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/temporal"
)

// CanonicalDate accepts exactly YYYY-MM-DD in the model's Gregorian domain.
// It performs no trimming, clock truncation or time-zone conversion.
func CanonicalDate() Codec[calendar.Date] {
	return Codec[calendar.Date]{
		schema: openapi.QueryDate, headerSchema: openapi.HeaderDate,
		decode: func(raw string) (calendar.Date, bool) {
			value, err := calendar.Parse(raw)
			return value, err == nil
		},
	}
}

// CanonicalTime accepts the model's exact clock spelling and microseconds.
// Midnight is present; offsets, 24:00 and fractional aliases are rejected.
func CanonicalTime() Codec[clock.Time] {
	return Codec[clock.Time]{
		schema: openapi.QueryTime, headerSchema: openapi.HeaderTime,
		decode: func(raw string) (clock.Time, bool) {
			value, err := clock.Parse(raw)
			return value, err == nil
		},
	}
}

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
