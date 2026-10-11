package orm

import (
	"math"
	"math/big"

	"github.com/progresshans/godj/duration"
)

// Only AVG admits SQLite's native binary64 microseconds. Source-field and
// SUM/MIN/MAX scanners keep their strict integer/canonical native contract.
// Round at the result boundary: SQLite HAVING must compare the native mean,
// not the rounded model value. A rounded mean can exceed int64 microseconds
// while still fitting the model's full normalized day domain.
type aggregateDurationScanner struct {
	value duration.Duration
	valid bool
}

func (s *aggregateDurationScanner) Scan(raw any) error {
	if s == nil {
		return duration.ErrInvalid
	}
	s.value, s.valid = duration.Duration{}, false
	if raw == nil {
		return nil
	}
	var value duration.Duration
	var err error
	if mean, ok := raw.(float64); ok {
		if math.IsNaN(mean) {
			return duration.ErrInvalid
		}
		if math.IsInf(mean, 0) {
			return duration.ErrRange
		}
		microseconds, _ := new(big.Float).SetFloat64(math.RoundToEven(mean)).Int(nil)
		var days, remainder big.Int
		days.DivMod(microseconds, big.NewInt(duration.MicrosecondsPerDay), &remainder)
		if !days.IsInt64() {
			return duration.ErrRange
		}
		value, err = duration.New(days.Int64(), remainder.Int64())
	} else {
		value, err = scanDuration(raw)
	}
	if err != nil {
		return err
	}
	s.value, s.valid = value, true
	return nil
}

func nullableAggregateDurationCell() scalarCell[*duration.Duration] {
	var scanner aggregateDurationScanner
	return scalarCell[*duration.Duration]{destination: &scanner, value: func() *duration.Duration {
		if !scanner.valid {
			return nil
		}
		value := scanner.value
		return &value
	}}
}
