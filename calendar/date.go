// Package calendar represents Gregorian dates without a clock or time zone.
package calendar

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var ErrInvalid = errors.New("date must be a valid Gregorian day in years 1 through 9999")

// Date is a comparable value. Its components are copied when a Date is passed
// to a model, field or query; they never refer to a location or an instant.
// A literal may be invalid, including the zero value. Validate it with New or
// Valid before use. Nullable model fields use *Date, with nil representing NULL.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func New(year int, month time.Month, day int) (Date, error) {
	value := Date{Year: year, Month: month, Day: day}
	if !value.Valid() {
		return Date{}, ErrInvalid
	}
	return value, nil
}

func (value Date) Valid() bool {
	if value.Year < 1 || value.Year > 9999 || value.Month < time.January || value.Month > time.December || value.Day < 1 {
		return false
	}
	days := 31
	switch value.Month {
	case time.April, time.June, time.September, time.November:
		days = 30
	case time.February:
		days = 28
		if value.Year%400 == 0 || value.Year%4 == 0 && value.Year%100 != 0 {
			days = 29
		}
	}
	return value.Day <= days
}

// Parse accepts the canonical YYYY-MM-DD representation. Transport-specific
// input grammars belong to their Form/JSON boundary, not to the stored value.
func Parse(raw string) (Date, error) {
	if len(raw) != 10 || raw[4] != '-' || raw[7] != '-' {
		return Date{}, ErrInvalid
	}
	for index, character := range []byte(raw) {
		if index == 4 || index == 7 {
			continue
		}
		if character < '0' || character > '9' {
			return Date{}, ErrInvalid
		}
	}
	year, _ := strconv.Atoi(raw[:4])
	month, _ := strconv.Atoi(raw[5:7])
	day, _ := strconv.Atoi(raw[8:])
	return New(year, time.Month(month), day)
}

func (value Date) String() string {
	if !value.Valid() {
		return "invalid date"
	}
	return fmt.Sprintf("%04d-%02d-%02d", value.Year, value.Month, value.Day)
}

func (value Date) MarshalText() ([]byte, error) {
	if !value.Valid() {
		return nil, ErrInvalid
	}
	return []byte(value.String()), nil
}

func (value *Date) UnmarshalText(raw []byte) error {
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

func (value Date) MarshalJSON() ([]byte, error) {
	raw, err := value.MarshalText()
	if err != nil {
		return nil, err
	}
	return []byte(`"` + string(raw) + `"`), nil
}

func (value *Date) UnmarshalJSON(raw []byte) error {
	if value == nil {
		return ErrInvalid
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("%w: expected a date string", ErrInvalid)
	}
	return value.UnmarshalText([]byte(text))
}
