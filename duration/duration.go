// Package duration represents normalized elapsed days and microseconds.
package duration

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/progresshans/godj/clock"
)

const MicrosecondsPerDay int64 = 86400000000
const MinDays int32 = -999999999
const MaxDays int32 = 999999999

var ErrInvalid = errors.New("duration must contain normalized days and subday microseconds")
var ErrRange = errors.New("duration is outside the supported day range")
var ErrMicrosecondRange = errors.New("duration exceeds signed int64 microseconds")

// Duration is a comparable elapsed value. Days and Microseconds are normalized:
// Microseconds is the nonnegative offset within one day. Zero is a present zero
// duration; nullable models use *Duration. Backend ranges remain separate from
// the full model range shared with datetime.timedelta.
type Duration struct {
	Days         int32
	Microseconds int64
}

func (value Duration) Valid() bool {
	return value.Days >= MinDays && value.Days <= MaxDays && value.Microseconds >= 0 && value.Microseconds < MicrosecondsPerDay
}

// New normalizes the component sum without overflowing intermediate products.
func New(days int64, microseconds int64) (Duration, error) {
	carry, remainder := splitMicroseconds(microseconds)
	if days < int64(MinDays)-carry || days > int64(MaxDays)-carry {
		return Duration{}, ErrRange
	}
	return Duration{Days: int32(days + carry), Microseconds: remainder}, nil
}
func splitMicroseconds(value int64) (days, remainder int64) {
	days, remainder = value/MicrosecondsPerDay, value%MicrosecondsPerDay
	if remainder < 0 {
		days--
		remainder += MicrosecondsPerDay
	}
	return
}
func FromMicroseconds(value int64) Duration {
	days, remainder := splitMicroseconds(value)
	return Duration{Days: int32(days), Microseconds: remainder}
}

// TotalMicroseconds checks the narrower signed-integer representation required
// by backends such as SQLite. It doesn't reduce the model's valid duration range.
func (value Duration) TotalMicroseconds() (int64, error) {
	if !value.Valid() {
		return 0, ErrInvalid
	}
	var total, unit, tail big.Int
	total.SetInt64(int64(value.Days))
	unit.SetInt64(MicrosecondsPerDay)
	tail.SetInt64(value.Microseconds)
	total.Mul(&total, &unit)
	total.Add(&total, &tail)
	if !total.IsInt64() {
		return 0, ErrMicrosecondRange
	}
	return total.Int64(), nil
}

// String uses normalized days and a positive subday clock: minus one
// microsecond is "-1 23:59:59.999999". A zero day prefix is omitted.
func (value Duration) String() string {
	if !value.Valid() {
		return "invalid duration"
	}
	remainder := value.Microseconds
	clockValue := clock.Time{Hour: int(remainder / 3600000000), Minute: int(remainder / 60000000 % 60), Second: int(remainder / 1000000 % 60), Microsecond: int(remainder % 1000000)}
	if value.Days == 0 {
		return clockValue.String()
	}
	return strconv.FormatInt(int64(value.Days), 10) + " " + clockValue.String()
}

// Parse accepts canonical model text only. Transport aliases and fractional
// rounding belong to their separate input boundary.
func Parse(raw string) (Duration, error) {
	if len(raw) < 8 || len(raw) > 26 {
		return Duration{}, ErrInvalid
	}
	days := int64(0)
	part := raw
	if prefix, tail, found := strings.Cut(raw, " "); found {
		parsed, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || parsed == 0 || strconv.FormatInt(parsed, 10) != prefix {
			return Duration{}, ErrInvalid
		}
		days, part = parsed, tail
	}
	clockValue, err := clock.Parse(part)
	if err != nil {
		return Duration{}, ErrInvalid
	}
	subday := int64(clockValue.Hour)*3600000000 + int64(clockValue.Minute)*60000000 + int64(clockValue.Second)*1000000 + int64(clockValue.Microsecond)
	value, err := New(days, subday)
	if err != nil {
		return Duration{}, err
	}
	if value.String() != raw {
		return Duration{}, ErrInvalid
	}
	return value, nil
}
func (value Duration) MarshalText() ([]byte, error) {
	if !value.Valid() {
		return nil, ErrInvalid
	}
	return []byte(value.String()), nil
}
func (value *Duration) UnmarshalText(raw []byte) error {
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
func (value Duration) MarshalJSON() ([]byte, error) {
	text, err := value.MarshalText()
	if err != nil {
		return nil, err
	}
	return []byte(`"` + string(text) + `"`), nil
}
func (value *Duration) UnmarshalJSON(raw []byte) error {
	if value == nil {
		return ErrInvalid
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return fmt.Errorf("%w: expected a duration string", ErrInvalid)
	}
	return value.UnmarshalText([]byte(text))
}
