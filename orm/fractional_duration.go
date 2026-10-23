package orm

import (
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationcalc"
)

type fractionalDurationScanner struct {
	value duration.Duration
	valid bool
}

func (scanner *fractionalDurationScanner) Scan(raw any) error {
	if scanner == nil {
		return duration.ErrInvalid
	}
	scanner.value, scanner.valid = duration.Duration{}, false
	if raw == nil {
		return nil
	}
	key, ok := raw.([]byte)
	if !ok {
		return durationcalc.ErrKey
	}
	value, err := durationcalc.FromKey(key)
	if err != nil {
		return err
	}
	period, err := value.Duration()
	if err != nil {
		return err
	}
	scanner.value, scanner.valid = period, true
	return nil
}

func nullableFractionalDurationCell() scalarCell[*duration.Duration] {
	var scanner fractionalDurationScanner
	return scalarCell[*duration.Duration]{destination: &scanner, value: func() *duration.Duration {
		if !scanner.valid {
			return nil
		}
		value := scanner.value
		return &value
	}}
}
