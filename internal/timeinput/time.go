// Package timeinput owns the distinct pinned Form and JSON clock grammars.
// Django 6.1 and DRF 3.18.0 (BSD-3-Clause) are behavior references; Python
// 3.14.3 supplies the ISO clock profile. See docs/SOURCES.md.
package timeinput

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/progresshans/godj/clock"
)

var isoClock = regexp.MustCompile(`^T?([0-9]{2})(?::([0-9]{2})(?::([0-9]{2})(?:[.,]([0-9]+))?)?|([0-9]{2})(?:([0-9]{2})(?:[.,]([0-9]+))?)?)?$`)
var fallbackClock = regexp.MustCompile(`^(\p{Nd}{1,2}):(\p{Nd}{1,2})(?::(\p{Nd}{1,2})(?:[.,](\p{Nd}{1,12}))?)?\n?$`)
var formClock = regexp.MustCompile(`^(2[0-3]|[0-1]\p{Nd}|\p{Nd}| \p{Nd}):([0-5]\p{Nd}|\p{Nd})(?::(6[0-1]|[0-5]\p{Nd}|\p{Nd})(?:\.([0-9]{1,6}))?)?$`)

// ISO preserves local clock components when an offset is supplied. It never
// converts them to UTC, strips general whitespace, or extracts a datetime's
// clock. End-of-day 24:00 normalizes to midnight in the pinned Python profile.
func ISO(raw string) (clock.Time, error) {
	if strings.ContainsRune(raw, 0) {
		return clock.Time{}, clock.ErrInvalid
	}
	main := raw
	if index := strings.IndexAny(raw, "Z+-"); index >= 0 {
		main, raw = raw[:index], raw[index:]
		if raw != "Z" {
			offset, ok := asciiClock(raw[1:])
			if !ok || strings.HasPrefix(raw[1:], "T") || int64(offset.Hour)*3600000000+int64(offset.Minute)*60000000+int64(offset.Second)*1000000+int64(offset.Microsecond) >= 86400000000 {
				return clock.Time{}, clock.ErrInvalid
			}
		}
		// The pinned ISO parser accepts a separating space before the zone.
		main = strings.TrimSuffix(main, " ")
		value, ok := asciiClock(main)
		if !ok {
			return clock.Time{}, clock.ErrInvalid
		}
		return canonicalISO(value)
	}
	if value, ok := asciiClock(main); ok {
		return canonicalISO(value)
	}
	if parts := fallbackClock.FindStringSubmatch(main); parts != nil {
		return clock.New(number(parts[1]), number(parts[2]), number(parts[3]), fraction(parts[4]))
	}
	return clock.Time{}, clock.ErrInvalid
}

func asciiClock(raw string) (clock.Time, bool) {
	parts := isoClock.FindStringSubmatch(raw)
	if parts == nil {
		return clock.Time{}, false
	}
	minute, second, micro := parts[2], parts[3], parts[4]
	if parts[5] != "" {
		minute, second, micro = parts[5], parts[6], parts[7]
	}
	return clock.Time{Hour: number(parts[1]), Minute: number(minute), Second: number(second), Microsecond: fraction(micro)}, true
}
func canonicalISO(value clock.Time) (clock.Time, error) {
	if value.Hour == 24 && value.Minute == 0 && value.Second == 0 && value.Microsecond == 0 {
		value.Hour = 0
	}
	if !value.Valid() {
		return clock.Time{}, clock.ErrInvalid
	}
	return value, nil
}

// FormEnglish implements the pinned en-us TIME_INPUT_FORMATS: hour/minute,
// optional seconds, and one through six fractional digits after seconds.
func FormEnglish(raw string) (clock.Time, error) {
	raw = strings.TrimFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	if parts := formClock.FindStringSubmatch(raw); parts != nil {
		return clock.New(number(strings.TrimSpace(parts[1])), number(parts[2]), number(parts[3]), fraction(parts[4]))
	}
	return clock.Time{}, clock.ErrInvalid
}
func fraction(raw string) int {
	digits := []rune(raw)
	if len(digits) > 6 {
		digits = digits[:6]
	}
	value := number(string(digits))
	for length := len(digits); length < 6; length++ {
		value *= 10
	}
	return value
}

// Numeric fallback directives recognize decimal Unicode digits. Every Nd
// range consists of decimal cycles; input regexes bound the component lengths.
func number(raw string) int {
	value := 0
	for _, character := range raw {
		digit := -1
		if character >= '0' && character <= '9' {
			digit = int(character - '0')
		} else {
			for _, span := range unicode.Nd.R16 {
				if character >= rune(span.Lo) && character <= rune(span.Hi) && (character-rune(span.Lo))%rune(span.Stride) == 0 {
					digit = int((character-rune(span.Lo))/rune(span.Stride)) % 10
					break
				}
			}
			if digit < 0 {
				for _, span := range unicode.Nd.R32 {
					if character >= rune(span.Lo) && character <= rune(span.Hi) && (character-rune(span.Lo))%rune(span.Stride) == 0 {
						digit = int((character-rune(span.Lo))/rune(span.Stride)) % 10
						break
					}
				}
			}
		}
		if digit < 0 {
			return -1
		}
		value = value*10 + digit
	}
	return value
}
