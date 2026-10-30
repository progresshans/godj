// Package clock represents time of day without a calendar date or time zone.
package clock

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

var ErrInvalid = errors.New("time must be a valid clock value with microsecond precision")

// Time is a comparable wall-clock value. Midnight is the valid zero value;
// nullable fields use *Time so midnight and NULL remain distinct.
type Time struct {
	Hour, Minute, Second, Microsecond int
}

func New(hour, minute, second, microsecond int) (Time, error) {
	value := Time{hour, minute, second, microsecond}
	if !value.Valid() {
		return Time{}, ErrInvalid
	}
	return value, nil
}
func (value Time) Valid() bool {
	return value.Hour >= 0 && value.Hour < 24 && value.Minute >= 0 && value.Minute < 60 && value.Second >= 0 && value.Second < 60 && value.Microsecond >= 0 && value.Microsecond < 1000000
}

// String follows the canonical clock form HH:MM:SS with six fractional digits
// when microseconds are nonzero. It never includes an offset or calendar day.
func (value Time) String() string {
	if !value.Valid() {
		return "invalid time"
	}
	result := fmt.Sprintf("%02d:%02d:%02d", value.Hour, value.Minute, value.Second)
	if value.Microsecond != 0 {
		result += fmt.Sprintf(".%06d", value.Microsecond)
	}
	return result
}

// Parse accepts canonical clock text. Form/JSON input aliases belong to their
// transport parser; offsets and 24:00 are not literal clock values.
func Parse(raw string) (Time, error) {
	if (len(raw) != 8 && len(raw) != 15) || raw[2] != ':' || raw[5] != ':' {
		return Time{}, ErrInvalid
	}
	if len(raw) == 15 && raw[8] != '.' {
		return Time{}, ErrInvalid
	}
	for i, c := range []byte(raw) {
		if i == 2 || i == 5 || i == 8 {
			continue
		}
		if c < '0' || c > '9' {
			return Time{}, ErrInvalid
		}
	}
	hour, _ := strconv.Atoi(raw[:2])
	minute, _ := strconv.Atoi(raw[3:5])
	second, _ := strconv.Atoi(raw[6:8])
	microsecond := 0
	if len(raw) == 15 {
		microsecond, _ = strconv.Atoi(raw[9:])
	}
	value, err := New(hour, minute, second, microsecond)
	if err != nil || value.String() != raw {
		return Time{}, ErrInvalid
	}
	return value, nil
}
func (value Time) MarshalText() ([]byte, error) {
	if !value.Valid() {
		return nil, ErrInvalid
	}
	return []byte(value.String()), nil
}
func (value *Time) UnmarshalText(raw []byte) error {
	if value == nil {
		return ErrInvalid
	}
	parsed, err := Parse(string(raw))
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}
func (value Time) MarshalJSON() ([]byte, error) {
	raw, err := value.MarshalText()
	if err != nil {
		return nil, err
	}
	return []byte(`"` + string(raw) + `"`), nil
}
func (value *Time) UnmarshalJSON(raw []byte) error {
	if value == nil {
		return ErrInvalid
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("%w: expected a clock string", ErrInvalid)
	}
	return value.UnmarshalText([]byte(text))
}
