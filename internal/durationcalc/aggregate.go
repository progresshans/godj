package durationcalc

import (
	"errors"
	"math"
	"math/big"
)

var ErrAggregate = errors.New("exact duration aggregate exceeds its row count")

// Aggregate owns its mutable state and must not be copied after first use.
// Every contribution is a validated Value. The accumulator is not itself a
// Duration result: cancellation and averaging may bring it back into range.
// Result checks the final expression domain before any downstream predicate.
type Aggregate struct {
	sum   big.Rat
	count int64
	err   error
}

func (aggregate *Aggregate) Add(value Value) error {
	if aggregate.err != nil {
		return aggregate.err
	}
	if aggregate.count == math.MaxInt64 {
		aggregate.err = ErrAggregate
		return aggregate.err
	}
	aggregate.sum.Add(&aggregate.sum, value.rat())
	aggregate.count++
	return nil
}

func (aggregate *Aggregate) Result(average bool) (Value, bool, error) {
	if aggregate.err != nil {
		return Value{}, false, aggregate.err
	}
	if aggregate.count == 0 {
		return Value{}, false, nil
	}
	number := new(big.Rat).Set(&aggregate.sum)
	if average {
		number.Quo(number, new(big.Rat).SetInt64(aggregate.count))
	}
	value, err := checked(number)
	return value, true, err
}
