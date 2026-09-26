// Package dateinput owns the separate Form and JSON date input grammars.
// The public calendar parser and database representation remain canonical.
// Behavior references Django 6.1 DateField / utils.dateparse and DRF 3.18.0
// DateField (BSD-3-Clause); this is an independent Go implementation.
package dateinput

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/progresshans/godj/calendar"
)

var calendarISO = regexp.MustCompile(`^(\p{Nd}{4})-(\p{Nd}{1,2})-(\p{Nd}{1,2})\n?$`)
var weekISO = regexp.MustCompile(`^([0-9]{4})(?:-W([0-9]{2})(?:-([1-7]))?|W([0-9]{2})([1-7])?)$`)

// ISO accepts the date forms used by the pinned DRF ISO-8601 boundary:
// calendar dates (including unpadded month/day), compact dates and ISO weeks.
// It never trims input or extracts a date from a datetime string.
func ISO(raw string) (calendar.Date, error) {
	if value, err := calendar.Parse(raw); err == nil {
		return value, nil
	}
	if len(raw) == 8 && asciiDigits(raw) {
		return calendar.New(number(raw[:4]), time.Month(number(raw[4:6])), number(raw[6:]))
	}
	if parts := weekISO.FindStringSubmatch(raw); parts != nil {
		year := number(parts[1])
		week, day := number(parts[2]), 1
		if parts[4] != "" {
			week = number(parts[4])
		}
		if parts[3] != "" {
			day = number(parts[3])
		}
		if parts[5] != "" {
			day = number(parts[5])
		}
		if year < 1 || year > 9999 || week < 1 || week > 53 {
			return calendar.Date{}, calendar.ErrInvalid
		}
		january4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
		mondayOffset := (int(january4.Weekday()) + 6) % 7
		value := january4.AddDate(0, 0, (week-1)*7+day-1-mondayOffset)
		if actualYear, actualWeek := value.ISOWeek(); actualYear != year || actualWeek != week {
			return calendar.Date{}, calendar.ErrInvalid
		}
		y, m, d := value.Date()
		return calendar.New(y, m, d)
	}
	if parts := calendarISO.FindStringSubmatch(raw); parts != nil {
		return calendar.New(number(parts[1]), time.Month(number(parts[2])), number(parts[3]))
	}
	return calendar.Date{}, calendar.ErrInvalid
}

const formDay = `(3[01]|[012]\p{Nd}|\p{Nd}| [1-9])`
const formMonth = `(1[012]|0[1-9]|[1-9])`
const formSpace = `[\p{Z}\t\n\v\f\r\x{001c}-\x{001f}\x{0085}]+`

var formCalendar = regexp.MustCompile(`^(\p{Nd}{4})-` + formMonth + `-` + formDay + `$`)
var formSlash = regexp.MustCompile(`^` + formMonth + `/` + formDay + `/(\p{Nd}{4}|\p{Nd}{2})$`)
var formMonthFirst = regexp.MustCompile(`(?i)^([a-z]+)` + formSpace + formDay + `,?` + formSpace + `(\p{Nd}{4})$`)
var formDayFirst = regexp.MustCompile(`(?i)^` + formDay + formSpace + `([a-z]+),?` + formSpace + `(\p{Nd}{4})$`)

// FormEnglish implements the pinned en-us DATE_INPUT_FORMATS. Locale selection
// is not inferred from the process locale. Blank input presence is handled by
// forms; whitespace-only input remains invalid after stripping.
func FormEnglish(raw string) (calendar.Date, error) {
	raw = strings.TrimFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || r >= 0x1c && r <= 0x1f })
	if parts := formCalendar.FindStringSubmatch(raw); parts != nil {
		return calendar.New(number(parts[1]), time.Month(number(parts[2])), number(strings.TrimSpace(parts[3])))
	}
	if parts := formSlash.FindStringSubmatch(raw); parts != nil {
		year := number(parts[3])
		if len([]rune(parts[3])) == 2 {
			if year <= 68 {
				year += 2000
			} else {
				year += 1900
			}
		}
		return calendar.New(year, time.Month(number(parts[1])), number(strings.TrimSpace(parts[2])))
	}
	if parts := formMonthFirst.FindStringSubmatch(raw); parts != nil {
		return calendar.New(number(parts[3]), englishMonth(parts[1]), number(strings.TrimSpace(parts[2])))
	}
	if parts := formDayFirst.FindStringSubmatch(raw); parts != nil {
		return calendar.New(number(parts[3]), englishMonth(parts[2]), number(strings.TrimSpace(parts[1])))
	}
	return calendar.Date{}, calendar.ErrInvalid
}

func englishMonth(raw string) time.Month {
	for month := time.January; month <= time.December; month++ {
		name := month.String()
		if strings.EqualFold(raw, name) || strings.EqualFold(raw, name[:3]) {
			return month
		}
	}
	return 0
}

func asciiDigits(raw string) bool {
	for _, character := range raw {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// Python's date fallback and strptime numeric directives recognize Unicode Nd.
// Every Nd range consists of ordered decimal cycles, including adjacent sets.
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
