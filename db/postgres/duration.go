package postgres

import (
	"database/sql"
	"github.com/progresshans/godj/duration"
	"regexp"
	"strconv"
	"strings"
)

// database/sql requests interval text from pgx. Only this backend understands
// that driver representation; ORM scanners receive normalized duration text.
var postgresDurationText = regexp.MustCompile(`^(?:(-?[0-9]+) days?(?: ([-+]?[0-9]+):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,6}))?)?|(-?[0-9]+):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,6}))?)$`)

func decodePostgresDuration(raw string) (duration.Duration, error) {
	parts := postgresDurationText.FindStringSubmatch(raw)
	if parts == nil {
		return duration.Duration{}, duration.ErrInvalid
	}
	days := int64(0)
	var err error
	if parts[1] != "" {
		days, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return duration.Duration{}, duration.ErrRange
		}
	}
	hours, minutes, seconds, fraction := parts[2], parts[3], parts[4], parts[5]
	if parts[6] != "" {
		hours, minutes, seconds, fraction = parts[6], parts[7], parts[8], parts[9]
	}
	if hours == "" {
		return duration.New(days, 0)
	}
	hour, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(hours, "-"), "+"), 10, 64)
	// A native interval's time component is int64 microseconds. Bound hours
	// before multiplication; do not use pgx's unchecked text component arithmetic.
	if err != nil || hour > 2562047788 {
		return duration.Duration{}, duration.ErrRange
	}
	minute, _ := strconv.ParseInt(minutes, 10, 64)
	second, _ := strconv.ParseInt(seconds, 10, 64)
	if minute > 59 || second > 59 {
		return duration.Duration{}, duration.ErrInvalid
	}
	micro := int64(0)
	if fraction != "" {
		micro, _ = strconv.ParseInt(fraction+strings.Repeat("0", 6-len(fraction)), 10, 64)
	}
	// Unsigned arithmetic also represents the magnitude of MinInt64.
	total := uint64(hour)*3600000000 + uint64(minute)*60000000 + uint64(second)*1000000 + uint64(micro)
	negative := strings.HasPrefix(hours, "-")
	limit := uint64(1) << 63
	if !negative {
		limit--
	}
	if total > limit {
		return duration.Duration{}, duration.ErrRange
	}
	signed := int64(total)
	if negative {
		signed = -signed
	}
	return duration.New(days, signed)
}

type durationDestination struct{ destination any }

func (target durationDestination) Scan(raw any) error {
	var canonical any
	if raw != nil {
		var text string
		switch value := raw.(type) {
		case string:
			text = value
		case []byte:
			text = string(value)
		default:
			return duration.ErrInvalid
		}
		value, err := decodePostgresDuration(text)
		if err != nil {
			if scanner, ok := target.destination.(sql.Scanner); ok {
				_ = scanner.Scan(nil)
			}
			return err
		}
		canonical = value.String()
	}
	switch destination := target.destination.(type) {
	case sql.Scanner:
		return destination.Scan(canonical)
	case *any:
		if destination == nil {
			return duration.ErrInvalid
		}
		*destination = canonical
		return nil
	case *string:
		if destination == nil || canonical == nil {
			return duration.ErrInvalid
		}
		*destination = canonical.(string)
		return nil
	case *[]byte:
		if destination == nil {
			return duration.ErrInvalid
		}
		if canonical == nil {
			*destination = nil
		} else {
			*destination = []byte(canonical.(string))
		}
		return nil
	default:
		return duration.ErrInvalid
	}
}
