// Package durationinput owns the Django 6.1 duration input grammar.
// Django and CPython (BSD-3-Clause and PSF) are behavior references;
// see docs/SOURCES.md. Model values themselves remain exact and canonical.
// The grammar expressions adapt Django's standard_duration_re,
// iso8601_duration_re and postgres_interval_re (derived=true), from commit
// fe0a859f537d4238cf49fca39073513206f83122, django/utils/dateparse.py.
// Copyright (c) Django Software Foundation and individual contributors.
// Licensed under BSD-3-Clause; see ../../LICENSE.django and NOTICE.md here.
// Modifications: Go positional/Unicode captures, explicit newline handling,
// colon grouping without lookahead, and normalized Duration output.
package durationinput

import (
	"github.com/progresshans/godj/duration"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var standard = regexp.MustCompile(`^(?:(-?\p{Nd}+) (?:days?, )?)?(-?)((?:\p{Nd}+:){0,2}\p{Nd}+)(?:[.,](\p{Nd}{1,12}))?\n?$`)
var iso = regexp.MustCompile(`^([-+]?)P(?:(\p{Nd}+(?:[.,]\p{Nd}+)?)W)?(?:(\p{Nd}+(?:[.,]\p{Nd}+)?)D)?(?:T(?:(\p{Nd}+(?:[.,]\p{Nd}+)?)H)?(?:(\p{Nd}+(?:[.,]\p{Nd}+)?)M)?(?:(\p{Nd}+(?:[.,]\p{Nd}+)?)S)?)?\n?$`)
var postgres = regexp.MustCompile(`^(?:(-?\p{Nd}+) days? ?)?(?:([-+])?(\p{Nd}+):(\p{Nd}{2}):(\p{Nd}{2})(?:\.(\p{Nd}{1,6}))?)?\n?$`)

type components struct {
	days, weeks, hours, minutes, seconds, micros string
	sign                                         int64
	signedDays                                   bool
}

// Parse accepts standard day/clock, ISO week/day/time, and PostgreSQL day/time
// inputs. Standard fractional seconds truncate to microseconds; ISO fractions
// follow the pinned timedelta float-component rounding, including ties to even.
func Parse(raw string) (duration.Duration, error) {
	if value, err := duration.Parse(raw); err == nil {
		return value, nil
	}
	c := components{sign: 1}
	if m := standard.FindStringSubmatch(raw); m != nil {
		c.days = m[1]
		if m[2] == "-" {
			c.sign = -1
		}
		parts := strings.Split(m[3], ":")
		c.seconds = parts[len(parts)-1]
		if len(parts) > 1 {
			c.minutes = parts[len(parts)-2]
		}
		if len(parts) > 2 {
			c.hours = parts[0]
		}
		digits := []rune(m[4])
		if len(digits) > 6 {
			digits = digits[:6]
		}
		if len(digits) > 0 {
			c.micros = string(digits) + strings.Repeat("0", 6-len(digits))
		}
	} else if m := iso.FindStringSubmatch(raw); m != nil {
		if m[1] == "-" {
			c.sign = -1
		}
		c.signedDays = true
		c.weeks, c.days, c.hours, c.minutes, c.seconds = m[2], m[3], m[4], m[5], m[6]
	} else if m := postgres.FindStringSubmatch(raw); m != nil {
		c.days = m[1]
		if m[2] == "-" {
			c.sign = -1
		}
		c.hours, c.minutes, c.seconds = m[3], m[4], m[5]
		if m[6] != "" {
			c.micros = m[6] + strings.Repeat("0", 6-len([]rune(m[6])))
		}
	} else {
		return duration.Duration{}, duration.ErrInvalid
	}
	days, err := timedelta([]component{{c.days, duration.MicrosecondsPerDay}})
	if err != nil {
		return duration.Duration{}, err
	}
	tail, err := timedelta([]component{{c.micros, 1}, {c.seconds, 1000000}, {c.minutes, 60000000}, {c.hours, 3600000000}, {c.weeks, 7 * duration.MicrosecondsPerDay}})
	if err != nil {
		return duration.Duration{}, err
	}
	if c.signedDays && c.sign < 0 {
		days.Neg(days)
		if _, err := normalized(days); err != nil {
			return duration.Duration{}, err
		}
	}
	if c.sign < 0 {
		tail.Neg(tail)
		if _, err := normalized(tail); err != nil {
			return duration.Duration{}, err
		}
	}
	days.Add(days, tail)
	return normalized(days)
}

type component struct {
	raw  string
	unit int64
}

// CPython accumulates integer contributions exactly and fractional microseconds
// separately before one round-half-even step. Multiplying all components by
// float seconds at once would corrupt long durations and cancellation.
func timedelta(parts []component) (*big.Int, error) {
	total := new(big.Int)
	leftover := 0.0
	for _, part := range parts {
		if part.raw == "" {
			continue
		}
		number, err := strconv.ParseFloat(asciiNumber(part.raw), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, duration.ErrRange
		}
		integer, fraction := math.Modf(number)
		exact, _ := new(big.Float).SetFloat64(integer).Int(nil)
		exact.Mul(exact, big.NewInt(part.unit))
		total.Add(total, exact)
		whole, rest := math.Modf(fraction * float64(part.unit))
		total.Add(total, big.NewInt(int64(whole)))
		leftover += rest
	}
	// At a half tie, include the parity of the exact integer total.
	rounded := math.RoundToEven(leftover)
	if math.Abs(leftover-rounded) == 0.5 {
		floor := math.Floor(leftover)
		parity := new(big.Int).Add(total, big.NewInt(int64(floor))).Bit(0)
		rounded = floor + float64(parity)
	}
	total.Add(total, big.NewInt(int64(rounded)))
	if _, err := normalized(total); err != nil {
		return nil, err
	}
	return total, nil
}

func normalized(total *big.Int) (duration.Duration, error) {
	days, subday := new(big.Int), new(big.Int)
	days.DivMod(total, big.NewInt(duration.MicrosecondsPerDay), subday)
	if !days.IsInt64() || days.Int64() < int64(duration.MinDays) || days.Int64() > int64(duration.MaxDays) {
		return duration.Duration{}, duration.ErrRange
	}
	return duration.Duration{Days: int32(days.Int64()), Microseconds: subday.Int64()}, nil
}
func asciiNumber(raw string) string {
	return strings.Map(func(r rune) rune {
		if r == ',' {
			return '.'
		}
		if r < 128 {
			return r
		}
		for _, span := range unicode.Nd.R16 {
			if r >= rune(span.Lo) && r <= rune(span.Hi) && (r-rune(span.Lo))%rune(span.Stride) == 0 {
				return '0' + (r-rune(span.Lo))/rune(span.Stride)%10
			}
		}
		for _, span := range unicode.Nd.R32 {
			if r >= rune(span.Lo) && r <= rune(span.Hi) && (r-rune(span.Lo))%rune(span.Stride) == 0 {
				return '0' + (r-rune(span.Lo))/rune(span.Stride)%10
			}
		}
		return r
	}, raw)
}

// JSONNumber models DRF's explicit str(JSON-decoded number) ingress. Decimal
// or exponent JSON tokens enter Python's float profile here, not in the common
// JSON value owner. Canonical duration output never uses this lossy conversion.
func JSONNumber(raw string) (duration.Duration, error) {
	if !strings.ContainsAny(raw, ".eE") {
		return Parse(raw)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return duration.Duration{}, duration.ErrInvalid
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	index := strings.LastIndexByte(scientific, 'e')
	exponent, err := strconv.Atoi(scientific[index+1:])
	if err != nil {
		return duration.Duration{}, duration.ErrInvalid
	}
	if exponent >= -4 && exponent < 16 {
		raw = strconv.FormatFloat(value, 'f', -1, 64)
		if !strings.ContainsRune(raw, '.') {
			raw += ".0"
		}
	} else {
		raw = scientific
	}
	return Parse(raw)
}
