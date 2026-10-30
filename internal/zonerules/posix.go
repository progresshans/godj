package zonerules

import (
	"strconv"
	"strings"
	"time"
)

// TZif's POSIX footer expresses recurring Gregorian transitions. This parser
// follows RFC 9636 section 3.3, including signed/extended transition hours,
// Julian rules and negative daylight-saving offsets. It reads embedded bytes,
// never the process TZ variable. Names label offsets; they are not lookups.
type recurrence struct {
	standard, daylight int
	start, end         civilRule
	dst                bool
}

type civilRule struct {
	kind                      byte
	day, month, week, seconds int
}

type footerParser struct{ rest string }

func parseRecurrence(text string) (recurrence, error) {
	var result recurrence
	p := footerParser{rest: text}
	if !p.name() {
		return result, ErrUnavailable
	}
	seconds, ok := p.offset()
	if !ok {
		return result, ErrUnavailable
	}
	result.standard = -seconds
	if p.rest == "" {
		return result, nil
	}
	if !p.name() {
		return result, ErrUnavailable
	}
	result.dst, result.daylight = true, result.standard+3600
	if p.rest != "" && p.rest[0] != ',' {
		seconds, ok = p.offset()
		if !ok {
			return recurrence{}, ErrUnavailable
		}
		result.daylight = -seconds
	}
	if p.rest == "" {
		// POSIX's conventional default is also the pinned Go reader's default.
		p.rest = ",M3.2.0,M11.1.0"
	}
	if !p.take(',') {
		return recurrence{}, ErrUnavailable
	}
	result.start, ok = p.rule()
	if !ok || !p.take(',') {
		return recurrence{}, ErrUnavailable
	}
	result.end, ok = p.rule()
	if !ok || p.rest != "" {
		return recurrence{}, ErrUnavailable
	}
	return result, nil
}

func (p *footerParser) take(char byte) bool {
	if p.rest == "" || p.rest[0] != char {
		return false
	}
	p.rest = p.rest[1:]
	return true
}

func (p *footerParser) number(low, high int) (int, bool) {
	end := 0
	for end < len(p.rest) && p.rest[end] >= '0' && p.rest[end] <= '9' {
		end++
	}
	if end == 0 || end > 3 {
		return 0, false
	}
	value, err := strconv.Atoi(p.rest[:end])
	if err != nil || value < low || value > high {
		return 0, false
	}
	p.rest = p.rest[end:]
	return value, true
}

func (p *footerParser) name() bool {
	if p.take('<') {
		end := strings.IndexByte(p.rest, '>')
		if end < 3 {
			return false
		}
		for _, char := range p.rest[:end] {
			if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '+' || char == '-') {
				return false
			}
		}
		p.rest = p.rest[end+1:]
		return true
	}
	end := 0
	for end < len(p.rest) && (p.rest[end] >= 'A' && p.rest[end] <= 'Z' || p.rest[end] >= 'a' && p.rest[end] <= 'z') {
		end++
	}
	if end < 3 {
		return false
	}
	p.rest = p.rest[end:]
	return true
}

func (p *footerParser) offset() (int, bool) {
	sign := 1
	if p.take('-') {
		sign = -1
	} else {
		p.take('+')
	}
	hour, ok := p.number(0, 167)
	if !ok {
		return 0, false
	}
	minute, second := 0, 0
	if p.take(':') {
		minute, ok = p.number(0, 59)
		if !ok {
			return 0, false
		}
		if p.take(':') {
			second, ok = p.number(0, 59)
			if !ok {
				return 0, false
			}
		}
	}
	return sign * (hour*3600 + minute*60 + second), true
}

func (p *footerParser) rule() (civilRule, bool) {
	rule := civilRule{kind: 'N', seconds: 2 * 3600}
	var ok bool
	if p.take('M') {
		rule.kind = 'M'
		rule.month, ok = p.number(1, 12)
		if !ok || !p.take('.') {
			return civilRule{}, false
		}
		rule.week, ok = p.number(1, 5)
		if !ok || !p.take('.') {
			return civilRule{}, false
		}
		rule.day, ok = p.number(0, 6)
	} else if p.take('J') {
		rule.kind = 'J'
		rule.day, ok = p.number(1, 365)
	} else {
		rule.day, ok = p.number(0, 365)
	}
	if !ok {
		return civilRule{}, false
	}
	if p.take('/') {
		rule.seconds, ok = p.offset()
		if !ok {
			return civilRule{}, false
		}
	}
	return rule, true
}

func (rule civilRule) instant(year, previousOffset int) int64 {
	day := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	switch rule.kind {
	case 'M':
		day = time.Date(year, time.Month(rule.month), 1, 0, 0, 0, 0, time.UTC)
		delta := (rule.day-int(day.Weekday())+7)%7 + (rule.week-1)*7
		day = day.AddDate(0, 0, delta)
		if int(day.Month()) != rule.month {
			day = day.AddDate(0, 0, -7)
		}
	case 'J':
		delta := rule.day - 1
		if rule.day >= 60 && year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			delta++
		}
		day = day.AddDate(0, 0, delta)
	default:
		day = day.AddDate(0, 0, rule.day)
	}
	return day.Unix() + int64(rule.seconds-previousOffset)
}
