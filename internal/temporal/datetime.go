// Package temporal owns the common instant representation. Database wire
// encodings remain at the backend boundary; model values are UTC microseconds.
package temporal

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const WireLayout = "2006-01-02T15:04:05.000000Z"
const SQLiteLayout = "2006-01-02 15:04:05.000000"

var ErrInvalid = errors.New("datetime must be a valid instant in UTC years 1 through 9999")

// Canonical removes location/monotonic identity and truncates sub-microsecond
// precision, matching the documented common model and database precision.
// The Go zero time is the valid instant at the start of year 1, not NULL.
func Canonical(value time.Time) (time.Time, error) {
	value = value.UTC()
	if value.Year() < 1 || value.Year() > 9999 {
		return time.Time{}, ErrInvalid
	}
	return value.Truncate(time.Microsecond), nil
}

func Format(value time.Time) string { return value.Format(WireLayout) }

var wirePattern = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?([Zz]|[+-][0-9]{2}:[0-9]{2})$`)
var formPattern = regexp.MustCompile(`^([0-9]{4})-([0-9]{1,2})-([0-9]{1,2})(?:[T ]([0-9]{1,2}):([0-9]{2})(?::([0-9]{2})(?:[.,]([0-9]+))?)?(Z|[+-][0-9]{2}(?::?[0-9]{2})?)?)?$`)

// ParseRFC3339 requires an explicit offset and complete clock. Fractional
// seconds are truncated to microseconds without an intermediate float.
func ParseRFC3339(raw string) (time.Time, error) {
	return parseMatch(wirePattern.FindStringSubmatch(raw), false)
}

// ParseCanonical is the exact historical/default wire shape, independent of
// the more permissive user input parser.
func ParseCanonical(raw string) (time.Time, error) {
	if len(raw) != len(WireLayout) {
		return time.Time{}, ErrInvalid
	}
	value, err := ParseRFC3339(raw)
	if err != nil || Format(value) != raw {
		return time.Time{}, ErrInvalid
	}
	return value, nil
}

// ParseFormUTC gives offset-free ISO/calendar input an explicit UTC meaning.
// Locale-specific formats and named-zone/DST interpretation are separate policy.
func ParseFormUTC(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if value, err := ParseRFC3339(raw); err == nil {
		return value, nil
	}
	return parseMatch(formPattern.FindStringSubmatch(raw), true)
}

func parseMatch(parts []string, allowEndOfDay bool) (time.Time, error) {
	if len(parts) != 9 {
		return time.Time{}, ErrInvalid
	}
	number := func(raw string) int { value, _ := strconv.Atoi(raw); return value }
	year, month, day := number(parts[1]), number(parts[2]), number(parts[3])
	hour, minute, second := number(parts[4]), number(parts[5]), number(parts[6])
	if year < 1 || month < 1 || month > 12 || day < 1 || day > 31 || hour > 24 || minute > 59 || second > 59 {
		return time.Time{}, ErrInvalid
	}
	fraction := parts[7]
	if len(fraction) > 6 {
		fraction = fraction[:6]
	}
	micros := number(fraction + strings.Repeat("0", 6-len(fraction)))
	endOfDay := hour == 24
	if endOfDay {
		if !allowEndOfDay || minute != 0 || second != 0 || micros != 0 {
			return time.Time{}, ErrInvalid
		}
		hour = 0
	}
	offset := 0
	if zone := parts[8]; zone != "" && zone != "Z" && zone != "z" {
		digits := strings.ReplaceAll(zone[1:], ":", "")
		hours, minutes := number(digits[:2]), 0
		if len(digits) == 4 {
			minutes = number(digits[2:])
		}
		if hours > 23 || minutes > 59 {
			return time.Time{}, ErrInvalid
		}
		offset = (hours*60 + minutes) * 60
		if zone[0] == '-' {
			offset = -offset
		}
	}
	local := time.Date(year, time.Month(month), day, hour, minute, second, micros*1000, time.UTC)
	if local.Year() != year || int(local.Month()) != month || local.Day() != day {
		return time.Time{}, ErrInvalid
	}
	if endOfDay {
		local = local.AddDate(0, 0, 1)
		if local.Year() > 9999 {
			return time.Time{}, ErrInvalid
		}
	}
	return Canonical(local.Add(-time.Duration(offset) * time.Second))
}

// FromDatabase accepts the native PostgreSQL/SQLite column value and SQLite's
// text-valued aggregates. It rejects NULL and unrelated scalar types.
func FromDatabase(raw any) (time.Time, error) {
	switch value := raw.(type) {
	case time.Time:
		return Canonical(value)
	case string:
		parsed, err := time.Parse("2006-01-02 15:04:05.999999999", value)
		if err != nil {
			return time.Time{}, ErrInvalid
		}
		return Canonical(parsed)
	case []byte:
		return FromDatabase(string(value))
	default:
		return time.Time{}, ErrInvalid
	}
}
