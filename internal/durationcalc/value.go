// Package durationcalc retains exact fractional microseconds while a query
// computes a model Duration. Rounding belongs to a materialized result or write,
// not to an arithmetic node, selected-result stage, comparison or aggregate.
package durationcalc

import (
	"errors"
	"math"
	"math/big"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
)

var ErrInvalid = errors.New("invalid exact duration operand")
var ErrZeroDivisor = errors.New("duration division by zero")
var ErrFloatRange = errors.New("duration ratio exceeds finite Float")

// Value is immutable. A copy can share its private number: every operation owns
// its result, and no method exposes or mutates the stored Rat. The zero value
// denotes zero microseconds. Every nonzero value is in the full model domain.
type Value struct {
	number *big.Rat
}

func bounds() (*big.Int, *big.Int) {
	unit := big.NewInt(duration.MicrosecondsPerDay)
	lower := new(big.Int).Mul(big.NewInt(int64(duration.MinDays)), unit)
	upper := new(big.Int).Mul(big.NewInt(int64(duration.MaxDays)+1), unit)
	return lower, upper.Sub(upper, big.NewInt(1))
}

func checked(number *big.Rat) (Value, error) {
	if number.Sign() == 0 {
		return Value{}, nil
	}
	lower, upper := bounds()
	if number.Cmp(new(big.Rat).SetInt(lower)) < 0 || number.Cmp(new(big.Rat).SetInt(upper)) > 0 {
		return Value{}, duration.ErrRange
	}
	return Value{number: new(big.Rat).Set(number)}, nil
}

func (value Value) rat() *big.Rat {
	if value.number == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(value.number)
}

func FromDuration(value duration.Duration) (Value, error) {
	if !value.Valid() {
		return Value{}, duration.ErrInvalid
	}
	total := new(big.Int).Mul(big.NewInt(int64(value.Days)), big.NewInt(duration.MicrosecondsPerDay))
	total.Add(total, big.NewInt(value.Microseconds))
	return checked(new(big.Rat).SetInt(total))
}

// FromMicroseconds accepts the exact SQLite storage representation. The model
// domain is wider than int64, so calculation results do not use this constructor.
func FromMicroseconds(value int64) Value {
	if value == 0 {
		return Value{}
	}
	return Value{number: new(big.Rat).SetInt64(value)}
}

// FromNativeMean preserves the actual finite binary64 emitted by a native AVG.
// Its provenance is a backend responsibility; this does not permit a raw REAL
// value in an ordinary Duration field.
func FromNativeMean(value float64) (Value, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return Value{}, ErrInvalid
	}
	return checked(new(big.Rat).SetFloat64(value))
}

// Factor retains the original integer, binary64 or Decimal value exactly.
// Like Value it has immutable private storage and a valid zero value.
type Factor struct {
	number *big.Rat
}

func IntegerFactor(value int64) Factor {
	return Factor{number: new(big.Rat).SetInt64(value)}
}

func FloatFactor(value float64) (Factor, error) {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return Factor{}, ErrInvalid
	}
	return Factor{number: new(big.Rat).SetFloat64(value)}, nil
}

func DecimalFactor(value decimal.Decimal) (Factor, error) {
	canonical, err := value.Canonical()
	if err != nil || canonical != value {
		return Factor{}, errors.Join(ErrInvalid, err)
	}
	if value.Coefficient == "" || value.Coefficient == "-0" {
		return Factor{}, nil
	}
	number, valid := new(big.Int).SetString(value.Coefficient, 10)
	if !valid {
		return Factor{}, ErrInvalid
	}
	exponent := int64(value.Exponent)
	if exponent < 0 {
		return Factor{number: new(big.Rat).SetFrac(number, new(big.Int).Exp(big.NewInt(10), big.NewInt(-exponent), nil))}, nil
	}
	number.Mul(number, new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil))
	return Factor{number: new(big.Rat).SetInt(number)}, nil
}

func (factor Factor) rat() *big.Rat {
	if factor.number == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(factor.number)
}

func (value Value) Scale(factor Factor) (Value, error) {
	return checked(new(big.Rat).Mul(value.rat(), factor.rat()))
}

func (value Value) Divide(divisor Factor) (Value, error) {
	denominator := divisor.rat()
	if denominator.Sign() == 0 {
		return Value{}, ErrZeroDivisor
	}
	return checked(new(big.Rat).Quo(value.rat(), denominator))
}

func (value Value) Add(other Value) (Value, error) {
	return checked(new(big.Rat).Add(value.rat(), other.rat()))
}

func (value Value) Subtract(other Value) (Value, error) {
	return checked(new(big.Rat).Sub(value.rat(), other.rat()))
}

func (value Value) Negate() (Value, error) {
	return checked(new(big.Rat).Neg(value.rat()))
}

func (value Value) Compare(other Value) int {
	return value.rat().Cmp(other.rat())
}

func (value Value) Ratio(other Value) (float64, error) {
	denominator := other.rat()
	if denominator.Sign() == 0 {
		return 0, ErrZeroDivisor
	}
	result, _ := new(big.Rat).Quo(value.rat(), denominator).Float64()
	if math.IsInf(result, 0) {
		return 0, ErrFloatRange
	}
	return result, nil
}

// RoundOffset rounds the exact sum with an integer origin. It is also used at a
// DateTime result boundary, where rounding the period before adding an odd epoch
// microsecond would change half-even ties. The returned Int is independently owned.
func (value Value) RoundOffset(origin int64) *big.Int {
	number := value.rat()
	number.Add(number, new(big.Rat).SetInt64(origin))
	var whole, remainder big.Int
	whole.QuoRem(number.Num(), number.Denom(), &remainder)
	remainder.Abs(&remainder)
	comparison := remainder.Lsh(&remainder, 1).Cmp(number.Denom())
	if comparison > 0 || comparison == 0 && whole.Bit(0) != 0 {
		whole.Add(&whole, big.NewInt(int64(number.Sign())))
	}
	return &whole
}

func (value Value) Duration() (duration.Duration, error) {
	whole := value.RoundOffset(0)
	var days, remainder big.Int
	days.DivMod(whole, big.NewInt(duration.MicrosecondsPerDay), &remainder)
	if !days.IsInt64() || !remainder.IsInt64() {
		return duration.Duration{}, duration.ErrRange
	}
	return duration.New(days.Int64(), remainder.Int64())
}
